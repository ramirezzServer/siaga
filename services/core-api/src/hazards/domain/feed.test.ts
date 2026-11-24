import { describe, expect, it } from "vitest";

import { hazard } from "../../../test/support/records.js";
import { InvalidParameterError } from "../../shared/domain/errors.js";
import {
  buildFeedFilter,
  decodePosition,
  encodePosition,
  MAX_REPLAY_DAYS,
  pageOf,
  parseBBox,
  type FeedQuery,
} from "./feed.js";

const now = new Date("2026-11-24T03:00:00Z");
const base: FeedQuery = { kinds: [], status: "active", drill: false, limit: 50 };

function issues(fn: () => unknown): string[] {
  try {
    fn();
  } catch (err) {
    if (err instanceof InvalidParameterError) return err.issues.map((i) => i.parameter);
    throw err;
  }
  return [];
}

describe("parseBBox", () => {
  it("mengurai empat angka", () => {
    expect(parseBBox("105.9,-7.9,108.9,-5.8")).toEqual([105.9, -7.9, 108.9, -5.8]);
  });

  it.each(["1,2,3", "a,b,c,d", "1,2,3,4,5", "-181,0,10,10", "0,-91,10,10", "10,0,5,5", "0,5,10,5"])(
    "menolak %s",
    (text) => {
      expect(issues(() => parseBBox(text))).toEqual(["bbox"]);
    },
  );
});

describe("buildFeedFilter", () => {
  it("bawaan: aktif pada jam server, semua jenis, satu baris ekstra", () => {
    expect(buildFeedFilter(base, now)).toEqual({
      mode: "active",
      now,
      kinds: [],
      drill: false,
      fetch: 51,
    });
  });

  it("status=all tanpa jam", () => {
    expect(buildFeedFilter({ ...base, status: "all" }, now).mode).toBe("all");
  });

  it("at mengalahkan status dan membawa waktunya", () => {
    const at = new Date("2026-11-20T00:00:00Z");
    const f = buildFeedFilter({ ...base, status: "active", at }, now);
    expect(f).toMatchObject({ mode: "at", at });
  });

  it("menyalin saringan opsional dan membuang jenis ganda", () => {
    const since = new Date("2026-11-01T00:00:00Z");
    const until = new Date("2026-11-02T00:00:00Z");
    const cursor = encodePosition({
      occurredAt: since,
      id: "0b6c8a5e-3d1f-5a2b-9c4d-7e8f9a0b1c2d",
    });
    const f = buildFeedFilter(
      {
        ...base,
        kinds: ["quake", "quake", "weather"],
        since,
        until,
        minLevel: 3,
        bbox: "105,-8,109,-5",
        drill: true,
        cursor,
      },
      now,
    );
    expect(f).toMatchObject({
      kinds: ["quake", "weather"],
      since,
      until,
      minLevel: 3,
      bbox: [105, -8, 109, -5],
      drill: true,
      after: { occurredAt: since, id: "0b6c8a5e-3d1f-5a2b-9c4d-7e8f9a0b1c2d" },
    });
  });

  it("mengumpulkan semua masalah sekaligus", () => {
    const at = new Date(now.getTime() - (MAX_REPLAY_DAYS + 1) * 86_400_000);
    expect(
      issues(() => buildFeedFilter({ ...base, since: now, until: now, at, bbox: "1,2" }, now)),
    ).toEqual(["until", "at", "bbox"]);
  });

  it("menolak at di masa depan, tetapi menerima beda jam kecil", () => {
    expect(
      issues(() => buildFeedFilter({ ...base, at: new Date(now.getTime() + 3_600_000) }, now)),
    ).toEqual(["at"]);
    expect(buildFeedFilter({ ...base, at: new Date(now.getTime() + 60_000) }, now).mode).toBe("at");
  });

  it("menolak kursor rusak", () => {
    expect(issues(() => buildFeedFilter({ ...base, cursor: "rusak" }, now))).toEqual(["cursor"]);
  });

  it("melempar ulang galat bukan validasi", () => {
    // bbox dengan getter yang melempar: jalur rethrow untuk galat tak terduga.
    const q = { ...base } as FeedQuery & { bbox: string };
    Object.defineProperty(q, "bbox", {
      get() {
        throw new TypeError("rusak");
      },
      enumerable: true,
    });
    expect(() => buildFeedFilter(q, now)).toThrow(TypeError);
  });
});

describe("pageOf", () => {
  const a = hazard({
    id: "00000000-0000-4000-8000-000000000003",
    occurredAt: new Date("2026-11-24T03:00:00Z"),
  });
  const b = hazard({
    id: "00000000-0000-4000-8000-000000000002",
    occurredAt: new Date("2026-11-24T02:00:00Z"),
  });
  const c = hazard({
    id: "00000000-0000-4000-8000-000000000001",
    occurredAt: new Date("2026-11-24T01:00:00Z"),
  });

  it("kursor menunjuk baris terakhir halaman bila ada baris ekstra", () => {
    const { items, nextCursor } = pageOf([a, b, c], 2);
    expect(items).toEqual([a, b]);
    expect(nextCursor && decodePosition(nextCursor)).toEqual({
      occurredAt: b.occurredAt,
      id: b.id,
    });
  });

  it("tanpa baris ekstra tidak ada halaman berikutnya", () => {
    expect(pageOf([a, b], 2).nextCursor).toBeNull();
    expect(pageOf([], 2)).toEqual({ items: [], nextCursor: null });
  });
});
