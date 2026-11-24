import { Controller, Get, Header, Inject, Param, Query } from "@nestjs/common";
import { z } from "zod";

import { parseInput, queryEnum, queryInt, queryString } from "../../shared/http/query.js";
import { REGIONS_SERVICE, type RegionsService } from "../app/regions.service.js";
import { REGION_CODE_PATTERN, type RegionQuery } from "../domain/region.js";

const code = z
  .string()
  .regex(REGION_CODE_PATTERN, "harus kode wilayah Kemendagri bertitik, misal 32.73");

const listQuery = z.object({
  parent: queryString(20).pipe(code).optional(),
  level: queryInt(1, 4)
    .transform((n) => n as 1 | 2 | 3 | 4)
    .optional(),
  q: queryString(100).pipe(z.string().trim().min(3, "minimal 3 karakter")).optional(),
  limit: queryInt(1, 200).default(50),
  cursor: queryString(200).optional(),
});

const codeParam = z.object({ code });
const detailQuery = z.object({
  include: queryEnum(["geometry"]).optional(),
});

/** Endpoint publik wilayah. Batas wilayah jarang berubah, jadi cache panjang. */
@Controller("v1/regions")
export class RegionsController {
  constructor(@Inject(REGIONS_SERVICE) private readonly regions: RegionsService) {}

  @Get()
  @Header("cache-control", "public, max-age=3600")
  list(@Query() query: unknown) {
    const q = parseInput(listQuery, query);
    const rq: RegionQuery = {
      limit: q.limit,
      ...(q.parent !== undefined && { parent: q.parent }),
      ...(q.level !== undefined && { level: q.level }),
      ...(q.q !== undefined && { q: q.q }),
      ...(q.cursor !== undefined && { cursor: q.cursor }),
    };
    return this.regions.list(rq);
  }

  @Get(":code")
  @Header("cache-control", "public, max-age=86400")
  get(@Param() params: unknown, @Query() query: unknown) {
    const { code } = parseInput(codeParam, params);
    const { include } = parseInput(detailQuery, query);
    return this.regions.get(code, include === "geometry");
  }
}
