import { describe, expect, it } from "vitest";

import { expectSchema } from "../../test/support/contract.js";
import { FakeRegionReader } from "../../test/support/fakes.js";
import { region } from "../../test/support/records.js";
import { encodeCursor } from "../shared/domain/cursor.js";
import { InvalidParameterError, NotFoundError } from "../shared/domain/errors.js";
import { RegionsService } from "./app/regions.service.js";
import { buildRegionFilter, toRegion } from "./domain/region.js";

describe("buildRegionFilter", () => {
  it("tanpa pencarian memakai keyset kode", () => {
    expect(
      buildRegionFilter({
        limit: 10,
        parent: "32",
        level: 2,
        cursor: encodeCursor({ c: "32.10" }),
      }),
    ).toEqual({
      fetch: 11,
      parent: "32",
      level: 2,
      after: "32.10",
    });
  });

  it("dengan pencarian kursor diabaikan dan spasi dibuang", () => {
    expect(buildRegionFilter({ limit: 10, q: "  bandung ", cursor: "apa-saja" })).toEqual({
      fetch: 11,
      search: "bandung",
    });
  });

  it("kursor rusak ditolak", () => {
    expect(() =>
      buildRegionFilter({ limit: 10, cursor: encodeCursor({ c: "bukan kode" }) }),
    ).toThrow(InvalidParameterError);
  });
});

describe("RegionsService", () => {
  const reader = new FakeRegionReader();
  reader.rows = [
    region({ code: "32.04", name: "Kabupaten Bandung", kind: "kabupaten" }),
    region(),
    region({ code: "32.77", name: "Kota Cimahi" }),
  ];
  const svc = new RegionsService(reader);

  it("halaman urut kode dengan kursor", async () => {
    const p1 = await svc.list({ limit: 2 });
    expectSchema("RegionPage", p1);
    expect(p1.items.map((r) => r.code)).toEqual(["32.04", "32.73"]);
    expect(p1.next_cursor).not.toBeNull();
    const p2 = await svc.list({ limit: 2, ...(p1.next_cursor && { cursor: p1.next_cursor }) });
    expect(reader.filters.at(-1)?.after).toBe("32.73");
    expect(p2.next_cursor).toBeNull();
  });

  it("pencarian hanya satu halaman", async () => {
    const p = await svc.list({ limit: 1, q: "band" });
    expect(p.next_cursor).toBeNull();
  });

  it("detail dengan dan tanpa geometri, atau 404", async () => {
    reader.rows[1] = region({
      geometry: {
        type: "MultiPolygon",
        coordinates: [
          [
            [
              [107.5, -6.9],
              [107.7, -6.9],
              [107.7, -6.8],
              [107.5, -6.9],
            ],
          ],
        ],
      },
    });
    const withGeom = await svc.get("32.73", true);
    expectSchema("Region", withGeom);
    expect(withGeom.geometry?.type).toBe("MultiPolygon");
    expect("geometry" in (await svc.get("32.73", false))).toBe(false);
    await expect(svc.get("32.99", false)).rejects.toBeInstanceOf(NotFoundError);
  });

  it("provinsi tanpa induk", () => {
    expect(
      toRegion(region({ code: "32", level: 1, kind: "provinsi", parentCode: null })).parent_code,
    ).toBeNull();
  });
});
