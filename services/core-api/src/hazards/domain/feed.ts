import { z } from "zod";

import { decodeCursor, encodeCursor } from "../../shared/domain/cursor.js";
import { InvalidParameterError, type ParameterIssue } from "../../shared/domain/errors.js";
import type { BBox } from "../../shared/domain/geo.js";
import type { AlertLevel, HazardKind, HazardRecord } from "./hazard.js";

/** Putar ulang (`at`) paling jauh 400 hari: retensi deret waktu 1 tahun ditambah jeda. */
export const MAX_REPLAY_DAYS = 400;
/** `at` boleh sedikit di depan jam server (beda jam klien). */
export const MAX_FUTURE_SKEW_MS = 5 * 60 * 1000;

/** Parameter feed setelah lolos validasi bentuk di lapisan HTTP. */
export interface FeedQuery {
  readonly kinds: readonly HazardKind[];
  readonly status: "active" | "all";
  readonly at?: Date;
  readonly since?: Date;
  readonly until?: Date;
  readonly minLevel?: AlertLevel;
  readonly bbox?: string;
  readonly drill: boolean;
  readonly limit: number;
  readonly cursor?: string;
}

/** Posisi keyset: kejadian terakhir di halaman sebelumnya. */
export interface FeedPosition {
  readonly occurredAt: Date;
  readonly id: string;
}

/** Saringan yang dikirim ke port pembaca. */
export type FeedFilter =
  | (FeedFilterBase & { readonly mode: "active"; readonly now: Date })
  | (FeedFilterBase & { readonly mode: "all" })
  | (FeedFilterBase & { readonly mode: "at"; readonly at: Date });

interface FeedFilterBase {
  /** Kosong berarti semua jenis. */
  readonly kinds: readonly HazardKind[];
  readonly since?: Date;
  readonly until?: Date;
  readonly minLevel?: AlertLevel;
  readonly bbox?: BBox;
  readonly drill: boolean;
  /** Satu lebih banyak dari ukuran halaman, untuk tahu ada halaman berikutnya. */
  readonly fetch: number;
  readonly after?: FeedPosition;
}

/** Mengurai `min_lon,min_lat,max_lon,max_lat` dan memeriksa rentangnya. */
export function parseBBox(text: string): BBox {
  const parts = text.split(",").map(Number);
  const [minLon, minLat, maxLon, maxLat] = parts;
  if (
    parts.length !== 4 ||
    minLon === undefined ||
    minLat === undefined ||
    maxLon === undefined ||
    maxLat === undefined ||
    parts.some((n) => !Number.isFinite(n))
  ) {
    throw InvalidParameterError.of("bbox", "harus empat angka: min_lon,min_lat,max_lon,max_lat");
  }
  if (minLon < -180 || maxLon > 180 || minLat < -90 || maxLat > 90) {
    throw InvalidParameterError.of("bbox", "di luar rentang WGS84");
  }
  if (minLon >= maxLon || minLat >= maxLat) {
    throw InvalidParameterError.of("bbox", "nilai minimum harus lebih kecil dari maksimum");
  }
  return [minLon, minLat, maxLon, maxLat];
}

const positionSchema = z.object({
  t: z.iso.datetime({ offset: true }),
  id: z.uuid(),
});

export function encodePosition(p: FeedPosition): string {
  return encodeCursor({ t: p.occurredAt.toISOString(), id: p.id });
}

export function decodePosition(cursor: string): FeedPosition {
  const p = decodeCursor(cursor, positionSchema);
  return { occurredAt: new Date(p.t), id: p.id };
}

/** Menyusun saringan feed dan memeriksa aturan antar-parameter. */
export function buildFeedFilter(q: FeedQuery, now: Date): FeedFilter {
  const issues: ParameterIssue[] = [];
  if (q.since && q.until && q.since >= q.until) {
    issues.push({ parameter: "until", message: "harus setelah since" });
  }
  if (q.at) {
    const ageDays = (now.getTime() - q.at.getTime()) / 86_400_000;
    if (ageDays > MAX_REPLAY_DAYS) {
      issues.push({
        parameter: "at",
        message: `paling jauh ${String(MAX_REPLAY_DAYS)} hari ke belakang`,
      });
    } else if (q.at.getTime() - now.getTime() > MAX_FUTURE_SKEW_MS) {
      issues.push({ parameter: "at", message: "tidak boleh di masa depan" });
    }
  }
  let bbox: BBox | undefined;
  if (q.bbox !== undefined) {
    try {
      bbox = parseBBox(q.bbox);
    } catch (err) {
      if (!(err instanceof InvalidParameterError)) throw err;
      issues.push(...err.issues);
    }
  }
  if (issues.length > 0) {
    throw new InvalidParameterError(issues);
  }
  const base: FeedFilterBase = {
    kinds: [...new Set(q.kinds)],
    drill: q.drill,
    fetch: q.limit + 1,
    ...(q.since && { since: q.since }),
    ...(q.until && { until: q.until }),
    ...(q.minLevel !== undefined && { minLevel: q.minLevel }),
    ...(bbox && { bbox }),
    ...(q.cursor !== undefined && { after: decodePosition(q.cursor) }),
  };
  if (q.at) {
    return { ...base, mode: "at", at: q.at };
  }
  return q.status === "active" ? { ...base, mode: "active", now } : { ...base, mode: "all" };
}

/** Memotong hasil pembaca menjadi satu halaman dan kursor berikutnya. */
export function pageOf(
  rows: readonly HazardRecord[],
  limit: number,
): { items: HazardRecord[]; nextCursor: string | null } {
  const items = rows.slice(0, limit);
  const last = items.at(-1);
  const nextCursor =
    rows.length > limit && last
      ? encodePosition({ occurredAt: last.occurredAt, id: last.id })
      : null;
  return { items, nextCursor };
}
