import { HazardKind } from "./gen/siaga/hazard/v1/hazard_pb.js";

/** Potongan subjek NATS per jenis bahaya. Harus sama dengan docs/events.md. */
const kindToken: Record<Exclude<HazardKind, HazardKind.UNSPECIFIED>, string> = {
  [HazardKind.EARTHQUAKE]: "quake",
  [HazardKind.WEATHER]: "weather",
  [HazardKind.FLOOD]: "flood",
  [HazardKind.WILDFIRE]: "fire",
  [HazardKind.AIR_QUALITY]: "aq",
};

type Transition = "created" | "updated" | "expired";

/**
 * Potongan subjek untuk data raw dari ingest. Harus sama dengan libs/go/contracts/streams.
 * `forecast` hanya ada di raw: prakiraan bukan kejadian bahaya.
 */
export type RawKind = "quake" | "weather" | "flood" | "fire" | "aq" | "forecast";
export type RawSource = "bmkg" | "usgs" | "openmeteo" | "openaq" | "firms";

/** Pembentuk nama subjek NATS. */
export const subjects = {
  raw(kind: RawKind, source: RawSource): string {
    return `raw.${kind}.${source}`;
  },

  /** Antrean pesan gagal milik satu layanan, misal `dlq.geo-processor`. */
  dlq(service: string): string {
    if (!/^[a-z0-9]+(-[a-z0-9]+)*$/.test(service)) {
      throw new Error(`nama layanan tidak valid: ${service}`);
    }
    return `dlq.${service}`;
  },

  hazard(kind: HazardKind, transition: Transition): string {
    if (kind === HazardKind.UNSPECIFIED) {
      throw new Error("HazardKind.UNSPECIFIED tidak punya subjek");
    }
    return `hazard.${kindToken[kind]}.${transition}`;
  },
} as const;
