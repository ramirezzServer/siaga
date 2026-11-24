import type { components } from "@siaga/api-client";

import type { Clock } from "../../shared/domain/clock.js";
import { decodeOffset, encodeOffset } from "../../shared/domain/cursor.js";
import { NotFoundError } from "../../shared/domain/errors.js";
import { buildFeedFilter, pageOf, type FeedQuery } from "../domain/feed.js";
import { impactBasis } from "../domain/hazard.js";
import {
  toHazardDetail,
  toHazardSummary,
  toImpactedRegion,
  type HazardDetailDto,
} from "../domain/present.js";
import type { HazardReader } from "../ports/hazard-reader.js";

type HazardPage = components["schemas"]["HazardPage"];
type ImpactedRegionPage = components["schemas"]["ImpactedRegionPage"];

/** Use case baca kejadian bahaya untuk peta publik. */
export class HazardsService {
  constructor(
    private readonly reader: HazardReader,
    private readonly clock: Clock,
  ) {}

  async list(query: FeedQuery): Promise<HazardPage> {
    const now = this.clock.now();
    const filter = buildFeedFilter(query, now);
    const rows = await this.reader.list(filter);
    const { items, nextCursor } = pageOf(rows, query.limit);
    return {
      items: items.map(toHazardSummary),
      next_cursor: nextCursor,
      generated_at: now.toISOString(),
    };
  }

  async get(id: string, includeArea: boolean): Promise<HazardDetailDto> {
    const detail = await this.reader.get(id, includeArea);
    if (!detail) {
      throw new NotFoundError(`kejadian ${id}`);
    }
    return toHazardDetail(detail);
  }

  async impactedRegions(id: string, limit: number, cursor?: string): Promise<ImpactedRegionPage> {
    const offset = decodeOffset(cursor);
    const kind = await this.reader.kindOf(id);
    if (!kind) {
      throw new NotFoundError(`kejadian ${id}`);
    }
    const { total, rows } = await this.reader.impactedRegions(id, offset, limit);
    const next = offset + rows.length;
    return {
      basis: impactBasis(kind),
      total,
      items: rows.map(toImpactedRegion),
      next_cursor: rows.length === limit && next < total ? encodeOffset(next) : null,
    };
  }
}

export const HAZARDS_SERVICE = Symbol("HazardsService");
