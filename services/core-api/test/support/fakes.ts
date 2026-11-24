import type { FeedFilter } from "../../src/hazards/domain/feed.js";
import type {
  HazardDetailRecord,
  HazardRecord,
  ImpactedRegionRecord,
} from "../../src/hazards/domain/hazard.js";
import type { HazardReader } from "../../src/hazards/ports/hazard-reader.js";
import type { RegionFilter, RegionRecord } from "../../src/regions/domain/region.js";
import type { RegionReader } from "../../src/regions/ports/region-reader.js";

/** Pembaca kejadian palsu: mencatat saringan dan mengembalikan data yang disiapkan. */
export class FakeHazardReader implements HazardReader {
  filters: FeedFilter[] = [];
  listRows: HazardRecord[] = [];
  details = new Map<string, HazardDetailRecord>();
  impacts = new Map<string, ImpactedRegionRecord[]>();
  failWith: Error | null = null;

  list(filter: FeedFilter): Promise<HazardRecord[]> {
    this.filters.push(filter);
    if (this.failWith) return Promise.reject(this.failWith);
    return Promise.resolve(this.listRows.slice(0, filter.fetch));
  }

  get(id: string, includeArea: boolean): Promise<HazardDetailRecord | null> {
    const d = this.details.get(id);
    if (!d) return Promise.resolve(null);
    return Promise.resolve(includeArea ? d : { ...d, hazard: { ...d.hazard, area: null } });
  }

  kindOf(id: string): Promise<HazardRecord["kind"] | null> {
    return Promise.resolve(this.details.get(id)?.hazard.kind ?? null);
  }

  impactedRegions(id: string, offset: number, limit: number) {
    const all = this.impacts.get(id) ?? [];
    return Promise.resolve({ total: all.length, rows: all.slice(offset, offset + limit) });
  }
}

export class FakeRegionReader implements RegionReader {
  filters: RegionFilter[] = [];
  rows: RegionRecord[] = [];

  list(filter: RegionFilter): Promise<RegionRecord[]> {
    this.filters.push(filter);
    const after = filter.after;
    const rows = after === undefined ? this.rows : this.rows.filter((r) => r.code > after);
    return Promise.resolve(rows.slice(0, filter.fetch));
  }

  get(code: string, includeGeometry: boolean): Promise<RegionRecord | null> {
    const r = this.rows.find((x) => x.code === code);
    if (!r) return Promise.resolve(null);
    return Promise.resolve(includeGeometry ? r : { ...r, geometry: null });
  }
}
