import type {
  FloodRecord,
  HazardDetailRecord,
  HazardRecord,
  ImpactedRegionRecord,
  QuakeReportRecord,
} from "../../src/hazards/domain/hazard.js";
import type { RegionRecord } from "../../src/regions/domain/region.js";
import type { SourceRecord } from "../../src/sources/domain/source.js";

export const T0 = new Date("2026-11-24T03:00:00Z");

export function hazard(over: Partial<HazardRecord> = {}): HazardRecord {
  return {
    id: "0b6c8a5e-3d1f-5a2b-9c4d-7e8f9a0b1c2d",
    kind: "quake",
    level: 2,
    status: "active",
    mergedInto: null,
    primarySource: "bmkg",
    sourceEventId: "20261124095500",
    title: "Gempa M4,8 Kab. Sukabumi",
    summary: "Gempa M4,8 kedalaman 10 km, 24 km barat daya Kab. Sukabumi",
    occurredAt: new Date("2026-11-24T02:55:00Z"),
    detectedAt: new Date("2026-11-24T02:56:10Z"),
    expiresAt: new Date("2026-11-24T08:55:00Z"),
    endedAt: null,
    updatedAt: new Date("2026-11-24T02:57:00Z"),
    revision: 1,
    drill: false,
    location: { latitude: -7.12, longitude: 106.71 },
    bbox: [105.9, -8.1, 107.5, -6.1],
    area: null,
    magnitude: 4.8,
    impactedRegionCount: 2,
    ...over,
  };
}

export function report(over: Partial<QuakeReportRecord> = {}): QuakeReportRecord {
  return {
    source: "bmkg",
    feed: "bmkg-recent",
    sourceEventId: "20261124095500",
    occurredAt: new Date("2026-11-24T02:55:00Z"),
    location: { latitude: -7.12, longitude: 106.71 },
    magnitude: 4.8,
    magnitudeType: "M",
    depthKm: 10,
    place: "24 km BaratDaya KAB-SUKABUMI",
    tsunami: "none",
    sourceUrl: "",
    fetchedAt: new Date("2026-11-24T02:56:10Z"),
    ...over,
  };
}

export function quakeDetail(over: Partial<HazardRecord> = {}): HazardDetailRecord {
  return {
    hazard: hazard(over),
    quake: {
      magnitude: 4.8,
      magnitudeType: "M",
      depthKm: 10,
      feltRadiusKm: 94.5,
      place: "24 km BaratDaya KAB-SUKABUMI",
      feltDescription: "III Sukabumi",
      tsunami: "none",
      shakemapUrl: "https://data.bmkg.go.id/DataMKG/TEWS/20261124095500.mmi.jpg",
      sourceUrl: "",
    },
    quakeReports: [
      report({
        source: "usgs",
        feed: "usgs-summary",
        sourceEventId: "us7000abcd",
        sourceUrl: "https://earthquake.usgs.gov/earthquakes/eventpage/us7000abcd",
      }),
      report(),
    ],
    weather: null,
    flood: null,
    fire: null,
  };
}

export function floodRecord(over: Partial<FloodRecord> = {}): FloodRecord {
  return {
    indicator: "discharge",
    siteId: "river:citarum-dayeuhkolot",
    siteName: "Citarum Dayeuhkolot",
    river: "Citarum",
    model: "glofas_seamless_v4",
    issuedAt: new Date("2026-11-24T00:00:00Z"),
    outlookLevel: 2,
    possible: false,
    peakDate: new Date("2026-11-25T00:00:00Z"),
    maxLevel: 4,
    openedAt: new Date("2026-11-23T00:00:00Z"),
    lastExceededAt: new Date("2026-11-24T00:00:00Z"),
    thresholds: [100, 150, 220, 300],
    days: [
      {
        date: new Date("2026-11-25T00:00:00Z"),
        level: 2,
        possible: false,
        dischargeM3s: 160,
        dischargeP75M3s: 175,
        dischargeMaxM3s: 210,
        fromEnsemble: true,
        rain3hMm: null,
        rain6hMm: null,
        rain24hMm: null,
        windowHours: 0,
      },
      {
        date: new Date("2026-11-24T00:00:00Z"),
        level: 1,
        possible: false,
        dischargeM3s: 120,
        dischargeP75M3s: null,
        dischargeMaxM3s: null,
        fromEnsemble: false,
        rain3hMm: null,
        rain6hMm: null,
        rain24hMm: null,
        windowHours: 0,
      },
    ],
    ...over,
  };
}

export function impacted(over: Partial<ImpactedRegionRecord> = {}): ImpactedRegionRecord {
  return {
    code: "32.02.01.2001",
    name: "Cidadap",
    kind: "desa",
    districtCode: "32.02.01",
    districtName: "Pelabuhanratu",
    cityCode: "32.02",
    cityName: "Kabupaten Sukabumi",
    cityKind: "kabupaten",
    distanceKm: 12.5,
    level: 2,
    withinFelt: true,
    coverage: null,
    ...over,
  };
}

export function region(over: Partial<RegionRecord> = {}): RegionRecord {
  return {
    code: "32.73",
    name: "Kota Bandung",
    level: 2,
    kind: "kota",
    parentCode: "32",
    labelPoint: { latitude: -6.92, longitude: 107.64 },
    bbox: [107.54, -6.97, 107.74, -6.84],
    geometry: null,
    ...over,
  };
}

/** Konektor sehat: berhasil 20 detik sebelum T0, polling tiap 30 detik. */
export function source(over: Partial<SourceRecord> = {}): SourceRecord {
  const ok = new Date(T0.getTime() - 20_000);
  return {
    connector: "bmkg-autogempa",
    provider: "bmkg",
    dataset: "quake",
    intervalSeconds: 30,
    lastAttemptAt: ok,
    lastSuccessAt: ok,
    lastChangeAt: new Date(T0.getTime() - 3_600_000),
    failingSince: null,
    consecutiveFailures: 0,
    ...over,
  };
}
