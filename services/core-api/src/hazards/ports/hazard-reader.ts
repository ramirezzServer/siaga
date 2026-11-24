import type { FeedFilter } from "../domain/feed.js";
import type { HazardDetailRecord, HazardRecord, ImpactedRegionRecord } from "../domain/hazard.js";

/** Port baca kejadian bahaya (skema hazard milik geo-processor, hanya SELECT). */
export interface HazardReader {
  /** Kejadian sesuai saringan, `occurred_at` lalu `id` menurun, paling banyak `filter.fetch`. */
  list(filter: FeedFilter): Promise<HazardRecord[]>;
  /** Detail satu kejadian, atau null bila tidak ada. */
  get(id: string, includeArea: boolean): Promise<HazardDetailRecord | null>;
  /** Jenis kejadian, atau null bila tidak ada. */
  kindOf(id: string): Promise<HazardRecord["kind"] | null>;
  /** Wilayah terdampak: jarak lalu cakupan terluas lalu kode. */
  impactedRegions(
    id: string,
    offset: number,
    limit: number,
  ): Promise<{ total: number; rows: ImpactedRegionRecord[] }>;
}

export const HAZARD_READER = Symbol("HazardReader");
