import { describe, expect, it } from "vitest";

import { FakeHazardReader } from "../../../test/support/fakes.js";
import { hazard, impacted, quakeDetail, T0 } from "../../../test/support/records.js";
import { fixedClock } from "../../shared/domain/clock.js";
import { NotFoundError } from "../../shared/domain/errors.js";
import { HazardsService } from "./hazards.service.js";

const ID = "0b6c8a5e-3d1f-5a2b-9c4d-7e8f9a0b1c2d";

function setup() {
  const reader = new FakeHazardReader();
  return { reader, svc: new HazardsService(reader, fixedClock(T0)) };
}

describe("HazardsService.list", () => {
  it("memakai jam yang disuntikkan dan memotong halaman", async () => {
    const { reader, svc } = setup();
    reader.listRows = [
      hazard({ id: "00000000-0000-4000-8000-000000000002" }),
      hazard({ id: "00000000-0000-4000-8000-000000000001" }),
    ];
    const page = await svc.list({ kinds: ["quake"], status: "active", drill: false, limit: 1 });
    expect(reader.filters[0]).toMatchObject({
      mode: "active",
      now: T0,
      kinds: ["quake"],
      fetch: 2,
    });
    expect(page.items.map((h) => h.id)).toEqual(["00000000-0000-4000-8000-000000000002"]);
    expect(page.next_cursor).not.toBeNull();
    expect(page.generated_at).toBe(T0.toISOString());
  });
});

describe("HazardsService.get", () => {
  it("detail atau 404", async () => {
    const { reader, svc } = setup();
    reader.details.set(ID, quakeDetail());
    expect((await svc.get(ID, false)).quake?.magnitude).toBe(4.8);
    await expect(svc.get("00000000-0000-4000-8000-000000000009", false)).rejects.toBeInstanceOf(
      NotFoundError,
    );
  });
});

describe("HazardsService.impactedRegions", () => {
  it("menghalaman dengan kursor offset sampai habis", async () => {
    const { reader, svc } = setup();
    reader.details.set(ID, quakeDetail());
    reader.impacts.set(ID, [
      impacted({ code: "a" }),
      impacted({ code: "b" }),
      impacted({ code: "c" }),
    ]);
    const p1 = await svc.impactedRegions(ID, 2);
    expect(p1).toMatchObject({ basis: "felt_radius", total: 3 });
    expect(p1.items.map((r) => r.code)).toEqual(["a", "b"]);
    const p2 = await svc.impactedRegions(ID, 2, p1.next_cursor ?? undefined);
    expect(p2.items.map((r) => r.code)).toEqual(["c"]);
    expect(p2.next_cursor).toBeNull();
  });

  it("halaman penuh yang tepat di akhir tidak memberi kursor", async () => {
    const { reader, svc } = setup();
    reader.details.set(ID, quakeDetail());
    reader.impacts.set(ID, [impacted({ code: "a" }), impacted({ code: "b" })]);
    expect((await svc.impactedRegions(ID, 2)).next_cursor).toBeNull();
  });

  it("kejadian tanpa estimasi wilayah dan kejadian yang tidak ada", async () => {
    const { reader, svc } = setup();
    reader.details.set(ID, { ...quakeDetail({ kind: "fire" }), quake: null, quakeReports: [] });
    expect(await svc.impactedRegions(ID, 10)).toEqual({
      basis: "none",
      total: 0,
      items: [],
      next_cursor: null,
    });
    await expect(
      svc.impactedRegions("00000000-0000-4000-8000-000000000009", 10),
    ).rejects.toBeInstanceOf(NotFoundError);
  });
});
