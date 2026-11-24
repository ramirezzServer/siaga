import type pg from "pg";
import { z } from "zod";

import { parseRows } from "../../shared/adapters/postgres.js";
import { REGION_KINDS, type RegionFilter, type RegionRecord } from "../domain/region.js";
import type { RegionReader } from "../ports/region-reader.js";

const COLUMNS = `
  code, name, level, kind, parent_code,
  ST_Y(label_point) AS latitude, ST_X(label_point) AS longitude,
  ARRAY[ST_XMin(geom), ST_YMin(geom), ST_XMax(geom), ST_YMax(geom)] AS bbox`;

/**
 * Tanpa pencarian: urut kode dengan keyset ($4). Dengan pencarian ($3):
 * potongan nama (ILIKE, metakarakter LIKE di-escape) atau mirip per kata
 * (pg_trgm word_similarity, toleran salah ketik), urut kemiripan.
 */
export const LIST_SQL = `
SELECT ${COLUMNS}
FROM ref.region
WHERE ($1::text IS NULL OR parent_code = $1::text)
  AND ($2::int IS NULL OR level = $2::int)
  AND ($3::text IS NULL
       OR name ILIKE '%' || replace(replace(replace($3::text, '\\', '\\\\'), '%', '\\%'), '_', '\\_') || '%'
       OR $3::text <% name)
  AND ($4::text IS NULL OR code > $4::text)
ORDER BY CASE WHEN $3::text IS NULL THEN 0 ELSE word_similarity($3::text, name) END DESC,
         CASE WHEN $3::text IS NULL THEN 0 ELSE length(name) END,
         code
LIMIT $5::int`;

export const GET_SQL = `
SELECT ${COLUMNS},
  CASE WHEN $2::bool THEN ST_AsGeoJSON(geom, 5)::json END AS geometry
FROM ref.region WHERE code = $1::text`;

const row = z.object({
  code: z.string(),
  name: z.string(),
  level: z.union([z.literal(1), z.literal(2), z.literal(3), z.literal(4)]),
  kind: z.enum(REGION_KINDS),
  parent_code: z.string().nullable(),
  latitude: z.number(),
  longitude: z.number(),
  bbox: z.tuple([z.number(), z.number(), z.number(), z.number()]),
});

const detailRow = row.extend({
  geometry: z
    .object({
      type: z.literal("MultiPolygon"),
      coordinates: z.array(z.array(z.array(z.array(z.number())))),
    })
    .nullable(),
});

function toRecord(r: z.infer<typeof row>, geometry: RegionRecord["geometry"] = null): RegionRecord {
  return {
    code: r.code,
    name: r.name,
    level: r.level,
    kind: r.kind,
    parentCode: r.parent_code,
    labelPoint: { latitude: r.latitude, longitude: r.longitude },
    bbox: r.bbox,
    geometry,
  };
}

/** Adapter PostgreSQL untuk RegionReader. */
export class PgRegionReader implements RegionReader {
  constructor(private readonly pool: pg.Pool) {}

  async list(f: RegionFilter): Promise<RegionRecord[]> {
    const res = await this.pool.query(LIST_SQL, [
      f.parent ?? null,
      f.level ?? null,
      f.search ?? null,
      f.after ?? null,
      f.fetch,
    ]);
    return parseRows("ref.region", row, res.rows).map((r) => toRecord(r));
  }

  async get(code: string, includeGeometry: boolean): Promise<RegionRecord | null> {
    const res = await this.pool.query(GET_SQL, [code, includeGeometry]);
    const r = parseRows("ref.region (detail)", detailRow, res.rows)[0];
    return r ? toRecord(r, r.geometry) : null;
  }
}
