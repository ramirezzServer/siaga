import pg from "pg";

/**
 * Data uji integrasi, sama dengan konvensi test Go geo-processor: semua
 * kejadian tahun 2001 dan wilayah provinsi 99, jadi tidak menyentuh data nyata
 * di database lokal. Ditulis lewat role siaga_geo (pemilik skema); core-api
 * membacanya lewat siaga_core.
 */
export const NOW = new Date("2001-01-02T05:00:00Z");

export const IDS = {
  quake: "11111111-1111-4111-8111-000000000001",
  merged: "11111111-1111-4111-8111-000000000002",
  weather: "11111111-1111-4111-8111-000000000003",
  flood: "11111111-1111-4111-8111-000000000004",
  fire: "11111111-1111-4111-8111-000000000005",
  missing: "11111111-1111-4111-8111-000000000009",
} as const;

export function urls(): { geo: string; core: string } | null {
  const geo = process.env["SIAGA_TEST_DATABASE_URL"];
  const core = process.env["SIAGA_TEST_CORE_DATABASE_URL"];
  return geo && core ? { geo, core } : null;
}

// Data uji tahun 2001 juga ditulis test Go geo-processor dan bisa tertinggal
// (pesan CAP, keadaan titik banjir) dengan FK ke kejadiannya, jadi baris yang
// merujuk kejadian uji dihapus lebih dulu.
const TEST_EVENTS = `SELECT id FROM hazard.event WHERE occurred_at < '2002-01-01'`;

const CLEAN = [
  `DELETE FROM hazard.cap_message WHERE sent < '2002-01-01' OR event_id IN (${TEST_EVENTS})`,
  `DELETE FROM hazard.flood_site WHERE event_id IN (${TEST_EVENTS})`,
  `DELETE FROM hazard.event_source WHERE occurred_at < '2002-01-01' OR event_id IN (${TEST_EVENTS})`,
  `UPDATE hazard.event SET merged_into = NULL, status = 'expired', ended_at = COALESCE(ended_at, occurred_at + interval '1 second')
     WHERE occurred_at < '2002-01-01' AND status = 'merged'`,
  `DELETE FROM hazard.event WHERE occurred_at < '2002-01-01'`,
  `DELETE FROM hazard.impact_region WHERE region_code LIKE '99.%'`,
  `DELETE FROM ref.region WHERE code = '99' OR code LIKE '99.%'`,
];

const sha = `decode(repeat('ab', 32), 'hex')`;

function box(minLon: number, minLat: number, maxLon: number, maxLat: number): string {
  return `ST_Multi(ST_MakeEnvelope(${String(minLon)}, ${String(minLat)}, ${String(maxLon)}, ${String(maxLat)}, 4326))`;
}

const SEED = [
  // Wilayah: provinsi 99 > Kota Uji > Kecamatan Uji > dua kelurahan.
  ...(
    [
      ["99", "provinsi", "Provinsi Uji", box(106, -8, 108, -6)],
      ["99.71", "kota", "Kota Uji", box(106, -8, 108, -6)],
      ["99.71.01", "kecamatan", "Kecamatan Uji", box(106, -8, 108, -6)],
      ["99.71.01.1001", "kelurahan", "Kelurahan Dekat", box(106.99, -7.01, 107.01, -6.99)],
      ["99.71.01.1002", "kelurahan", "Kelurahan Jauh", box(106.99, -7.31, 107.01, -7.29)],
      ["99.71.01.2003", "desa", "Desa Bandungan", box(107.2, -7.01, 107.22, -6.99)],
    ] as const
  ).map(
    ([code, kind, name, geom]) =>
      `INSERT INTO ref.region (code, kind, name, geom, source, source_version) VALUES ('${code}', '${kind}', '${name}', ${geom}, 'test', 'v1')`,
  ),
  // Gempa aktif (BMKG utama, USGS tergabung) dengan dua wilayah terdampak.
  `INSERT INTO hazard.event (id, kind, status, level, primary_source, source_event_id, occurred_at, detected_at,
     expires_at, location, area, title, summary, revision, content_sha256)
   VALUES ('${IDS.quake}', 'quake', 'active', 2, 'bmkg', '20010102100000', '2001-01-02T03:00:00Z',
     '2001-01-02T03:01:00Z', '2001-01-02T09:00:00Z', ST_SetSRID(ST_MakePoint(107, -7), 4326),
     ${box(106.5, -7.5, 107.5, -6.5)}, 'Gempa M4,9 Kota Uji', 'Gempa uji', 1, ${sha})`,
  `INSERT INTO hazard.quake (event_id, magnitude, magnitude_type, depth_km, felt_radius_km, place, tsunami, shakemap_url)
   VALUES ('${IDS.quake}', 4.9, 'M', 10, 80, 'Kota Uji', 'none', 'https://example.org/shake.jpg')`,
  // Gempa ganda yang sudah digabung: tidak boleh muncul di feed.
  `INSERT INTO hazard.event (id, kind, status, merged_into, level, primary_source, source_event_id, occurred_at,
     detected_at, expires_at, ended_at, location, title, summary, revision, content_sha256)
   VALUES ('${IDS.merged}', 'quake', 'merged', '${IDS.quake}', 2, 'usgs', 'us2001abcd', '2001-01-02T03:00:05Z',
     '2001-01-02T03:02:00Z', '2001-01-02T09:00:05Z', '2001-01-02T03:03:00Z', ST_SetSRID(ST_MakePoint(107.02, -7.01), 4326),
     'Gempa M5,0 (USGS)', 'Gempa uji ganda', 1, ${sha})`,
  `INSERT INTO hazard.event_source (source, source_event_id, feed, kind, event_id, occurred_at, location, magnitude,
     magnitude_type, depth_km, place, tsunami, source_url, fetched_at, first_seen_at, content_sha256)
   VALUES
   ('bmkg', '20010102100000', 'bmkg-recent', 'quake', '${IDS.quake}', '2001-01-02T03:00:00Z',
     ST_SetSRID(ST_MakePoint(107, -7), 4326), 4.9, 'M', 10, 'Kota Uji', 'none', '', '2001-01-02T03:01:00Z',
     '2001-01-02T03:01:00Z', ${sha}),
   ('usgs', 'us2001abcd', 'usgs-summary', 'quake', '${IDS.quake}', '2001-01-02T03:00:05Z',
     ST_SetSRID(ST_MakePoint(107.02, -7.01), 4326), 5.0, 'mb', 12, 'Java, Indonesia', 'unknown',
     'https://earthquake.usgs.gov/earthquakes/eventpage/us2001abcd', '2001-01-02T03:02:00Z', '2001-01-02T03:02:00Z', ${sha})`,
  `INSERT INTO hazard.impact_region (event_id, region_code, distance_km, level, within_felt) VALUES
   ('${IDS.quake}', '99.71.01.1002', 30, 2, true),
   ('${IDS.quake}', '99.71.01.1001', 0, 2, true)`,
  // Peringatan cuaca aktif, Siaga.
  `INSERT INTO hazard.event (id, kind, status, level, primary_source, source_event_id, occurred_at, detected_at,
     expires_at, location, area, title, summary, revision, content_sha256)
   VALUES ('${IDS.weather}', 'weather', 'active', 3, 'bmkg', '2.49.0.1.360.0.2001.01.02.04.00.001',
     '2001-01-02T04:00:00Z', '2001-01-02T03:55:00Z', '2001-01-02T10:00:00Z', ST_SetSRID(ST_MakePoint(107.1, -7), 4326),
     ${box(106.98, -7.02, 107.21, -6.98)}, 'Peringatan dini cuaca Kota Uji', 'Hujan lebat', 2, ${sha})`,
  `INSERT INTO hazard.weather (event_id, cap_identifier, cap_msg_type, severity, event, headline, description,
     headline_en, sent_at, effective_at, area_km2, message_count, web_url)
   VALUES ('${IDS.weather}', '2.49.0.1.360.0.2001.01.02.04.00.001', 'update', 'severe', 'Hujan Lebat',
     'Peringatan Dini Cuaca Jawa Barat', 'Hujan lebat disertai kilat', 'Weather early warning',
     '2001-01-02T03:55:00Z', '2001-01-02T04:00:00Z', 105.5, 2, 'https://nowcasting.bmkg.go.id/')`,
  `INSERT INTO hazard.impact_region (event_id, region_code, distance_km, level, within_felt, coverage) VALUES
   ('${IDS.weather}', '99.71.01.1001', 0, 3, true, 0.4),
   ('${IDS.weather}', '99.71.01.2003', 0, 3, true, 1.0)`,
  // Potensi banjir yang sudah berakhir.
  `INSERT INTO hazard.event (id, kind, status, level, primary_source, source_event_id, occurred_at, detected_at,
     expires_at, ended_at, location, title, summary, revision, content_sha256)
   VALUES ('${IDS.flood}', 'flood', 'expired', 1, 'openmeteo', 'river:uji-sungai:20010102', '2001-01-02T00:00:00Z',
     '2001-01-02T00:10:00Z', '2001-01-02T04:30:00Z', '2001-01-02T04:30:00Z', ST_SetSRID(ST_MakePoint(107.3, -6.9), 4326),
     'Potensi banjir Sungai Uji', 'Debit di atas persentil 80', 1, ${sha})`,
  `INSERT INTO hazard.flood (event_id, indicator, site_id, site_name, river, model, issued_at, outlook_level, possible,
     peak_date, max_level, opened_at, last_exceeded_at, thresholds)
   VALUES ('${IDS.flood}', 'discharge', 'river:uji-sungai', 'Sungai Uji Hilir', 'Sungai Uji', 'glofas_seamless_v4',
     '2001-01-02T00:10:00Z', 1, false, '2001-01-03T00:00:00Z', 4, '2001-01-02T00:10:00Z', '2001-01-02T00:10:00Z',
     ARRAY[100, 150, 220, 300]::float8[])`,
  `INSERT INTO hazard.flood_day (event_id, date, level, possible, discharge_m3s, discharge_p75_m3s, discharge_max_m3s,
     from_ensemble, window_hours) VALUES
   ('${IDS.flood}', '2001-01-03T00:00:00Z', 1, false, 120, 130, 140, true, 0),
   ('${IDS.flood}', '2001-01-02T00:00:00Z', 0, false, 90, NULL, NULL, false, 0)`,
  // Titik api latihan (drill): hanya muncul bila drill=true.
  `INSERT INTO hazard.event (id, kind, status, level, primary_source, source_event_id, occurred_at, detected_at,
     expires_at, location, title, summary, drill, revision, content_sha256)
   VALUES ('${IDS.fire}', 'fire', 'active', 2, 'firms', 'VIIRS_NOAA20_NRT:20010102T0100:-7.00000:107.00000',
     '2001-01-02T01:00:00Z', '2001-01-02T01:20:00Z', '2001-01-03T01:00:00Z', ST_SetSRID(ST_MakePoint(107, -7), 4326),
     'Titik api (latihan)', 'Latihan', true, 1, ${sha})`,
  `INSERT INTO hazard.fire (event_id, opened_by, detection_count, overpass_count, high_count, peak_detections,
     peak_overpasses, first_detected_at, last_detected_at, level_reached_at, first_fetched_at, last_fetched_at,
     products, max_frp_mw, siaga_detections, waspada_overpasses)
   VALUES ('${IDS.fire}', 'VIIRS_NOAA20_NRT:20010102T0100:-7.00000:107.00000', 3, 2, 1, 3, 2,
     '2001-01-02T01:00:00Z', '2001-01-02T02:30:00Z', '2001-01-02T02:30:00Z', '2001-01-02T01:20:00Z',
     '2001-01-02T02:50:00Z', ARRAY['VIIRS_NOAA20_NRT', 'MODIS_NRT'], 12.5, 5, 2)`,
];

/** Menghapus lalu menulis ulang data uji dalam satu transaksi. */
export async function seed(geoUrl: string): Promise<void> {
  const client = new pg.Client({ connectionString: geoUrl });
  await client.connect();
  try {
    await client.query("BEGIN");
    for (const sql of [...CLEAN, ...SEED]) {
      await client.query(sql);
    }
    await client.query("COMMIT");
  } catch (err) {
    await client.query("ROLLBACK");
    throw err;
  } finally {
    await client.end();
  }
}

/** Membersihkan data uji setelah selesai. */
export async function clean(geoUrl: string): Promise<void> {
  const client = new pg.Client({ connectionString: geoUrl });
  await client.connect();
  try {
    for (const sql of CLEAN) await client.query(sql);
  } finally {
    await client.end();
  }
}
