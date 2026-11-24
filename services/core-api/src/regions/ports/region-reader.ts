import type { RegionFilter, RegionRecord } from "../domain/region.js";

/** Port baca wilayah (ref.region milik geo-processor, hanya SELECT). */
export interface RegionReader {
  list(filter: RegionFilter): Promise<RegionRecord[]>;
  get(code: string, includeGeometry: boolean): Promise<RegionRecord | null>;
}

export const REGION_READER = Symbol("RegionReader");
