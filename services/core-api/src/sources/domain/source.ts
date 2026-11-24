import type { components } from "@siaga/api-client";

/**
 * Status sumber data (ADR 0030). Keadaan konektor tersimpan di
 * ts.source_status (dilipat geo-processor dari hasil setiap polling ingest);
 * status publik dihitung di sini terhadap jam aplikasi, jadi ikut menua bila
 * ingest mati dan tetap benar setelah ingest dimulai ulang.
 */
export const SOURCE_PROVIDERS = ["bmkg", "usgs", "openmeteo", "openaq", "firms"] as const;
export const SOURCE_DATASETS = [
  "quake",
  "weather",
  "forecast",
  "aq",
  "flood",
  "rain",
  "fire",
] as const;

export type SourceProvider = (typeof SOURCE_PROVIDERS)[number];
export type SourceDataset = (typeof SOURCE_DATASETS)[number];
export type SourceState = components["schemas"]["SourceState"];
export type SourceStatusDto = components["schemas"]["SourceStatus"];
export type SourceListDto = components["schemas"]["SourceList"];
type HazardKind = components["schemas"]["HazardKind"];

/** Subjek raw yang dikenal, sama dengan constraint ts.source_status dan libs/go/contracts/streams. */
export const RAW_SUBJECT_PATTERN = new RegExp(
  `^raw\\.(${SOURCE_DATASETS.join("|")})\\.(${SOURCE_PROVIDERS.join("|")})$`,
);

/** Batas terlambat = waktu berhasil terakhir + LATE_INTERVALS × interval + LATE_SLACK_MS. */
export const LATE_INTERVALS = 2;
/** Sama dengan jeda terlama antar-polling ingest saat sumber terus gagal (MaxBackoff). */
export const LATE_SLACK_MS = 10 * 60_000;
/** Kegagalan beruntun mulai dianggap gangguan; sama dengan ambang log ERROR ingest. */
export const DEGRADED_FAILURES = 3;

/** Lapisan bahaya yang datanya berasal dari tiap jenis data. */
const HAZARD_KINDS: Record<SourceDataset, readonly HazardKind[]> = {
  quake: ["quake"],
  weather: ["weather"],
  forecast: [],
  aq: ["aq"],
  flood: ["flood"],
  rain: ["flood"],
  fire: ["fire"],
};

export interface SourceRecord {
  readonly connector: string;
  readonly provider: SourceProvider;
  readonly dataset: SourceDataset;
  readonly intervalSeconds: number;
  readonly lastAttemptAt: Date;
  readonly lastSuccessAt: Date | null;
  readonly lastChangeAt: Date | null;
  readonly failingSince: Date | null;
  readonly consecutiveFailures: number;
}

/** Memecah subjek raw menjadi penyedia dan jenis data; null bila tidak dikenal. */
export function parseSubject(
  subject: string,
): { provider: SourceProvider; dataset: SourceDataset } | null {
  const m = RAW_SUBJECT_PATTERN.exec(subject);
  if (!m) return null;
  return { dataset: m[1] as SourceDataset, provider: m[2] as SourceProvider };
}

/** Saat konektor menjadi terlambat bila tidak ada pengambilan berhasil lagi; null bila belum pernah berhasil. */
export function lateAfter(r: SourceRecord): Date | null {
  if (!r.lastSuccessAt) return null;
  return new Date(
    r.lastSuccessAt.getTime() + LATE_INTERVALS * r.intervalSeconds * 1000 + LATE_SLACK_MS,
  );
}

/** Status konektor pada saat now. */
export function sourceState(r: SourceRecord, now: Date): SourceState {
  const late = lateAfter(r);
  if (!late || now.getTime() > late.getTime()) return "late";
  return r.consecutiveFailures >= DEGRADED_FAILURES ? "degraded" : "ok";
}

const iso = (d: Date | null): string | null => (d ? d.toISOString() : null);

export function toSourceStatus(r: SourceRecord, now: Date): SourceStatusDto {
  return {
    id: r.connector,
    provider: r.provider,
    dataset: r.dataset,
    hazard_kinds: [...HAZARD_KINDS[r.dataset]],
    state: sourceState(r, now),
    interval_seconds: r.intervalSeconds,
    last_attempt_at: r.lastAttemptAt.toISOString(),
    last_success_at: iso(r.lastSuccessAt),
    last_change_at: iso(r.lastChangeAt),
    late_after: iso(lateAfter(r)),
    consecutive_failures: r.consecutiveFailures,
    failing_since: iso(r.failingSince),
  };
}

/** Daftar status urut id konektor. */
export function sourceList(rows: readonly SourceRecord[], now: Date): SourceListDto {
  return {
    items: [...rows]
      .sort((a, b) => (a.connector < b.connector ? -1 : a.connector > b.connector ? 1 : 0))
      .map((r) => toSourceStatus(r, now)),
    generated_at: now.toISOString(),
  };
}
