import "reflect-metadata";

import { Logger, type LogLevel } from "@nestjs/common";

import { createApp } from "./app.js";
import { loadConfig } from "./config.js";
import { PgHazardReader } from "./hazards/adapters/pg-hazard-reader.js";
import { PgRegionReader } from "./regions/adapters/pg-region-reader.js";
import { createPool, PgReadinessProbe } from "./shared/adapters/postgres.js";
import { systemClock } from "./shared/domain/clock.js";

const ORDER: LogLevel[] = ["fatal", "error", "warn", "log", "debug"];

async function main(): Promise<void> {
  const config = loadConfig(process.env);
  const pool = createPool({
    url: config.DATABASE_URL,
    max: config.DB_POOL_MAX,
    statementTimeoutMs: config.DB_STATEMENT_TIMEOUT_MS,
  });
  const logger = new Logger("core-api");
  // Koneksi idle yang putus (misal PostgreSQL restart) tidak boleh mematikan proses.
  pool.on("error", (err) => {
    logger.warn(`koneksi pool putus: ${err.message}`);
  });
  const app = await createApp({
    hazards: new PgHazardReader(pool),
    regions: new PgRegionReader(pool),
    readiness: new PgReadinessProbe(pool),
    clock: systemClock,
    version: config.SIAGA_VERSION,
    corsOrigins: config.CORS_ORIGINS,
    logLevels: ORDER.slice(0, ORDER.indexOf(config.LOG_LEVEL) + 1),
    logJson: config.LOG_FORMAT === "json",
  });
  app
    .getHttpAdapter()
    .getInstance()
    .addHook("onClose", async () => {
      await pool.end();
    });
  await app.listen({ port: config.PORT, host: config.HOST });
  logger.log(`siap di ${config.HOST}:${String(config.PORT)} (versi ${config.SIAGA_VERSION})`);
}

main().catch((err: unknown) => {
  console.error(err);
  process.exit(1);
});
