import pg from "pg";
import type { z } from "zod";

import { UnavailableError } from "../domain/errors.js";

export interface PoolOptions {
  readonly url: string;
  readonly max: number;
  readonly statementTimeoutMs: number;
}

/**
 * Pool PostgreSQL untuk role siaga_core. Setiap sesi dibuat read-only dan
 * dibatasi waktunya; ini lapis kedua setelah grant (siaga_core hanya punya
 * SELECT di skema hazard, ref, ts), sesuai aturan invarian dijaga dua kali.
 */
export function createPool(o: PoolOptions): pg.Pool {
  return new pg.Pool({
    connectionString: o.url,
    max: o.max,
    application_name: "core-api",
    connectionTimeoutMillis: 5_000,
    idleTimeoutMillis: 30_000,
    options: `-c default_transaction_read_only=on -c statement_timeout=${String(o.statementTimeoutMs)} -c TimeZone=UTC`,
  });
}

/** Klien yang bisa menjalankan query: pool atau koneksi di dalam transaksi. */
export type Queryable = Pick<pg.Pool, "query">;

/** Menjalankan fn di satu snapshot (REPEATABLE READ READ ONLY), untuk detail yang butuh beberapa query. */
export async function withSnapshot<T>(pool: pg.Pool, fn: (q: Queryable) => Promise<T>): Promise<T> {
  const client = await pool.connect();
  try {
    await client.query("BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY");
    const result = await fn(client);
    await client.query("COMMIT");
    return result;
  } catch (err) {
    await client.query("ROLLBACK").catch(() => undefined);
    throw err;
  } finally {
    client.release();
  }
}

/** Galat bentuk baris: skema database berubah tanpa adapter ikut diubah. */
export class RowShapeError extends Error {
  constructor(query: string, issues: string) {
    super(`baris ${query} tidak sesuai skema yang diharapkan: ${issues}`);
    this.name = "RowShapeError";
  }
}

/** Memvalidasi baris hasil query dengan Zod (batas masuk dari database). */
export function parseRows<T extends z.ZodType>(
  query: string,
  schema: T,
  rows: unknown[],
): z.infer<T>[] {
  return rows.map((row) => {
    const parsed = schema.safeParse(row);
    if (!parsed.success) {
      throw new RowShapeError(
        query,
        parsed.error.issues.map((i) => `${i.path.join(".")}: ${i.message}`).join("; "),
      );
    }
    return parsed.data;
  });
}

/** Galat yang berarti database sedang tidak bisa dipakai (HTTP 503, bukan 500). */
export function isUnavailable(err: unknown): boolean {
  if (typeof err !== "object" || err === null) return false;
  const code = (err as { code?: unknown }).code;
  if (typeof code !== "string") {
    return (
      err instanceof Error &&
      /timeout exceeded when trying to connect|Connection terminated/i.test(err.message)
    );
  }
  // ECONNREFUSED dan kawan-kawan dari Node; 57014 statement_timeout; 57P01–57P03
  // server dimatikan atau belum siap; 53300 koneksi penuh; 08xxx galat koneksi.
  return /^(E[A-Z]+|57014|57P0[1-3]|53300|08[0-9A-Z]{3})$/.test(code);
}

/** Kesiapan: satu query ringan ke database. */
export class PgReadinessProbe {
  constructor(private readonly pool: pg.Pool) {}

  async check(): Promise<void> {
    try {
      await this.pool.query("SELECT 1");
    } catch (err) {
      throw new UnavailableError("database", { cause: err });
    }
  }
}
