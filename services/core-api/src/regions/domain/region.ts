import type { components } from "@siaga/api-client";

import { decodeCursor, encodeCursor } from "../../shared/domain/cursor.js";
import type { BBox, MultiPolygon, Point } from "../../shared/domain/geo.js";
import { z } from "zod";

export const REGION_KINDS = [
  "provinsi",
  "kabupaten",
  "kota",
  "kecamatan",
  "kelurahan",
  "desa",
] as const;
export type RegionKind = (typeof REGION_KINDS)[number];

/** Pola kode Kemendagri bertitik, sama dengan kontrak OpenAPI dan constraint ref.region. */
export const REGION_CODE_PATTERN = /^[0-9]{2}(\.[0-9]{2}(\.[0-9]{2}(\.[12][0-9]{3})?)?)?$/;

export interface RegionRecord {
  readonly code: string;
  readonly name: string;
  readonly level: 1 | 2 | 3 | 4;
  readonly kind: RegionKind;
  readonly parentCode: string | null;
  readonly labelPoint: Point;
  readonly bbox: BBox;
  readonly geometry: MultiPolygon | null;
}

export interface RegionQuery {
  readonly parent?: string;
  readonly level?: 1 | 2 | 3 | 4;
  readonly q?: string;
  readonly limit: number;
  readonly cursor?: string;
}

/** Saringan untuk port: tanpa `search` urut kode (keyset `after`), dengan `search` urut kemiripan. */
export interface RegionFilter {
  readonly parent?: string;
  readonly level?: 1 | 2 | 3 | 4;
  readonly search?: string;
  readonly after?: string;
  readonly fetch: number;
}

export type RegionDto = components["schemas"]["Region"];

const afterSchema = z.object({ c: z.string().regex(REGION_CODE_PATTERN) });

export function buildRegionFilter(q: RegionQuery): RegionFilter {
  const search = q.q?.trim();
  return {
    fetch: q.limit + 1,
    ...(q.parent !== undefined && { parent: q.parent }),
    ...(q.level !== undefined && { level: q.level }),
    ...(search && { search }),
    // Dengan pencarian hanya ada satu halaman; kursor diabaikan.
    ...(!search && q.cursor !== undefined && { after: decodeCursor(q.cursor, afterSchema).c }),
  };
}

export function regionPage(
  rows: readonly RegionRecord[],
  filter: RegionFilter,
  limit: number,
): { items: RegionDto[]; next_cursor: string | null } {
  const items = rows.slice(0, limit);
  const last = items.at(-1);
  return {
    items: items.map(toRegion),
    next_cursor:
      !filter.search && rows.length > limit && last ? encodeCursor({ c: last.code }) : null,
  };
}

export function toRegion(r: RegionRecord): RegionDto {
  return {
    code: r.code,
    name: r.name,
    level: r.level,
    kind: r.kind,
    parent_code: r.parentCode,
    label_point: { latitude: r.labelPoint.latitude, longitude: r.labelPoint.longitude },
    bbox: [...r.bbox],
    ...(r.geometry && { geometry: r.geometry }),
  };
}
