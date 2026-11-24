import type pg from "pg";
import { z } from "zod";

import { parseRows } from "../../shared/adapters/postgres.js";
import { parseSubject, type SourceRecord } from "../domain/source.js";
import type { SourceReader } from "../ports/source-reader.js";

export const LIST_SQL = `
SELECT connector, subject, extract(epoch FROM poll_interval)::int AS interval_seconds,
  last_attempt_at, last_success_at, last_change_at, failing_since, consecutive_failures
FROM ts.source_status
ORDER BY connector`;

const row = z.object({
  connector: z.string().regex(/^[a-z0-9]+([.-][a-z0-9]+)*$/),
  subject: z.string().transform((s, ctx) => {
    const parsed = parseSubject(s);
    if (!parsed) {
      ctx.addIssue({ code: "custom", message: `subjek raw tidak dikenal: ${s}` });
      return z.NEVER;
    }
    return parsed;
  }),
  interval_seconds: z.number().int().positive(),
  last_attempt_at: z.date(),
  last_success_at: z.date().nullable(),
  last_change_at: z.date().nullable(),
  failing_since: z.date().nullable(),
  consecutive_failures: z.number().int().nonnegative(),
});

function toRecord(r: z.infer<typeof row>): SourceRecord {
  return {
    connector: r.connector,
    provider: r.subject.provider,
    dataset: r.subject.dataset,
    intervalSeconds: r.interval_seconds,
    lastAttemptAt: r.last_attempt_at,
    lastSuccessAt: r.last_success_at,
    lastChangeAt: r.last_change_at,
    failingSince: r.failing_since,
    consecutiveFailures: r.consecutive_failures,
  };
}

/** Adapter PostgreSQL untuk SourceReader. */
export class PgSourceReader implements SourceReader {
  constructor(private readonly pool: pg.Pool) {}

  async list(): Promise<SourceRecord[]> {
    const res = await this.pool.query(LIST_SQL);
    return parseRows("ts.source_status", row, res.rows).map(toRecord);
  }
}
