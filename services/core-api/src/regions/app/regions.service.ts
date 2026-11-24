import type { components } from "@siaga/api-client";

import { NotFoundError } from "../../shared/domain/errors.js";
import {
  buildRegionFilter,
  regionPage,
  toRegion,
  type RegionDto,
  type RegionQuery,
} from "../domain/region.js";
import type { RegionReader } from "../ports/region-reader.js";

type RegionPage = components["schemas"]["RegionPage"];

/** Use case baca wilayah administrasi. */
export class RegionsService {
  constructor(private readonly reader: RegionReader) {}

  async list(query: RegionQuery): Promise<RegionPage> {
    const filter = buildRegionFilter(query);
    const rows = await this.reader.list(filter);
    return regionPage(rows, filter, query.limit);
  }

  async get(code: string, includeGeometry: boolean): Promise<RegionDto> {
    const r = await this.reader.get(code, includeGeometry);
    if (!r) {
      throw new NotFoundError(`wilayah ${code}`);
    }
    return toRegion(r);
  }
}

export const REGIONS_SERVICE = Symbol("RegionsService");
