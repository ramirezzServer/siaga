import { z } from "zod";

import { InvalidParameterError } from "./errors.js";

/**
 * Kursor opak: JSON berversi di dalam base64url. Isinya bukan kontrak; klien
 * hanya mengembalikannya apa adanya. Kursor dari versi lain ditolak 400.
 */
const VERSION = 1;

export function encodeCursor(payload: Record<string, unknown>): string {
  return Buffer.from(JSON.stringify({ v: VERSION, ...payload }), "utf8").toString("base64url");
}

export function decodeCursor<T extends z.ZodType>(cursor: string, schema: T): z.infer<T> {
  const invalid = InvalidParameterError.of("cursor", "kursor tidak valid atau kedaluwarsa");
  if (!/^[A-Za-z0-9_-]{1,200}$/.test(cursor)) {
    throw invalid;
  }
  let raw: unknown;
  try {
    raw = JSON.parse(Buffer.from(cursor, "base64url").toString("utf8"));
  } catch {
    throw invalid;
  }
  if (typeof raw !== "object" || raw === null || (raw as { v?: unknown }).v !== VERSION) {
    throw invalid;
  }
  const parsed = schema.safeParse(raw);
  if (!parsed.success) {
    throw invalid;
  }
  return parsed.data;
}

/** Kursor offset untuk daftar pendek yang urutannya tidak cocok untuk keyset. */
const offsetSchema = z.object({ o: z.number().int().min(0).max(1_000_000) });

export function encodeOffset(offset: number): string {
  return encodeCursor({ o: offset });
}

export function decodeOffset(cursor: string | undefined): number {
  return cursor === undefined ? 0 : decodeCursor(cursor, offsetSchema).o;
}
