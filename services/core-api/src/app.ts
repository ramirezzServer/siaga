import { ConsoleLogger, Logger, Module, type DynamicModule, type LogLevel } from "@nestjs/common";
import { NestFactory } from "@nestjs/core";
import { FastifyAdapter, type NestFastifyApplication } from "@nestjs/platform-fastify";

import { HazardsService, HAZARDS_SERVICE } from "./hazards/app/hazards.service.js";
import { HazardsController } from "./hazards/http/hazards.controller.js";
import type { HazardReader } from "./hazards/ports/hazard-reader.js";
import {
  HealthController,
  READINESS_PROBE,
  VERSION,
  type ReadinessProbe,
} from "./health/health.controller.js";
import { RegionsService, REGIONS_SERVICE } from "./regions/app/regions.service.js";
import { RegionsController } from "./regions/http/regions.controller.js";
import type { RegionReader } from "./regions/ports/region-reader.js";
import type { Clock } from "./shared/domain/clock.js";
import { ProblemFilter } from "./shared/http/problem.js";
import { SourcesService, SOURCES_SERVICE } from "./sources/app/sources.service.js";
import { SourcesController } from "./sources/http/sources.controller.js";
import type { SourceReader } from "./sources/ports/source-reader.js";

/** Semua dependensi luar core-api; main.ts merakit versi asli, test merakit versi palsu. */
export interface AppDeps {
  readonly hazards: HazardReader;
  readonly regions: RegionReader;
  readonly sources: SourceReader;
  readonly readiness: ReadinessProbe;
  readonly clock: Clock;
  readonly version: string;
  readonly corsOrigins: readonly string[];
  readonly logLevels?: LogLevel[];
  /** JSON satu baris per log (container); false untuk teks berwarna (make core). */
  readonly logJson?: boolean;
}

@Module({})
class AppModule {
  static register(deps: AppDeps): DynamicModule {
    return {
      module: AppModule,
      controllers: [HealthController, RegionsController, HazardsController, SourcesController],
      providers: [
        { provide: HAZARDS_SERVICE, useValue: new HazardsService(deps.hazards, deps.clock) },
        { provide: REGIONS_SERVICE, useValue: new RegionsService(deps.regions) },
        { provide: SOURCES_SERVICE, useValue: new SourcesService(deps.sources, deps.clock) },
        { provide: READINESS_PROBE, useValue: deps.readiness },
        { provide: VERSION, useValue: deps.version },
      ],
    };
  }
}

const LEVELS: LogLevel[] = ["fatal", "error", "warn", "log", "debug", "verbose"];

/** Membuat aplikasi Nest + Fastify yang siap listen (atau inject di test). */
export async function createApp(deps: AppDeps): Promise<NestFastifyApplication> {
  const adapter = new FastifyAdapter({
    // Di belakang Traefik/Cloudflare: IP klien dari X-Forwarded-For.
    trustProxy: true,
    // Rute tanpa garis miring akhir; /v1/hazards/ dianggap sama dengan /v1/hazards.
    routerOptions: { ignoreTrailingSlash: true, maxParamLength: 100 },
    // Batas ukuran body kecil: API ini hanya GET.
    bodyLimit: 16 * 1024,
  });
  const app = await NestFactory.create<NestFastifyApplication>(AppModule.register(deps), adapter, {
    logger: new ConsoleLogger({
      json: deps.logJson ?? false,
      logLevels: deps.logLevels ?? LEVELS.slice(0, 4),
    }),
  });
  app.useGlobalFilters(new ProblemFilter(new Logger("http")));
  if (deps.corsOrigins.length > 0) {
    app.enableCors({
      origin: [...deps.corsOrigins],
      methods: ["GET", "HEAD", "OPTIONS"],
      maxAge: 3600,
    });
  }
  app.enableShutdownHooks();
  await app.init();
  return app;
}
