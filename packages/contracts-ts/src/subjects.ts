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

/** Potongan subjek untuk data raw dari ingest. Harus sama dengan libs/go/contracts/streams. */
export type RawKind = "quake" | "weather" | "flood" | "fire" | "aq";
export type RawSource = "bmkg" | "usgs" | "openmeteo" | "openaq" | "firms";

/** Pembentuk nama subjek NATS. */
export const subjects = {
  raw(kind: RawKind, source: RawSource): string {
    return `raw.${kind}.${source}`;
  },

  hazard(kind: HazardKind, transition: Transition): string {
    if (kind === HazardKind.UNSPECIFIED) {
      throw new Error("HazardKind.UNSPECIFIED tidak punya subjek");
    }
    return `hazard.${kindToken[kind]}.${transition}`;
  },
} as const;
