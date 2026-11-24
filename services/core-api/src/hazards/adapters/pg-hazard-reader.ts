import type pg from "pg";
import { z } from "zod";

import { parseRows, withSnapshot, type Queryable } from "../../shared/adapters/postgres.js";
import type { FeedFilter } from "../domain/feed.js";
import {
  CAP_MSG_TYPES,
  CAP_SEVERITIES,
  FIRE_PRODUCTS,
  FLOOD_INDICATORS,
  HAZARD_KINDS,
  HAZARD_SOURCES,
  HAZARD_STATUSES,
  QUAKE_FEEDS,
  TSUNAMI_STATUSES,
  type AlertLevel,
  type FireRecord,
  type FloodRecord,
  type HazardDetailRecord,
  type HazardRecord,
  type ImpactedRegionRecord,
  type QuakeRecord,
  type QuakeReportRecord,
  type WeatherRecord,
} from "../domain/hazard.js";
import type { HazardReader } from "../ports/hazard-reader.js";

// Kolom ringkasan kejadian. Area hanya dikirim sebagai bbox; geometri lengkap
// hanya untuk detail dengan include=area (peta memakai vector tiles, fase 2b).
const SUMMARY_COLUMNS = `
  e.id, e.kind, e.level, e.status, e.merged_into, e.primary_source, e.source_event_id,
  e.title, e.summary, e.occurred_at, e.detected_at, e.expires_at, e.ended_at, e.updated_at,
  e.revision, e.drill,
  ST_Y(e.location) AS latitude, ST_X(e.location) AS longitude,
  CASE WHEN e.area IS NULL THEN NULL
       ELSE ARRAY[ST_XMin(e.area), ST_YMin(e.area), ST_XMax(e.area), ST_YMax(e.area)] END AS bbox,
  q.magnitude,
  (SELECT count(*) FROM hazard.impact_region ir WHERE ir.event_id = e.id)::int AS impacted_region_count`;

/**
 * Feed. Kejadian merged tidak pernah ikut (sudah tergabung ke kejadian lain).
 * Mode: active = aktif dan belum lewat expires_at pada $3; all = semua selain
 * merged; at = yang tampil pada $3 (sudah terdeteksi dan belum berakhir).
 */
export const LIST_SQL = `
SELECT ${SUMMARY_COLUMNS}
FROM hazard.event e
LEFT JOIN hazard.quake q ON q.event_id = e.id
WHERE e.status <> 'merged'
  AND (cardinality($1::text[]) = 0 OR e.kind = ANY ($1::text[]))
  AND CASE $2::text
        WHEN 'active' THEN e.status = 'active' AND e.expires_at > $3::timestamptz
        WHEN 'all' THEN true
        WHEN 'at' THEN e.detected_at <= $3::timestamptz
                   AND LEAST(COALESCE(e.ended_at, e.expires_at), e.expires_at) > $3::timestamptz
      END
  AND ($4::timestamptz IS NULL OR e.occurred_at >= $4::timestamptz)
  AND ($5::timestamptz IS NULL OR e.occurred_at < $5::timestamptz)
  AND ($6::int IS NULL OR e.level >= $6::int)
  AND ($7::float8[] IS NULL OR e.location && ST_MakeEnvelope($7[1], $7[2], $7[3], $7[4], 4326))
  AND ($8::bool OR NOT e.drill)
  AND ($9::timestamptz IS NULL OR (e.occurred_at, e.id) < ($9::timestamptz, $10::uuid))
ORDER BY e.occurred_at DESC, e.id DESC
LIMIT $11::int`;

export const DETAIL_SQL = `
SELECT ${SUMMARY_COLUMNS},
  CASE WHEN $2::bool AND e.area IS NOT NULL THEN ST_AsGeoJSON(e.area, 5)::json END AS area
FROM hazard.event e
LEFT JOIN hazard.quake q ON q.event_id = e.id
WHERE e.id = $1::uuid`;

export const KIND_SQL = `SELECT kind FROM hazard.event WHERE id = $1::uuid`;

export const QUAKE_SQL = `
SELECT magnitude, magnitude_type, depth_km, felt_radius_km, place, felt_description,
       tsunami, shakemap_url, source_url
FROM hazard.quake WHERE event_id = $1::uuid`;

// Laporan yang ditarik sumber selalu terlepas dari kejadian (constraint
// event_source_deleted_detached), jadi tidak perlu disaring lagi di sini.
export const QUAKE_REPORTS_SQL = `
SELECT source, feed, source_event_id, occurred_at,
       ST_Y(location) AS latitude, ST_X(location) AS longitude,
       magnitude, magnitude_type, depth_km, place, tsunami, source_url, fetched_at
FROM hazard.event_source WHERE event_id = $1::uuid`;

export const WEATHER_SQL = `
SELECT cap_identifier, cap_msg_type, severity, urgency, certainty, event_code,
       event, headline, description, instruction,
       event_en, headline_en, description_en, instruction_en,
       area_desc, web_url, source_url, sent_at, effective_at, onset_at, area_km2, message_count
FROM hazard.weather WHERE event_id = $1::uuid`;

export const FLOOD_SQL = `
SELECT indicator, site_id, site_name, river, model, issued_at, outlook_level, possible,
       peak_date, max_level, opened_at, last_exceeded_at, thresholds
FROM hazard.flood WHERE event_id = $1::uuid`;

export const FLOOD_DAYS_SQL = `
SELECT date, level, possible, discharge_m3s, discharge_p75_m3s, discharge_max_m3s,
       from_ensemble, rain_3h_mm, rain_6h_mm, rain_24h_mm, window_hours
FROM hazard.flood_day WHERE event_id = $1::uuid ORDER BY date`;

export const FIRE_SQL = `
SELECT detection_count, overpass_count, high_count, first_detected_at, last_detected_at,
       products, max_frp_mw
FROM hazard.fire WHERE event_id = $1::uuid`;

export const IMPACT_COUNT_SQL = `SELECT count(*)::int AS total FROM hazard.impact_region WHERE event_id = $1::uuid`;

// Nama kecamatan dan kabupaten/kota diambil dari induk di ref.region supaya
// klien bisa menulis "Kel. X, Kec. Y, Kota Z" tanpa query tambahan.
export const IMPACT_SQL = `
SELECT ir.region_code AS code, r4.name, r4.kind,
       r3.code AS district_code, r3.name AS district_name,
       r2.code AS city_code, r2.name AS city_name, r2.kind AS city_kind,
       ir.distance_km, ir.level, ir.within_felt, ir.coverage
FROM hazard.impact_region ir
JOIN ref.region r4 ON r4.code = ir.region_code
JOIN ref.region r3 ON r3.code = r4.parent_code
JOIN ref.region r2 ON r2.code = r3.parent_code
WHERE ir.event_id = $1::uuid
ORDER BY ir.distance_km, ir.coverage DESC NULLS LAST, ir.region_code
OFFSET $2::int LIMIT $3::int`;

const level = z.union([
  z.literal(1),
  z.literal(2),
  z.literal(3),
  z.literal(4),
]) satisfies z.ZodType<AlertLevel>;
const nullableNumber = z.number().nullable();

const summaryRow = z.object({
  id: z.uuid(),
  kind: z.enum(HAZARD_KINDS),
  level,
  status: z.enum(HAZARD_STATUSES),
  merged_into: z.uuid().nullable(),
  primary_source: z.enum(HAZARD_SOURCES),
  source_event_id: z.string(),
  title: z.string(),
  summary: z.string(),
  occurred_at: z.date(),
  detected_at: z.date(),
  expires_at: z.date(),
  ended_at: z.date().nullable(),
  updated_at: z.date(),
  revision: z.number().int().min(1),
  drill: z.boolean(),
  latitude: z.number(),
  longitude: z.number(),
  bbox: z.tuple([z.number(), z.number(), z.number(), z.number()]).nullable(),
  magnitude: nullableNumber,
  impacted_region_count: z.number().int().min(0),
});

const detailRow = summaryRow.extend({
  area: z
    .object({
      type: z.literal("MultiPolygon"),
      coordinates: z.array(z.array(z.array(z.array(z.number())))),
    })
    .nullable(),
});

const quakeRow = z.object({
  magnitude: z.number(),
  magnitude_type: z.string(),
  depth_km: z.number(),
  felt_radius_km: z.number(),
  place: z.string(),
  felt_description: z.string(),
  tsunami: z.enum(TSUNAMI_STATUSES),
  shakemap_url: z.string(),
  source_url: z.string(),
});

const reportRow = z.object({
  source: z.enum(HAZARD_SOURCES),
  feed: z.enum(QUAKE_FEEDS),
  source_event_id: z.string(),
  occurred_at: z.date(),
  latitude: z.number(),
  longitude: z.number(),
  magnitude: z.number(),
  magnitude_type: z.string(),
  depth_km: z.number(),
  place: z.string(),
  tsunami: z.enum(TSUNAMI_STATUSES),
  source_url: z.string(),
  fetched_at: z.date(),
});

const weatherRow = z.object({
  cap_identifier: z.string(),
  cap_msg_type: z.enum(CAP_MSG_TYPES),
  severity: z.enum(CAP_SEVERITIES),
  urgency: z.string(),
  certainty: z.string(),
  event_code: z.string(),
  event: z.string(),
  headline: z.string(),
  description: z.string(),
  instruction: z.string(),
  event_en: z.string(),
  headline_en: z.string(),
  description_en: z.string(),
  instruction_en: z.string(),
  area_desc: z.string(),
  web_url: z.string(),
  source_url: z.string(),
  sent_at: z.date(),
  effective_at: z.date(),
  onset_at: z.date().nullable(),
  area_km2: z.number(),
  message_count: z.number().int(),
});

const floodRow = z.object({
  indicator: z.enum(FLOOD_INDICATORS),
  site_id: z.string(),
  site_name: z.string(),
  river: z.string(),
  model: z.string(),
  issued_at: z.date(),
  outlook_level: z.number().int(),
  possible: z.boolean(),
  peak_date: z.date().nullable(),
  max_level: level,
  opened_at: z.date(),
  last_exceeded_at: z.date(),
  thresholds: z.array(z.number()),
});

const floodDayRow = z.object({
  date: z.date(),
  level: z.number().int(),
  possible: z.boolean(),
  discharge_m3s: nullableNumber,
  discharge_p75_m3s: nullableNumber,
  discharge_max_m3s: nullableNumber,
  from_ensemble: z.boolean(),
  rain_3h_mm: nullableNumber,
  rain_6h_mm: nullableNumber,
  rain_24h_mm: nullableNumber,
  window_hours: z.number().int(),
});

const fireRow = z.object({
  detection_count: z.number().int(),
  overpass_count: z.number().int(),
  high_count: z.number().int(),
  first_detected_at: z.date(),
  last_detected_at: z.date(),
  products: z.array(z.enum(FIRE_PRODUCTS)),
  max_frp_mw: nullableNumber,
});

const kindRow = z.object({ kind: z.enum(HAZARD_KINDS) });
const totalRow = z.object({ total: z.number().int() });

const impactRow = z.object({
  code: z.string(),
  name: z.string(),
  kind: z.enum(["kelurahan", "desa"]),
  district_code: z.string(),
  district_name: z.string(),
  city_code: z.string(),
  city_name: z.string(),
  city_kind: z.enum(["kabupaten", "kota"]),
  distance_km: z.number(),
  level: z.number().int(),
  within_felt: z.boolean(),
  coverage: nullableNumber,
});

function toHazard(r: z.infer<typeof summaryRow>, area: HazardRecord["area"] = null): HazardRecord {
  return {
    id: r.id,
    kind: r.kind,
    level: r.level,
    status: r.status,
    mergedInto: r.merged_into,
    primarySource: r.primary_source,
    sourceEventId: r.source_event_id,
    title: r.title,
    summary: r.summary,
    occurredAt: r.occurred_at,
    detectedAt: r.detected_at,
    expiresAt: r.expires_at,
    endedAt: r.ended_at,
    updatedAt: r.updated_at,
    revision: r.revision,
    drill: r.drill,
    location: { latitude: r.latitude, longitude: r.longitude },
    bbox: r.bbox,
    area,
    magnitude: r.magnitude,
    impactedRegionCount: r.impacted_region_count,
  };
}

/** Parameter LIST_SQL dari saringan domain (urutan sama dengan $1..$11). */
export function listParams(f: FeedFilter): unknown[] {
  const instant = f.mode === "active" ? f.now : f.mode === "at" ? f.at : null;
  return [
    [...f.kinds],
    f.mode,
    instant,
    f.since ?? null,
    f.until ?? null,
    f.minLevel ?? null,
    f.bbox ? [...f.bbox] : null,
    f.drill,
    f.after?.occurredAt ?? null,
    f.after?.id ?? null,
    f.fetch,
  ];
}

/** Adapter PostgreSQL untuk HazardReader. */
export class PgHazardReader implements HazardReader {
  constructor(private readonly pool: pg.Pool) {}

  async list(filter: FeedFilter): Promise<HazardRecord[]> {
    const res = await this.pool.query(LIST_SQL, listParams(filter));
    return parseRows("hazard.event (feed)", summaryRow, res.rows).map((r) => toHazard(r));
  }

  async kindOf(id: string): Promise<HazardRecord["kind"] | null> {
    const res = await this.pool.query(KIND_SQL, [id]);
    return parseRows("hazard.event (jenis)", kindRow, res.rows)[0]?.kind ?? null;
  }

  get(id: string, includeArea: boolean): Promise<HazardDetailRecord | null> {
    return withSnapshot(this.pool, async (q) => {
      const res = await q.query(DETAIL_SQL, [id, includeArea]);
      const row = parseRows("hazard.event (detail)", detailRow, res.rows)[0];
      if (!row) return null;
      const hazard = toHazard(row, row.area);
      return {
        hazard,
        quake: hazard.kind === "quake" ? await quake(q, id) : null,
        quakeReports: hazard.kind === "quake" ? await quakeReports(q, id) : [],
        weather: hazard.kind === "weather" ? await weather(q, id) : null,
        flood: hazard.kind === "flood" ? await flood(q, id) : null,
        fire: hazard.kind === "fire" ? await fire(q, id) : null,
      };
    });
  }

  async impactedRegions(
    id: string,
    offset: number,
    limit: number,
  ): Promise<{ total: number; rows: ImpactedRegionRecord[] }> {
    return withSnapshot(this.pool, async (q) => {
      const count = await q.query(IMPACT_COUNT_SQL, [id]);
      const total = parseRows("hazard.impact_region (jumlah)", totalRow, count.rows)[0]?.total ?? 0;
      const res = await q.query(IMPACT_SQL, [id, offset, limit]);
      const rows = parseRows("hazard.impact_region", impactRow, res.rows).map(
        (r): ImpactedRegionRecord => ({
          code: r.code,
          name: r.name,
          kind: r.kind,
          districtCode: r.district_code,
          districtName: r.district_name,
          cityCode: r.city_code,
          cityName: r.city_name,
          cityKind: r.city_kind,
          distanceKm: r.distance_km,
          level: r.level,
          withinFelt: r.within_felt,
          coverage: r.coverage,
        }),
      );
      return { total, rows };
    });
  }
}

async function quake(q: Queryable, id: string): Promise<QuakeRecord | null> {
  const res = await q.query(QUAKE_SQL, [id]);
  const r = parseRows("hazard.quake", quakeRow, res.rows)[0];
  if (!r) return null;
  return {
    magnitude: r.magnitude,
    magnitudeType: r.magnitude_type,
    depthKm: r.depth_km,
    feltRadiusKm: r.felt_radius_km,
    place: r.place,
    feltDescription: r.felt_description,
    tsunami: r.tsunami,
    shakemapUrl: r.shakemap_url,
    sourceUrl: r.source_url,
  };
}

async function quakeReports(q: Queryable, id: string): Promise<QuakeReportRecord[]> {
  const res = await q.query(QUAKE_REPORTS_SQL, [id]);
  return parseRows("hazard.event_source", reportRow, res.rows).map((r) => ({
    source: r.source,
    feed: r.feed,
    sourceEventId: r.source_event_id,
    occurredAt: r.occurred_at,
    location: { latitude: r.latitude, longitude: r.longitude },
    magnitude: r.magnitude,
    magnitudeType: r.magnitude_type,
    depthKm: r.depth_km,
    place: r.place,
    tsunami: r.tsunami,
    sourceUrl: r.source_url,
    fetchedAt: r.fetched_at,
  }));
}

async function weather(q: Queryable, id: string): Promise<WeatherRecord | null> {
  const res = await q.query(WEATHER_SQL, [id]);
  const r = parseRows("hazard.weather", weatherRow, res.rows)[0];
  if (!r) return null;
  return {
    capIdentifier: r.cap_identifier,
    msgType: r.cap_msg_type,
    severity: r.severity,
    urgency: r.urgency,
    certainty: r.certainty,
    eventCode: r.event_code,
    sentAt: r.sent_at,
    effectiveAt: r.effective_at,
    onsetAt: r.onset_at,
    areaDesc: r.area_desc,
    areaKm2: r.area_km2,
    messageCount: r.message_count,
    webUrl: r.web_url,
    sourceUrl: r.source_url,
    textId: {
      event: r.event,
      headline: r.headline,
      description: r.description,
      instruction: r.instruction,
    },
    textEn: {
      event: r.event_en,
      headline: r.headline_en,
      description: r.description_en,
      instruction: r.instruction_en,
    },
  };
}

async function flood(q: Queryable, id: string): Promise<FloodRecord | null> {
  const res = await q.query(FLOOD_SQL, [id]);
  const r = parseRows("hazard.flood", floodRow, res.rows)[0];
  if (!r) return null;
  const days = await q.query(FLOOD_DAYS_SQL, [id]);
  return {
    indicator: r.indicator,
    siteId: r.site_id,
    siteName: r.site_name,
    river: r.river,
    model: r.model,
    issuedAt: r.issued_at,
    outlookLevel: r.outlook_level,
    possible: r.possible,
    peakDate: r.peak_date,
    maxLevel: r.max_level,
    openedAt: r.opened_at,
    lastExceededAt: r.last_exceeded_at,
    thresholds: r.thresholds,
    days: parseRows("hazard.flood_day", floodDayRow, days.rows).map((d) => ({
      date: d.date,
      level: d.level,
      possible: d.possible,
      dischargeM3s: d.discharge_m3s,
      dischargeP75M3s: d.discharge_p75_m3s,
      dischargeMaxM3s: d.discharge_max_m3s,
      fromEnsemble: d.from_ensemble,
      rain3hMm: d.rain_3h_mm,
      rain6hMm: d.rain_6h_mm,
      rain24hMm: d.rain_24h_mm,
      windowHours: d.window_hours,
    })),
  };
}

async function fire(q: Queryable, id: string): Promise<FireRecord | null> {
  const res = await q.query(FIRE_SQL, [id]);
  const r = parseRows("hazard.fire", fireRow, res.rows)[0];
  if (!r) return null;
  return {
    detectionCount: r.detection_count,
    overpassCount: r.overpass_count,
    highCount: r.high_count,
    firstDetectedAt: r.first_detected_at,
    lastDetectedAt: r.last_detected_at,
    products: r.products,
    maxFrpMw: r.max_frp_mw,
  };
}
