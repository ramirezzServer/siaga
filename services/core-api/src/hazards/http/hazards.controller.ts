import { Controller, Get, Header, Inject, Param, Query } from "@nestjs/common";
import { z } from "zod";

import {
  parseInput,
  queryArray,
  queryBool,
  queryDateTime,
  queryEnum,
  queryInt,
  queryString,
} from "../../shared/http/query.js";
import { HAZARDS_SERVICE, type HazardsService } from "../app/hazards.service.js";
import type { FeedQuery } from "../domain/feed.js";
import { HAZARD_KINDS, type AlertLevel } from "../domain/hazard.js";

const levelParam = queryInt(1, 4).transform((n) => n as AlertLevel);

const listQuery = z.object({
  kind: queryArray(z.enum(HAZARD_KINDS), 5).optional(),
  status: queryEnum(["active", "all"]).default("active"),
  at: queryDateTime.optional(),
  since: queryDateTime.optional(),
  until: queryDateTime.optional(),
  min_level: levelParam.optional(),
  bbox: queryString(100).optional(),
  drill: queryBool.default(false),
  limit: queryInt(1, 200).default(50),
  cursor: queryString(200).optional(),
});

const idParam = z.object({ id: z.uuid({ message: "harus UUID" }) });

const detailQuery = z.object({
  include: queryEnum(["area"]).optional(),
});

const regionsQuery = z.object({
  limit: queryInt(1, 500).default(100),
  cursor: queryString(200).optional(),
});

/** Endpoint publik kejadian bahaya. Cache singkat: data berubah tiap menit, Cloudflare menyerap lonjakan. */
@Controller("v1/hazards")
export class HazardsController {
  constructor(@Inject(HAZARDS_SERVICE) private readonly hazards: HazardsService) {}

  @Get()
  @Header("cache-control", "public, max-age=15")
  list(@Query() query: unknown) {
    const q = parseInput(listQuery, query);
    const feed: FeedQuery = {
      kinds: q.kind ?? [],
      status: q.status,
      drill: q.drill,
      limit: q.limit,
      ...(q.at && { at: q.at }),
      ...(q.since && { since: q.since }),
      ...(q.until && { until: q.until }),
      ...(q.min_level !== undefined && { minLevel: q.min_level }),
      ...(q.bbox !== undefined && { bbox: q.bbox }),
      ...(q.cursor !== undefined && { cursor: q.cursor }),
    };
    return this.hazards.list(feed);
  }

  @Get(":id")
  @Header("cache-control", "public, max-age=15")
  get(@Param() params: unknown, @Query() query: unknown) {
    const { id } = parseInput(idParam, params);
    const { include } = parseInput(detailQuery, query);
    return this.hazards.get(id, include === "area");
  }

  @Get(":id/regions")
  @Header("cache-control", "public, max-age=60")
  regions(@Param() params: unknown, @Query() query: unknown) {
    const { id } = parseInput(idParam, params);
    const q = parseInput(regionsQuery, query);
    return this.hazards.impactedRegions(id, q.limit, q.cursor);
  }
}
