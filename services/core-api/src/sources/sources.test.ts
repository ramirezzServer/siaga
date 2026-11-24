import { describe, expect, it } from "vitest";

import { expectSchema } from "../../test/support/contract.js";
import { FakeSourceReader } from "../../test/support/fakes.js";
import { source, T0 } from "../../test/support/records.js";
import { fixedClock } from "../shared/domain/clock.js";
import { SourcesService } from "./app/sources.service.js";
import {
  DEGRADED_FAILURES,
  lateAfter,
  parseSubject,
  SOURCE_DATASETS,
  SOURCE_PROVIDERS,
  sourceList,
  sourceState,
  toSourceStatus,
} from "./domain/source.js";

const at = (ms: number) => new Date(T0.getTime() + ms);
const MIN = 60_000;

describe("parseSubject", () => {
  it("semua pasangan jenis–penyedia yang dikenal", () => {
    for (const dataset of SOURCE_DATASETS) {
      for (const provider of SOURCE_PROVIDERS) {
        expect(parseSubject(`raw.${dataset}.${provider}`)).toEqual({ dataset, provider });
      }
    }
  });

  it.each([
    "raw.quake.emsc",
    "hazard.quake.created",
    "raw.quake",
    "raw.quake.bmkg.x",
    "RAW.quake.bmkg",
  ])("%s ditolak", (subject) => {
    expect(parseSubject(subject)).toBeNull();
  });
});

describe("sourceState", () => {
  // Berhasil di T0, interval 30 dtk: terlambat setelah T0 + 2×30 dtk + 10 menit.
  const ok = source({ lastAttemptAt: T0, lastSuccessAt: T0 });
  const limit = 60_000 + 10 * MIN;

  it("batas terlambat = berhasil terakhir + 2 × interval + 10 menit", () => {
    expect(lateAfter(ok)).toEqual(at(limit));
    expect(sourceState(ok, at(limit))).toBe("ok");
    expect(sourceState(ok, at(limit + 1))).toBe("late");
  });

  it("sapuan 6 jam boleh terlewat satu kali", () => {
    const sweep = source({ intervalSeconds: 6 * 3600, lastAttemptAt: T0, lastSuccessAt: T0 });
    expect(sourceState(sweep, at(12 * 3600_000))).toBe("ok");
    expect(sourceState(sweep, at(12 * 3600_000 + 10 * MIN + 1))).toBe("late");
  });

  it("gagal beruntun sebelum batas terlambat menjadi degraded", () => {
    const failing = (n: number) =>
      source({
        lastAttemptAt: at(3 * MIN),
        lastSuccessAt: T0,
        failingSince: at(MIN),
        consecutiveFailures: n,
      });
    expect(sourceState(failing(DEGRADED_FAILURES - 1), at(4 * MIN))).toBe("ok");
    expect(sourceState(failing(DEGRADED_FAILURES), at(4 * MIN))).toBe("degraded");
    expect(sourceState(failing(DEGRADED_FAILURES), at(limit + 1))).toBe("late");
  });

  it("belum pernah berhasil selalu late", () => {
    const never = source({
      lastSuccessAt: null,
      lastChangeAt: null,
      failingSince: T0,
      consecutiveFailures: 1,
    });
    expect(lateAfter(never)).toBeNull();
    expect(sourceState(never, T0)).toBe("late");
  });
});

describe("toSourceStatus dan daftar", () => {
  it("DTO sesuai kontrak, jenis bahaya per jenis data", () => {
    const now = T0;
    const dto = toSourceStatus(source(), now);
    expectSchema("SourceStatus", dto);
    expect(dto).toMatchObject({
      id: "bmkg-autogempa",
      provider: "bmkg",
      dataset: "quake",
      hazard_kinds: ["quake"],
      state: "ok",
      interval_seconds: 30,
      failing_since: null,
    });
    expect(
      toSourceStatus(source({ dataset: "rain", provider: "openmeteo" }), now).hazard_kinds,
    ).toEqual(["flood"]);
    expect(toSourceStatus(source({ dataset: "forecast" }), now).hazard_kinds).toEqual([]);
    const never = toSourceStatus(
      source({ lastSuccessAt: null, lastChangeAt: null, failingSince: T0, consecutiveFailures: 2 }),
      now,
    );
    expectSchema("SourceStatus", never);
    expect(never).toMatchObject({ last_success_at: null, late_after: null, state: "late" });
  });

  it("urut id dan generated_at dari jam", async () => {
    const reader = new FakeSourceReader();
    reader.rows = [source({ connector: "usgs-2.5-day", provider: "usgs" }), source()];
    const list = await new SourcesService(reader, fixedClock(T0)).list();
    expectSchema("SourceList", list);
    expect(list.items.map((s) => s.id)).toEqual(["bmkg-autogempa", "usgs-2.5-day"]);
    expect(list.generated_at).toBe(T0.toISOString());
    expect(sourceList([], T0).items).toEqual([]);
  });
});
