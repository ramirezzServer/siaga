// Package seriessql berisi SQL penyimpanan deret waktu di schema ts. Dipisah
// dari adapter supaya bisa dibaca dan ditinjau sebagai SQL utuh.
package seriessql

// UpsertSite menyisipkan atau memperbarui titik. Kelurahan/desa titik grid
// dan sungai dicari di ref.region (level 4, ST_Covers); bila poligon yang
// disederhanakan saling tumpang, desa terkecil dipilih, lalu kode terkecil.
// Titik kelurahan/desa BMKG memakai kodenya sendiri. Baris hanya ditulis ulang bila isinya berubah.
//
// $1 id, $2 jenis, $3 nama, $4 sungai, $5 lintang, $6 bujur, $7 kode wilayah
// (jenis region), $8 waktu pertama terlihat.
const UpsertSite = `WITH p AS (SELECT ST_SetSRID(ST_MakePoint($6, $5), 4326) AS g)
INSERT INTO ts.site (id, kind, name, river, location, region_code, first_seen_at)
SELECT $1, $2, $3, $4, p.g,
  CASE WHEN $2 = 'region' THEN $7::text ELSE (
    SELECT r.code FROM ref.region r
    WHERE r.level = 4 AND ST_Covers(r.geom, p.g)
    ORDER BY ST_Area(r.geom), r.code LIMIT 1
  ) END,
  $8
FROM p
ON CONFLICT (id) DO UPDATE SET
  kind = EXCLUDED.kind,
  name = EXCLUDED.name,
  river = EXCLUDED.river,
  location = EXCLUDED.location,
  region_code = EXCLUDED.region_code,
  updated_at = now()
WHERE (ts.site.kind, ts.site.name, ts.site.river, ts.site.region_code)
      IS DISTINCT FROM (EXCLUDED.kind, EXCLUDED.name, EXCLUDED.river, EXCLUDED.region_code)
   OR NOT ST_Equals(ts.site.location, EXCLUDED.location)`

// UpsertSeries mencatat deret dan kesegarannya. Sel model, elevasi, dan
// kunci arsip mengikuti pengambilan terbaru; pesan yang lebih tua atau ulangan
// tidak mengubah apa pun.
//
// $1 titik, $2 jenis data, $3 model, $4 sumber, $5 lintang sel, $6 bujur sel,
// $7 elevasi, $8 waktu terbit, $9 waktu ambil, $10 kunci arsip.
const UpsertSeries = `INSERT INTO ts.series (
  site_id, dataset, model, source, cell, elevation_m, last_issued_at, last_fetched_at, last_archive_key
) VALUES ($1, $2, $3, $4, ST_SetSRID(ST_MakePoint($6, $5), 4326), $7, $8, $9, $10)
ON CONFLICT (site_id, dataset, model) DO UPDATE SET
  source = EXCLUDED.source,
  cell = CASE WHEN EXCLUDED.last_fetched_at >= ts.series.last_fetched_at THEN EXCLUDED.cell ELSE ts.series.cell END,
  elevation_m = CASE WHEN EXCLUDED.last_fetched_at >= ts.series.last_fetched_at THEN EXCLUDED.elevation_m ELSE ts.series.elevation_m END,
  last_archive_key = CASE WHEN EXCLUDED.last_fetched_at >= ts.series.last_fetched_at THEN EXCLUDED.last_archive_key ELSE ts.series.last_archive_key END,
  last_issued_at = GREATEST(ts.series.last_issued_at, EXCLUDED.last_issued_at),
  last_fetched_at = GREATEST(ts.series.last_fetched_at, EXCLUDED.last_fetched_at),
  updated_at = now()
WHERE EXCLUDED.last_fetched_at > ts.series.last_fetched_at
   OR EXCLUDED.last_issued_at > ts.series.last_issued_at`

// UpsertWeather menyisipkan langkah prakiraan cuaca dari array sejajar
// ($4..$15) dan melaporkan jumlah baris baru dan baris yang berubah.
// $1 titik, $2 model, $3 waktu terbit.
const UpsertWeather = `WITH up AS (
  INSERT INTO ts.weather_forecast (
    site_id, model, issued_at, valid_time, temperature_c, humidity_pct, precipitation_mm, weather_code,
    cloud_cover_pct, wind_speed_kmh, wind_from_deg, wind_gust_kmh, pressure_hpa, boundary_layer_m, visibility_m
  )
  SELECT $1, $2, $3, r.*
  FROM unnest($4::timestamptz[], $5::real[], $6::real[], $7::real[], $8::smallint[], $9::real[],
              $10::real[], $11::real[], $12::real[], $13::real[], $14::real[], $15::real[]) AS r
  ON CONFLICT (site_id, model, issued_at, valid_time) DO UPDATE SET
    temperature_c = EXCLUDED.temperature_c,
    humidity_pct = EXCLUDED.humidity_pct,
    precipitation_mm = EXCLUDED.precipitation_mm,
    weather_code = EXCLUDED.weather_code,
    cloud_cover_pct = EXCLUDED.cloud_cover_pct,
    wind_speed_kmh = EXCLUDED.wind_speed_kmh,
    wind_from_deg = EXCLUDED.wind_from_deg,
    wind_gust_kmh = EXCLUDED.wind_gust_kmh,
    pressure_hpa = EXCLUDED.pressure_hpa,
    boundary_layer_m = EXCLUDED.boundary_layer_m,
    visibility_m = EXCLUDED.visibility_m
  WHERE (ts.weather_forecast.temperature_c, ts.weather_forecast.humidity_pct, ts.weather_forecast.precipitation_mm,
         ts.weather_forecast.weather_code, ts.weather_forecast.cloud_cover_pct, ts.weather_forecast.wind_speed_kmh,
         ts.weather_forecast.wind_from_deg, ts.weather_forecast.wind_gust_kmh, ts.weather_forecast.pressure_hpa,
         ts.weather_forecast.boundary_layer_m, ts.weather_forecast.visibility_m)
        IS DISTINCT FROM
        (EXCLUDED.temperature_c, EXCLUDED.humidity_pct, EXCLUDED.precipitation_mm, EXCLUDED.weather_code,
         EXCLUDED.cloud_cover_pct, EXCLUDED.wind_speed_kmh, EXCLUDED.wind_from_deg, EXCLUDED.wind_gust_kmh,
         EXCLUDED.pressure_hpa, EXCLUDED.boundary_layer_m, EXCLUDED.visibility_m)
  RETURNING (xmax = 0) AS inserted
)
SELECT count(*) FILTER (WHERE inserted), count(*) FILTER (WHERE NOT inserted) FROM up`

// UpsertAirQuality seperti UpsertWeather untuk ts.aq_forecast ($4..$12).
const UpsertAirQuality = `WITH up AS (
  INSERT INTO ts.aq_forecast (
    site_id, model, issued_at, valid_time, pm2_5_ugm3, pm10_ugm3, co_ugm3, no2_ugm3, so2_ugm3, o3_ugm3,
    aerosol_optical_depth, dust_ugm3
  )
  SELECT $1, $2, $3, r.*
  FROM unnest($4::timestamptz[], $5::real[], $6::real[], $7::real[], $8::real[], $9::real[],
              $10::real[], $11::real[], $12::real[]) AS r
  ON CONFLICT (site_id, model, issued_at, valid_time) DO UPDATE SET
    pm2_5_ugm3 = EXCLUDED.pm2_5_ugm3,
    pm10_ugm3 = EXCLUDED.pm10_ugm3,
    co_ugm3 = EXCLUDED.co_ugm3,
    no2_ugm3 = EXCLUDED.no2_ugm3,
    so2_ugm3 = EXCLUDED.so2_ugm3,
    o3_ugm3 = EXCLUDED.o3_ugm3,
    aerosol_optical_depth = EXCLUDED.aerosol_optical_depth,
    dust_ugm3 = EXCLUDED.dust_ugm3
  WHERE (ts.aq_forecast.pm2_5_ugm3, ts.aq_forecast.pm10_ugm3, ts.aq_forecast.co_ugm3, ts.aq_forecast.no2_ugm3,
         ts.aq_forecast.so2_ugm3, ts.aq_forecast.o3_ugm3, ts.aq_forecast.aerosol_optical_depth, ts.aq_forecast.dust_ugm3)
        IS DISTINCT FROM
        (EXCLUDED.pm2_5_ugm3, EXCLUDED.pm10_ugm3, EXCLUDED.co_ugm3, EXCLUDED.no2_ugm3,
         EXCLUDED.so2_ugm3, EXCLUDED.o3_ugm3, EXCLUDED.aerosol_optical_depth, EXCLUDED.dust_ugm3)
  RETURNING (xmax = 0) AS inserted
)
SELECT count(*) FILTER (WHERE inserted), count(*) FILTER (WHERE NOT inserted) FROM up`

// UpsertDischarge seperti UpsertWeather untuk ts.river_discharge ($4..$11).
const UpsertDischarge = `WITH up AS (
  INSERT INTO ts.river_discharge (
    site_id, model, issued_at, valid_date, discharge_m3s, ensemble_mean_m3s, ensemble_median_m3s,
    ensemble_max_m3s, ensemble_min_m3s, ensemble_p25_m3s, ensemble_p75_m3s
  )
  SELECT $1, $2, $3, r.*
  FROM unnest($4::timestamptz[], $5::real[], $6::real[], $7::real[], $8::real[], $9::real[],
              $10::real[], $11::real[]) AS r
  ON CONFLICT (site_id, model, issued_at, valid_date) DO UPDATE SET
    discharge_m3s = EXCLUDED.discharge_m3s,
    ensemble_mean_m3s = EXCLUDED.ensemble_mean_m3s,
    ensemble_median_m3s = EXCLUDED.ensemble_median_m3s,
    ensemble_max_m3s = EXCLUDED.ensemble_max_m3s,
    ensemble_min_m3s = EXCLUDED.ensemble_min_m3s,
    ensemble_p25_m3s = EXCLUDED.ensemble_p25_m3s,
    ensemble_p75_m3s = EXCLUDED.ensemble_p75_m3s
  WHERE (ts.river_discharge.discharge_m3s, ts.river_discharge.ensemble_mean_m3s, ts.river_discharge.ensemble_median_m3s,
         ts.river_discharge.ensemble_max_m3s, ts.river_discharge.ensemble_min_m3s, ts.river_discharge.ensemble_p25_m3s,
         ts.river_discharge.ensemble_p75_m3s)
        IS DISTINCT FROM
        (EXCLUDED.discharge_m3s, EXCLUDED.ensemble_mean_m3s, EXCLUDED.ensemble_median_m3s, EXCLUDED.ensemble_max_m3s,
         EXCLUDED.ensemble_min_m3s, EXCLUDED.ensemble_p25_m3s, EXCLUDED.ensemble_p75_m3s)
  RETURNING (xmax = 0) AS inserted
)
SELECT count(*) FILTER (WHERE inserted), count(*) FILTER (WHERE NOT inserted) FROM up`
