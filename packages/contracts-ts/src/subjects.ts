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
 * `forecast` dan `rain` hanya ada di raw: prakiraan bukan kejadian bahaya
 * (indeks hujan sub-DAS menjadi kejadian `hazard.flood.*`).
 */
export type RawKind = "quake" | "weather" | "flood" | "fire" | "aq" | "forecast" | "rain";
export type RawSource = "bmkg" | "usgs" | "openmeteo" | "openaq" | "firms";

const TOKEN = /^[a-z0-9]+(-[a-z0-9]+)*$/;
const CONNECTOR = /^[a-z0-9]+([.-][a-z0-9]+)*$/;

/** Pembentuk nama subjek NATS. */
export const subjects = {
  raw(kind: RawKind, source: RawSource): string {
    return `raw.${kind}.${source}`;
  },

  /** Antrean pesan gagal milik satu layanan, misal `dlq.geo-processor`. */
  dlq(service: string): string {
    if (!TOKEN.test(service)) {
      throw new Error(`nama layanan tidak valid: ${service}`);
    }
    return `dlq.${service}`;
  },

  /**
   * Hasil polling satu konektor ingest (ADR 0030), misal `status.connector.bmkg-autogempa`.
   * Titik di nama konektor (`usgs-2.5-day`) diganti `_` karena titik memisahkan token subjek.
   */
  status(connector: string): string {
    if (!CONNECTOR.test(connector)) {
      throw new Error(`nama konektor tidak valid: ${connector}`);
    }
    return `status.connector.${connector.replaceAll(".", "_")}`;
  },

  hazard(kind: HazardKind, transition: Transition): string {
    if (kind === HazardKind.UNSPECIFIED) {
      throw new Error("HazardKind.UNSPECIFIED tidak punya subjek");
    }
    return `hazard.${kindToken[kind]}.${transition}`;
  },
} as const;
