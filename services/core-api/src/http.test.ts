import "reflect-metadata";

import type { NestFastifyApplication } from "@nestjs/platform-fastify";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { contractRoutes, expectSchema, openapi } from "../test/support/contract.js";
import { FakeHazardReader, FakeRegionReader } from "../test/support/fakes.js";
import { hazard, impacted, quakeDetail, region, T0 } from "../test/support/records.js";
import { createApp } from "./app.js";
import { fixedClock } from "./shared/domain/clock.js";
import { UnavailableError } from "./shared/domain/errors.js";

// Lapisan HTTP lengkap (Nest + Fastify, filter Problem, validasi query) dengan
// pembaca palsu; setiap respons diperiksa terhadap kontrak OpenAPI.
const ID = "0b6c8a5e-3d1f-5a2b-9c4d-7e8f9a0b1c2d";
const hazards = new FakeHazardReader();
const regions = new FakeRegionReader();
let ready = true;
let app: NestFastifyApplication;

beforeAll(async () => {
  hazards.listRows = [
    hazard(),
    hazard({ id: "00000000-0000-4000-8000-000000000001", magnitude: null, kind: "weather" }),
  ];
  hazards.details.set(
    ID,
    quakeDetail({
      area: {
        type: "MultiPolygon",
        coordinates: [
          [
            [
              [106, -8],
              [107, -8],
              [107, -7],
              [106, -8],
            ],
          ],
        ],
      },
    }),
  );
  hazards.impacts.set(ID, [impacted(), impacted({ code: "32.02.01.2002", distanceKm: 20 })]);
  regions.rows = [region()];
  app = await createApp({
    hazards,
    regions,
    readiness: {
      check: () => (ready ? Promise.resolve() : Promise.reject(new UnavailableError("database"))),
    },
    clock: fixedClock(T0),
    version: "uji",
    corsOrigins: ["https://siaga.example"],
    logLevels: [],
  });
});

afterAll(async () => {
  await app.close();
});

async function get(url: string, headers: Record<string, string> = {}) {
  const res = await app.inject({ method: "GET", url, headers });
  return { status: res.statusCode, headers: res.headers, body: res.json<unknown>() };
}

async function problem(url: string, status: number) {
  const res = await get(url);
  expect(res.status).toBe(status);
  expect(res.headers["content-type"]).toMatch(/^application\/problem\+json/);
  expectSchema("Problem", res.body);
  return res.body as { errors?: { parameter: string }[]; type: string };
}

describe("kontrak", () => {
  it("HazardDetail memuat semua kolom HazardSummary (ditulis ulang tanpa allOf)", () => {
    const schemas = openapi.components.schemas as Record<
      string,
      { required: string[]; properties: Record<string, unknown> }
    >;
    const summary = schemas["HazardSummary"];
    const detail = schemas["HazardDetail"];
    expect(detail?.required).toEqual(expect.arrayContaining(summary?.required ?? ["?"]));
    for (const [name, schema] of Object.entries(summary?.properties ?? {})) {
      expect(detail?.properties[name], name).toEqual(schema);
    }
  });

  it("setiap operasi GET di OpenAPI punya rute", () => {
    const fastify = app.getHttpAdapter().getInstance();
    const missing = contractRoutes().filter((url) => !fastify.hasRoute({ method: "GET", url }));
    expect(missing).toEqual([]);
  });
});

describe("health", () => {
  it("healthz dan readyz", async () => {
    const h = await get("/healthz");
    expect(h.status).toBe(200);
    expectSchema("Health", h.body);
    expect((await get("/readyz")).status).toBe(200);
    ready = false;
    const body = await problem("/readyz", 503);
    expect(body.type).toBe("/problems/unavailable");
    ready = true;
  });
});

describe("GET /v1/hazards", () => {
  it("feed sesuai kontrak dengan cache singkat", async () => {
    const res = await get("/v1/hazards?kind=quake&kind=weather&min_level=2&limit=1&drill=true");
    expect(res.status).toBe(200);
    expectSchema("HazardPage", res.body);
    expect(res.headers["cache-control"]).toBe("public, max-age=15");
    expect(hazards.filters.at(-1)).toMatchObject({
      mode: "active",
      kinds: ["quake", "weather"],
      minLevel: 2,
      drill: true,
      fetch: 2,
    });
  });

  it("satu nilai kind juga diterima, at mengubah mode", async () => {
    await get("/v1/hazards?kind=fire&at=2026-11-23T10:00:00%2B07:00");
    expect(hazards.filters.at(-1)).toMatchObject({
      mode: "at",
      kinds: ["fire"],
      at: new Date("2026-11-23T03:00:00Z"),
    });
  });

  it("kursor halaman berikutnya bisa dipakai", async () => {
    const first = (await get("/v1/hazards?limit=1")).body as { next_cursor: string };
    const res = await get(`/v1/hazards?limit=1&cursor=${first.next_cursor}`);
    expect(res.status).toBe(200);
    expect(hazards.filters.at(-1)?.after?.id).toBe(ID);
  });

  it.each([
    ["kind=gempa", "kind.0"],
    ["status=semua", "status"],
    ["limit=0", "limit"],
    ["limit=201", "limit"],
    ["limit=1.5", "limit"],
    ["min_level=5", "min_level"],
    ["at=kemarin", "at"],
    ["drill=ya", "drill"],
    ["since=2026-11-02T00:00:00Z&until=2026-11-01T00:00:00Z", "until"],
    ["bbox=1,2,3", "bbox"],
    ["cursor=xx", "cursor"],
  ])("%s ditolak 400 dengan parameter %s", async (qs, parameter) => {
    const body = await problem(`/v1/hazards?${qs}`, 400);
    expect(body.type).toBe("/problems/invalid-parameter");
    expect(body.errors?.map((e) => e.parameter)).toContain(parameter);
  });

  it("database mati menjadi 503 dengan Retry-After", async () => {
    hazards.failWith = Object.assign(new Error("connect ECONNREFUSED"), { code: "ECONNREFUSED" });
    const res = await get("/v1/hazards");
    hazards.failWith = null;
    expect(res.status).toBe(503);
    expect(res.headers["retry-after"]).toBe("5");
  });

  it("galat tak terduga menjadi 500 tanpa membocorkan pesan", async () => {
    hazards.failWith = new Error("rahasia internal");
    const res = await get("/v1/hazards");
    hazards.failWith = null;
    expect(res.status).toBe(500);
    expect(JSON.stringify(res.body)).not.toContain("rahasia");
  });
});

describe("GET /v1/hazards/{id}", () => {
  it("detail tanpa area kecuali diminta", async () => {
    const plain = await get(`/v1/hazards/${ID}`);
    expect(plain.status).toBe(200);
    expectSchema("HazardDetail", plain.body);
    expect((plain.body as { area: unknown }).area).toBeNull();
    const withArea = await get(`/v1/hazards/${ID}?include=area`);
    expectSchema("HazardDetail", withArea.body);
    expect((withArea.body as { area: { type: string } }).area.type).toBe("MultiPolygon");
  });

  it("404, id bukan UUID, include tak dikenal", async () => {
    await problem("/v1/hazards/00000000-0000-4000-8000-000000000009", 404);
    expect((await problem("/v1/hazards/bukan-uuid", 400)).errors?.[0]?.parameter).toBe("id");
    await problem(`/v1/hazards/${ID}?include=semua`, 400);
  });
});

describe("GET /v1/hazards/{id}/regions", () => {
  it("halaman wilayah terdampak", async () => {
    const res = await get(`/v1/hazards/${ID}/regions?limit=1`);
    expect(res.status).toBe(200);
    expectSchema("ImpactedRegionPage", res.body);
    expect(res.body).toMatchObject({ basis: "felt_radius", total: 2 });
    await problem(`/v1/hazards/${ID}/regions?limit=501`, 400);
    await problem("/v1/hazards/00000000-0000-4000-8000-000000000009/regions", 404);
  });
});

describe("GET /v1/regions", () => {
  it("daftar dan detail", async () => {
    const list = await get("/v1/regions?parent=32&level=2");
    expect(list.status).toBe(200);
    expectSchema("RegionPage", list.body);
    expect(list.headers["cache-control"]).toBe("public, max-age=3600");
    const one = await get("/v1/regions/32.73?include=geometry");
    expect(one.status).toBe(200);
    expectSchema("Region", one.body);
  });

  it.each([
    ["/v1/regions?parent=32.7", "parent"],
    ["/v1/regions?q=ba", "q"],
    ["/v1/regions?level=5", "level"],
    ["/v1/regions/3273", "code"],
    ["/v1/regions/32.73?include=batas", "include"],
  ])("%s ditolak 400 (%s)", async (url, parameter) => {
    expect((await problem(url, 400)).errors?.map((e) => e.parameter)).toContain(parameter);
  });

  it("wilayah yang tidak ada 404", async () => {
    await problem("/v1/regions/32.99", 404);
  });
});

describe("lain-lain", () => {
  it("rute tak dikenal tetap Problem", async () => {
    const body = await problem("/v2/apa", 404);
    expect(body.type).toBe("about:blank");
  });

  it("CORS hanya untuk origin terdaftar", async () => {
    const ok = await get("/v1/hazards", { origin: "https://siaga.example" });
    expect(ok.headers["access-control-allow-origin"]).toBe("https://siaga.example");
    const other = await get("/v1/hazards", { origin: "https://lain.example" });
    expect(other.headers["access-control-allow-origin"]).toBeUndefined();
  });
});
