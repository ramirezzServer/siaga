import "reflect-metadata";

import type { NestFastifyApplication } from "@nestjs/platform-fastify";
import type pg from "pg";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { createApp } from "../src/app.js";
import { PgHazardReader } from "../src/hazards/adapters/pg-hazard-reader.js";
import { PgRegionReader } from "../src/regions/adapters/pg-region-reader.js";
import { createPool, PgReadinessProbe } from "../src/shared/adapters/postgres.js";
import { fixedClock } from "../src/shared/domain/clock.js";
import { expectSchema } from "./support/contract.js";
import { clean, IDS, NOW, seed, urls } from "./support/seed.js";

// Integrasi dengan PostgreSQL asli lewat role siaga_core: SQL adapter, grant
// baca-saja, dan bentuk respons terhadap kontrak. Jam aplikasi dipaku ke 2001
// supaya data uji (tahun 2001) bisa "aktif" tanpa menyentuh data nyata.
const env = urls();
const d = env ? describe : describe.skip;

let pool: pg.Pool;
let app: NestFastifyApplication;

// Tipe body hanya penanda untuk test; bentuknya diperiksa expectSchema terhadap kontrak.
// eslint-disable-next-line @typescript-eslint/no-unnecessary-type-parameters
async function get<T = unknown>(url: string): Promise<{ status: number; body: T }> {
  const res = await app.inject({ method: "GET", url });
  return { status: res.statusCode, body: res.json<T>() };
}

interface Page {
  items: {
    id: string;
    kind: string;
    status: string;
    magnitude: number | null;
    impacted_region_count: number;
  }[];
  next_cursor: string | null;
}

// Hanya kejadian uji (tahun 2001) yang dilihat, apa pun isi database lokal.
const window2001 = "since=2001-01-01T00:00:00Z&until=2002-01-01T00:00:00Z";

d("core-api dengan PostgreSQL", () => {
  beforeAll(async () => {
    if (!env) return;
    await seed(env.geo);
    pool = createPool({ url: env.core, max: 4, statementTimeoutMs: 5_000 });
    app = await createApp({
      hazards: new PgHazardReader(pool),
      regions: new PgRegionReader(pool),
      readiness: new PgReadinessProbe(pool),
      clock: fixedClock(NOW),
      version: "uji",
      corsOrigins: [],
      logLevels: ["error"],
    });
  });

  afterAll(async () => {
    if (!env) return;
    await app.close();
    await pool.end();
    await clean(env.geo);
  });

  it("readyz menyentuh database", async () => {
    expect((await get("/readyz")).status).toBe(200);
  });

  describe("feed", () => {
    it("bawaan: aktif pada jam aplikasi, tanpa merged, tanpa latihan, terbaru dulu", async () => {
      const res = await get<Page>(`/v1/hazards?${window2001}`);
      expect(res.status).toBe(200);
      expectSchema("HazardPage", res.body);
      expect(res.body.items.map((h) => h.id)).toEqual([IDS.weather, IDS.quake]);
      const quake = res.body.items.find((h) => h.id === IDS.quake);
      expect(quake).toMatchObject({ magnitude: 4.9, impacted_region_count: 2 });
    });

    it("status=all memuat yang berakhir; drill=true memuat latihan; merged tidak pernah", async () => {
      const all = await get<Page>(`/v1/hazards?${window2001}&status=all&drill=true`);
      expect(all.body.items.map((h) => h.id)).toEqual([
        IDS.weather,
        IDS.quake,
        IDS.fire,
        IDS.flood,
      ]);
      expect(all.body.items.map((h) => h.id)).not.toContain(IDS.merged);
    });

    it("at: yang tampil pada saat itu (banjir sebelum berakhir, cuaca setelah terdeteksi)", async () => {
      const early = await get<Page>(`/v1/hazards?${window2001}&at=2001-01-02T03:30:00Z`);
      expect(early.body.items.map((h) => h.id)).toEqual([IDS.quake, IDS.flood]);
      const later = await get<Page>(`/v1/hazards?${window2001}&at=2001-01-02T04:45:00Z`);
      expect(later.body.items.map((h) => h.id)).toEqual([IDS.weather, IDS.quake]);
    });

    it("saringan jenis, tingkat, dan bbox", async () => {
      const kinds = await get<Page>(`/v1/hazards?${window2001}&status=all&kind=flood&kind=quake`);
      expect(kinds.body.items.map((h) => h.id)).toEqual([IDS.quake, IDS.flood]);
      const level = await get<Page>(`/v1/hazards?${window2001}&min_level=3`);
      expect(level.body.items.map((h) => h.id)).toEqual([IDS.weather]);
      const box = await get<Page>(
        `/v1/hazards?${window2001}&status=all&bbox=107.05,-7.05,107.5,-6.5`,
      );
      expect(box.body.items.map((h) => h.id)).toEqual([IDS.weather, IDS.flood]);
    });

    it("kursor keyset menelusuri semua halaman tanpa ganda", async () => {
      const seen: string[] = [];
      let cursor: string | null = null;
      do {
        const qs: string = cursor ? `&cursor=${cursor}` : "";
        const res: { body: Page } = await get<Page>(
          `/v1/hazards?${window2001}&status=all&drill=true&limit=1${qs}`,
        );
        seen.push(...res.body.items.map((h) => h.id));
        cursor = res.body.next_cursor;
      } while (cursor);
      expect(seen).toEqual([IDS.weather, IDS.quake, IDS.fire, IDS.flood]);
    });
  });

  describe("detail", () => {
    it("gempa: BMKG utama, USGS di laporan (US-05), area bila diminta", async () => {
      const res = await get<{
        quake: {
          reports: { source: string; primary: boolean; url: string | null }[];
          shakemap_url: string;
        };
        area: { type: string } | null;
        bbox: number[];
      }>(`/v1/hazards/${IDS.quake}?include=area`);
      expect(res.status).toBe(200);
      expectSchema("HazardDetail", res.body);
      expect(res.body.quake.reports.map((r) => [r.source, r.primary])).toEqual([
        ["bmkg", true],
        ["usgs", false],
      ]);
      expect(res.body.quake.reports[0]?.url).toBeNull();
      expect(res.body.area?.type).toBe("MultiPolygon");
      expect(res.body.bbox).toEqual([106.5, -7.5, 107.5, -6.5]);
    });

    it("gempa yang digabung tetap bisa dibuka dan menunjuk kejadian yang bertahan", async () => {
      const res = await get<{ status: string; merged_into: string; quake: unknown }>(
        `/v1/hazards/${IDS.merged}`,
      );
      expect(res.status).toBe(200);
      expectSchema("HazardDetail", res.body);
      expect(res.body).toMatchObject({ status: "merged", merged_into: IDS.quake, quake: null });
    });

    it("cuaca: teks asli id, terjemahan en sebagian", async () => {
      const res = await get<{
        weather: { severity: string; text_id: { event: string }; text_en: { headline: string } };
      }>(`/v1/hazards/${IDS.weather}`);
      expectSchema("HazardDetail", res.body);
      expect(res.body.weather).toMatchObject({
        severity: "severe",
        text_id: { event: "Hujan Lebat" },
        text_en: { headline: "Weather early warning" },
      });
    });

    it("banjir dan titik api", async () => {
      const flood = await get<{ flood: { days: { date: string }[]; peak_date: string } }>(
        `/v1/hazards/${IDS.flood}`,
      );
      expectSchema("HazardDetail", flood.body);
      expect(flood.body.flood.days.map((x) => x.date)).toEqual(["2001-01-02", "2001-01-03"]);
      expect(flood.body.flood.peak_date).toBe("2001-01-03");
      const fire = await get<{ fire: { products: string[]; max_frp_mw: number } }>(
        `/v1/hazards/${IDS.fire}`,
      );
      expectSchema("HazardDetail", fire.body);
      expect(fire.body.fire).toMatchObject({
        products: ["VIIRS_NOAA20_NRT", "MODIS_NRT"],
        max_frp_mw: 12.5,
      });
    });

    it("404 untuk kejadian yang tidak ada", async () => {
      expect((await get(`/v1/hazards/${IDS.missing}`)).status).toBe(404);
      expect((await get(`/v1/hazards/${IDS.missing}/regions`)).status).toBe(404);
    });
  });

  describe("wilayah terdampak (US-06)", () => {
    it("gempa: terdekat dulu, nama kecamatan dan kota dari induk", async () => {
      const res = await get<{
        basis: string;
        total: number;
        items: { code: string; district: { name: string }; city: { name: string; kind: string } }[];
      }>(`/v1/hazards/${IDS.quake}/regions`);
      expectSchema("ImpactedRegionPage", res.body);
      expect(res.body.basis).toBe("felt_radius");
      expect(res.body.items.map((r) => r.code)).toEqual(["99.71.01.1001", "99.71.01.1002"]);
      expect(res.body.items[0]).toMatchObject({
        district: { name: "Kecamatan Uji" },
        city: { name: "Kota Uji", kind: "kota" },
      });
    });

    it("cuaca: cakupan terluas dulu, menghalaman dengan kursor", async () => {
      const p1 = await get<{
        basis: string;
        total: number;
        items: { code: string }[];
        next_cursor: string;
      }>(`/v1/hazards/${IDS.weather}/regions?limit=1`);
      expect(p1.body).toMatchObject({ basis: "area_overlap", total: 2 });
      expect(p1.body.items.map((r) => r.code)).toEqual(["99.71.01.2003"]);
      const p2 = await get<{ items: { code: string }[]; next_cursor: string | null }>(
        `/v1/hazards/${IDS.weather}/regions?limit=1&cursor=${p1.body.next_cursor}`,
      );
      expect(p2.body.items.map((r) => r.code)).toEqual(["99.71.01.1001"]);
      expect(p2.body.next_cursor).toBeNull();
    });
  });

  describe("wilayah", () => {
    it("anak langsung urut kode, dengan kursor", async () => {
      const p1 = await get<{ items: { code: string }[]; next_cursor: string }>(
        "/v1/regions?parent=99.71.01&limit=2",
      );
      expectSchema("RegionPage", p1.body);
      expect(p1.body.items.map((r) => r.code)).toEqual(["99.71.01.1001", "99.71.01.1002"]);
      const p2 = await get<{ items: { code: string }[]; next_cursor: string | null }>(
        `/v1/regions?parent=99.71.01&limit=2&cursor=${p1.body.next_cursor}`,
      );
      expect(p2.body.items.map((r) => r.code)).toEqual(["99.71.01.2003"]);
      expect(p2.body.next_cursor).toBeNull();
    });

    it("pencarian toleran salah ketik dan aman dari metakarakter LIKE", async () => {
      const typo = await get<{ items: { code: string }[] }>(
        "/v1/regions?parent=99.71.01&q=Bandungn",
      );
      expect(typo.body.items.map((r) => r.code)).toContain("99.71.01.2003");
      const meta = await get<{ items: unknown[] }>("/v1/regions?parent=99.71.01&q=%25%25%25");
      expect(meta.body.items).toEqual([]);
    });

    it("detail dengan geometri", async () => {
      const res = await get<{ kind: string; parent_code: string; geometry: { type: string } }>(
        "/v1/regions/99.71.01.1001?include=geometry",
      );
      expectSchema("Region", res.body);
      expect(res.body).toMatchObject({
        kind: "kelurahan",
        parent_code: "99.71.01",
        geometry: { type: "MultiPolygon" },
      });
    });
  });

  describe("batas akses (ADR 0029)", () => {
    it("sesi core-api read-only dan role siaga_core tidak bisa menulis skema hazard", async () => {
      await expect(pool.query("DELETE FROM hazard.event WHERE false")).rejects.toMatchObject({
        code: "25006",
      });
      const client = await pool.connect();
      try {
        await client.query("SET SESSION CHARACTERISTICS AS TRANSACTION READ WRITE");
        await expect(client.query("DELETE FROM hazard.event WHERE false")).rejects.toMatchObject({
          code: "42501",
        });
        await expect(client.query("SELECT 1 FROM hazard.outbox LIMIT 1")).rejects.toMatchObject({
          code: "42501",
        });
      } finally {
        await client.query("SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY");
        client.release();
      }
    });
  });
});
