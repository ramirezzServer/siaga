import type { components } from "@siaga/api-client";

import type {
  CapTexts,
  FireRecord,
  FloodDayRecord,
  FloodRecord,
  HazardDetailRecord,
  HazardRecord,
  ImpactedRegionRecord,
  QuakeRecord,
  QuakeReportRecord,
  WeatherRecord,
} from "./hazard.js";

type Schemas = components["schemas"];
export type HazardSummaryDto = Schemas["HazardSummary"];
export type HazardDetailDto = Schemas["HazardDetail"];
export type ImpactedRegionDto = Schemas["ImpactedRegion"];
type QuakeDetailDto = Schemas["QuakeDetail"];
type QuakeReportDto = Schemas["QuakeReport"];
type WeatherDetailDto = Schemas["WeatherDetail"];
type FloodDetailDto = Schemas["FloodDetail"];
type FloodDayDto = Schemas["FloodDay"];
type FloodThresholdDto = Schemas["FloodThreshold"];
type FireDetailDto = Schemas["FireDetail"];

const RAIN_WINDOWS = [3, 6, 24] as const;

/** URL kosong di database (bawaan '') menjadi null di API. */
export function urlOrNull(url: string): string | null {
  return url === "" ? null : url;
}

/** Tanggal UTC (tengah malam) sebagai YYYY-MM-DD. */
export function isoDate(d: Date): string {
  return d.toISOString().slice(0, 10);
}

function windowOrNull(hours: number): 3 | 6 | 24 | null {
  return hours === 3 || hours === 6 || hours === 24 ? hours : null;
}

export function toHazardSummary(h: HazardRecord): HazardSummaryDto {
  return {
    id: h.id,
    kind: h.kind,
    level: h.level,
    status: h.status,
    primary_source: h.primarySource,
    source_event_id: h.sourceEventId,
    title: h.title,
    summary: h.summary,
    occurred_at: h.occurredAt.toISOString(),
    detected_at: h.detectedAt.toISOString(),
    expires_at: h.expiresAt.toISOString(),
    ended_at: h.endedAt?.toISOString() ?? null,
    updated_at: h.updatedAt.toISOString(),
    revision: h.revision,
    drill: h.drill,
    location: { latitude: h.location.latitude, longitude: h.location.longitude },
    bbox: h.bbox ? [...h.bbox] : null,
    magnitude: h.magnitude,
    impacted_region_count: h.impactedRegionCount,
  };
}

export function toQuakeReport(r: QuakeReportRecord, primarySource: string): QuakeReportDto {
  return {
    source: r.source,
    feed: r.feed,
    source_event_id: r.sourceEventId,
    primary: r.source === primarySource,
    occurred_at: r.occurredAt.toISOString(),
    location: { latitude: r.location.latitude, longitude: r.location.longitude },
    magnitude: r.magnitude,
    magnitude_type: r.magnitudeType,
    depth_km: r.depthKm,
    place: r.place,
    tsunami: r.tsunami,
    url: urlOrNull(r.sourceUrl),
    fetched_at: r.fetchedAt.toISOString(),
  };
}

/** Laporan sumber utama lebih dulu, lalu menurut sumber, feed, dan ID (urutan stabil). */
export function sortReports(
  reports: readonly QuakeReportRecord[],
  primarySource: string,
): QuakeReportRecord[] {
  const rank = (r: QuakeReportRecord) => (r.source === primarySource ? 0 : 1);
  return [...reports].sort(
    (a, b) =>
      rank(a) - rank(b) ||
      a.source.localeCompare(b.source) ||
      a.feed.localeCompare(b.feed) ||
      a.sourceEventId.localeCompare(b.sourceEventId),
  );
}

export function toQuakeDetail(
  q: QuakeRecord,
  reports: readonly QuakeReportRecord[],
  primarySource: string,
): QuakeDetailDto {
  return {
    magnitude: q.magnitude,
    magnitude_type: q.magnitudeType,
    depth_km: q.depthKm,
    felt_radius_km: q.feltRadiusKm,
    place: q.place,
    felt_description: q.feltDescription,
    tsunami: q.tsunami,
    shakemap_url: urlOrNull(q.shakemapUrl),
    source_url: urlOrNull(q.sourceUrl),
    reports: sortReports(reports, primarySource).map((r) => toQuakeReport(r, primarySource)),
  };
}

function texts(t: CapTexts) {
  return {
    event: t.event,
    headline: t.headline,
    description: t.description,
    instruction: t.instruction,
  };
}

function hasText(t: CapTexts): boolean {
  return t.event !== "" || t.headline !== "" || t.description !== "" || t.instruction !== "";
}

export function toWeatherDetail(w: WeatherRecord): WeatherDetailDto {
  return {
    cap_identifier: w.capIdentifier,
    msg_type: w.msgType,
    severity: w.severity,
    urgency: w.urgency,
    certainty: w.certainty,
    event_code: w.eventCode,
    sent_at: w.sentAt.toISOString(),
    effective_at: w.effectiveAt.toISOString(),
    onset_at: w.onsetAt?.toISOString() ?? null,
    area_desc: w.areaDesc,
    area_km2: w.areaKm2,
    message_count: w.messageCount,
    web_url: urlOrNull(w.webUrl),
    source_url: urlOrNull(w.sourceUrl),
    text_id: texts(w.textId),
    text_en: hasText(w.textEn) ? texts(w.textEn) : null,
  };
}

/** Ambang rata (4 debit atau 12 indeks hujan) menjadi satu set per jendela. */
export function floodThresholds(f: FloodRecord): FloodThresholdDto[] {
  const set = (values: readonly number[], window: 3 | 6 | 24 | null): FloodThresholdDto => {
    const [info, waspada, siaga, bahaya] = values;
    if (
      info === undefined ||
      waspada === undefined ||
      siaga === undefined ||
      bahaya === undefined
    ) {
      throw new Error(`ambang banjir ${f.siteId} tidak lengkap`);
    }
    return { window_hours: window, info, waspada, siaga, bahaya };
  };
  if (f.indicator === "discharge") {
    if (f.thresholds.length !== 4) throw new Error(`ambang debit ${f.siteId} harus 4 nilai`);
    return [set(f.thresholds, null)];
  }
  if (f.thresholds.length !== 12) throw new Error(`ambang hujan ${f.siteId} harus 12 nilai`);
  return RAIN_WINDOWS.map((w, i) => set(f.thresholds.slice(i * 4, i * 4 + 4), w));
}

export function toFloodDay(d: FloodDayRecord): FloodDayDto {
  return {
    date: isoDate(d.date),
    level: d.level,
    possible: d.possible,
    discharge_m3s: d.dischargeM3s,
    discharge_p75_m3s: d.dischargeP75M3s,
    discharge_max_m3s: d.dischargeMaxM3s,
    from_ensemble: d.fromEnsemble,
    rain_3h_mm: d.rain3hMm,
    rain_6h_mm: d.rain6hMm,
    rain_24h_mm: d.rain24hMm,
    window_hours: windowOrNull(d.windowHours),
  };
}

export function toFloodDetail(f: FloodRecord): FloodDetailDto {
  return {
    indicator: f.indicator,
    unit: f.indicator === "discharge" ? "m3/s" : "mm",
    site_id: f.siteId,
    site_name: f.siteName,
    river: f.river === "" ? null : f.river,
    model: f.model,
    issued_at: f.issuedAt.toISOString(),
    outlook_level: f.outlookLevel,
    possible: f.possible,
    peak_date: f.peakDate ? isoDate(f.peakDate) : null,
    max_level: f.maxLevel,
    opened_at: f.openedAt.toISOString(),
    last_exceeded_at: f.lastExceededAt.toISOString(),
    thresholds: floodThresholds(f),
    days: [...f.days].sort((a, b) => a.date.getTime() - b.date.getTime()).map(toFloodDay),
  };
}

export function toFireDetail(f: FireRecord): FireDetailDto {
  return {
    detection_count: f.detectionCount,
    overpass_count: f.overpassCount,
    high_confidence_count: f.highCount,
    first_detected_at: f.firstDetectedAt.toISOString(),
    last_detected_at: f.lastDetectedAt.toISOString(),
    products: [...f.products],
    max_frp_mw: f.maxFrpMw,
  };
}

export function toHazardDetail(d: HazardDetailRecord): HazardDetailDto {
  const h = d.hazard;
  return {
    ...toHazardSummary(h),
    merged_into: h.mergedInto,
    area: h.area,
    quake: d.quake ? toQuakeDetail(d.quake, d.quakeReports, h.primarySource) : null,
    weather: d.weather ? toWeatherDetail(d.weather) : null,
    flood: d.flood ? toFloodDetail(d.flood) : null,
    fire: d.fire ? toFireDetail(d.fire) : null,
  };
}

export function toImpactedRegion(r: ImpactedRegionRecord): ImpactedRegionDto {
  return {
    code: r.code,
    name: r.name,
    kind: r.kind,
    district: { code: r.districtCode, name: r.districtName, kind: "kecamatan" },
    city: { code: r.cityCode, name: r.cityName, kind: r.cityKind },
    distance_km: r.distanceKm,
    level: r.level,
    within_felt: r.withinFelt,
    coverage: r.coverage,
  };
}
