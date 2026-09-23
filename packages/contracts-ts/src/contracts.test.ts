import { create, fromBinary, toBinary, toJson } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { describe, expect, it } from "vitest";

import {
  AlertLevel,
  HazardCreatedSchema,
  HazardKind,
  HazardSchema,
  Source,
  subjects,
} from "./index.js";

describe("kontrak event bahaya", () => {
  it("bolak-balik biner tanpa kehilangan data", () => {
    const event = create(HazardCreatedSchema, {
      hazard: create(HazardSchema, {
        id: "bmkg:20260921123000",
        kind: HazardKind.EARTHQUAKE,
        level: AlertLevel.SIAGA,
        primarySource: Source.BMKG,
        occurredAt: timestampFromDate(new Date("2026-09-21T05:30:00Z")),
        location: { latitude: -6.9, longitude: 107.6 },
        impactedRegions: [{ code: "32.73.02.1003", name: "Sadang Serang" }],
        detail: { case: "earthquake", value: { magnitude: 5.1, depthKm: 10 } },
      }),
    });
    const decoded = fromBinary(HazardCreatedSchema, toBinary(HazardCreatedSchema, event));
    expect(decoded).toEqual(event);
    expect(toJson(HazardCreatedSchema, decoded)).toMatchObject({
      hazard: { kind: "HAZARD_KIND_EARTHQUAKE", level: "ALERT_LEVEL_SIAGA" },
    });
  });

  it("membentuk subjek NATS sesuai konvensi", () => {
    expect(subjects.hazard(HazardKind.EARTHQUAKE, "created")).toBe("hazard.quake.created");
    expect(subjects.hazard(HazardKind.AIR_QUALITY, "expired")).toBe("hazard.aq.expired");
    expect(() => subjects.hazard(HazardKind.UNSPECIFIED, "created")).toThrow();
  });
});
