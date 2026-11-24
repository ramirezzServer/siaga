import { Controller, Get, Header, Inject } from "@nestjs/common";

import { SOURCES_SERVICE, type SourcesService } from "../app/sources.service.js";

/** Endpoint publik status sumber data. Cache singkat, sama dengan feed kejadian. */
@Controller("v1/sources")
export class SourcesController {
  constructor(@Inject(SOURCES_SERVICE) private readonly sources: SourcesService) {}

  @Get()
  @Header("cache-control", "public, max-age=15")
  list() {
    return this.sources.list();
  }
}
