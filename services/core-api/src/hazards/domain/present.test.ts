import { describe, expect, it } from "vitest";

import { expectSchema } from "../../../test/support/contract.js";
import {
  floodRecord,
  hazard,
  impacted,
  quakeDetail,
  report,
} from "../../../test/support/records.js";
import { impactBasis, type WeatherRecord } from "./hazard.js";
import {
  floodThresholds,
  isoDate,
  sortReports,
  toFireDetail,
  toFloodDetail,
  toHazardDetail,
  toHazardSummary,
  toImpactedRegion,
  toWeatherDetail,
  urlOrNull,
} from "./present.js";

const weather: WeatherRecord = {
  capIdentifier: "2.49.0.1.360.0.2026.11.24.02.32.008",
  msgType: "alert",
  severity: "moderate",
  urgency: "immediate",
  certainty: "likely",
  eventCode: "rain",
  sentAt: new Date("2026-11-24T02:32:00Z"),
  effectiveAt: new Date("2026-11-24T02:32:00Z"),
  onsetAt: null,
  areaDesc: "Kec. Lembang",
  areaKm2: 95.4,
  messageCount: 1,
  webUrl: "https://nowcasting.bmkg.go.id/",
  sourceUrl: "",
  textId: {
    event: "Hujan Lebat",
    headline: "Peringatan Dini Cuaca",
    description: "Hujan lebat disertai petir",
    instruction: "",
  },
  textEn: { event: "", headline: "", description: "", instruction: "" },
};

describe("ringkasan dan detail", () => {
  it("ringkasan sesuai kontrak", () => {
    const dto = toHazardSummary(
      hazard({ endedAt: new Date("2026-11-24T08:55:00Z"), status: "expired" }),
    );
    expectSchema("HazardSummary", dto);
    expect(dto.ended_at).toBe("2026-11-24T08:55:00.000Z");
    expect(toHazardSummary(hazard({ bbox: null })).bbox).toBeNull();
  });

  it("detail gempa: laporan sumber utama lebih dulu, URL kosong menjadi null", () => {
    const dto = toHazardDetail(quakeDetail());
    expectSchema("HazardDetail", dto);
    expect(dto.quake?.reports.map((r) => [r.source, r.primary])).toEqual([
      ["bmkg", true],
      ["usgs", false],
    ]);
    expect(dto.quake?.source_url).toBeNull();
    expect(dto.quake?.reports[1]?.url).toMatch(/^https:/);
    expect([dto.weather, dto.flood, dto.fire, dto.area]).toEqual([null, null, null, null]);
  });

  it("urutan laporan stabil", () => {
    const sorted = sortReports(
      [
        report({ source: "usgs", feed: "usgs-summary", sourceEventId: "b" }),
        report({ feed: "bmkg-recent", sourceEventId: "z" }),
        report({ source: "usgs", feed: "usgs-summary", sourceEventId: "a" }),
        report({ feed: "bmkg-felt", sourceEventId: "z" }),
        report({ feed: "bmkg-felt", sourceEventId: "y" }),
      ],
      "bmkg",
    );
    expect(sorted.map((r) => `${r.feed}/${r.sourceEventId}`)).toEqual([
      "bmkg-felt/y",
      "bmkg-felt/z",
      "bmkg-recent/z",
      "usgs-summary/a",
      "usgs-summary/b",
    ]);
  });

  it("cuaca: teks Inggris kosong menjadi null, terisi tetap dikirim", () => {
    const dto = toWeatherDetail(weather);
    expectSchema("WeatherDetail", dto);
    expect(dto.text_en).toBeNull();
    expect(dto.source_url).toBeNull();
    const en = toWeatherDetail({
      ...weather,
      onsetAt: weather.sentAt,
      textEn: { ...weather.textEn, headline: "Early warning" },
    });
    expect(en.text_en?.headline).toBe("Early warning");
    expect(en.onset_at).toBe("2026-11-24T02:32:00.000Z");
    expectSchema(
      "HazardDetail",
      toHazardDetail({
        hazard: hazard({ kind: "weather", magnitude: null }),
        quake: null,
        quakeReports: [],
        weather,
        flood: null,
        fire: null,
      }),
    );
  });

  it("banjir debit: satu set ambang, hari diurutkan, tanggal UTC", () => {
    const dto = toFloodDetail(floodRecord());
    expectSchema("FloodDetail", dto);
    expect(dto.unit).toBe("m3/s");
    expect(dto.thresholds).toEqual([
      { window_hours: null, info: 100, waspada: 150, siaga: 220, bahaya: 300 },
    ]);
    expect(dto.days.map((d) => d.date)).toEqual(["2026-11-24", "2026-11-25"]);
    expect(dto.peak_date).toBe("2026-11-25");
  });

  it("banjir indeks hujan: tiga set ambang per jendela, river null", () => {
    const f = floodRecord({
      indicator: "rainfall",
      siteId: "catchment:cikapundung",
      river: "",
      maxLevel: 3,
      peakDate: null,
      thresholds: [10, 20, 30, 40, 15, 25, 35, 45, 50, 70, 90, 120],
      days: floodRecord()
        .days.slice(0, 1)
        .map((d) => ({ ...d, windowHours: 6, dischargeM3s: null, fromEnsemble: false })),
    });
    const dto = toFloodDetail(f);
    expectSchema("FloodDetail", dto);
    expect(dto.unit).toBe("mm");
    expect(dto.river).toBeNull();
    expect(dto.peak_date).toBeNull();
    expect(dto.thresholds.map((t) => [t.window_hours, t.info, t.bahaya])).toEqual([
      [3, 10, 40],
      [6, 15, 45],
      [24, 50, 120],
    ]);
    expect(dto.days[0]?.window_hours).toBe(6);
  });

  it("ambang dengan jumlah salah adalah galat (bukan data yang dipotong diam-diam)", () => {
    expect(() => floodThresholds(floodRecord({ thresholds: [1, 2, 3] }))).toThrow(/4 nilai/);
    expect(() =>
      floodThresholds(floodRecord({ indicator: "rainfall", thresholds: [1, 2, 3, 4] })),
    ).toThrow(/12 nilai/);
  });

  it("titik api", () => {
    const dto = toFireDetail({
      detectionCount: 6,
      overpassCount: 2,
      highCount: 1,
      firstDetectedAt: new Date("2026-11-23T18:00:00Z"),
      lastDetectedAt: new Date("2026-11-24T06:00:00Z"),
      products: ["VIIRS_NOAA20_NRT", "MODIS_NRT"],
      maxFrpMw: null,
    });
    expectSchema("FireDetail", dto);
    expect(dto.high_confidence_count).toBe(1);
  });

  it("wilayah terdampak membawa induknya", () => {
    const dto = toImpactedRegion(impacted());
    expectSchema("ImpactedRegion", dto);
    expect(dto.district).toEqual({ code: "32.02.01", name: "Pelabuhanratu", kind: "kecamatan" });
    expect(dto.city.kind).toBe("kabupaten");
  });

  it("dasar estimasi per jenis", () => {
    expect(["quake", "weather", "flood", "fire", "aq"].map((k) => impactBasis(k as never))).toEqual(
      ["felt_radius", "area_overlap", "none", "none", "none"],
    );
  });

  it("utilitas", () => {
    expect(urlOrNull("")).toBeNull();
    expect(urlOrNull("https://x")).toBe("https://x");
    expect(isoDate(new Date("2026-11-24T00:00:00Z"))).toBe("2026-11-24");
  });
});
