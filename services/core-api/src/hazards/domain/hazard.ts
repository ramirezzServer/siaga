import type { BBox, MultiPolygon, Point } from "../../shared/domain/geo.js";

// Nilai sama dengan constraint hazard.event (migrasi geo-processor 00002).
export const HAZARD_KINDS = ["quake", "weather", "flood", "fire", "aq"] as const;
export type HazardKind = (typeof HAZARD_KINDS)[number];

export const HAZARD_STATUSES = ["active", "expired", "merged", "retracted"] as const;
export type HazardStatus = (typeof HAZARD_STATUSES)[number];

export const HAZARD_SOURCES = ["bmkg", "usgs", "openmeteo", "openaq", "firms", "drill"] as const;
export type HazardSource = (typeof HAZARD_SOURCES)[number];

/** 1 Info, 2 Waspada, 3 Siaga, 4 Bahaya. */
export type AlertLevel = 1 | 2 | 3 | 4;

export const TSUNAMI_STATUSES = ["unknown", "none", "potential"] as const;
export type Tsunami = (typeof TSUNAMI_STATUSES)[number];

/** Satu baris hazard.event beserta ringkasan yang dihitung adapter. */
export interface HazardRecord {
  readonly id: string;
  readonly kind: HazardKind;
  readonly level: AlertLevel;
  readonly status: HazardStatus;
  readonly mergedInto: string | null;
  readonly primarySource: HazardSource;
  readonly sourceEventId: string;
  readonly title: string;
  readonly summary: string;
  readonly occurredAt: Date;
  readonly detectedAt: Date;
  readonly expiresAt: Date;
  readonly endedAt: Date | null;
  readonly updatedAt: Date;
  readonly revision: number;
  readonly drill: boolean;
  readonly location: Point;
  readonly bbox: BBox | null;
  /** Hanya diisi bila diminta (include=area). */
  readonly area: MultiPolygon | null;
  readonly magnitude: number | null;
  readonly impactedRegionCount: number;
}

export interface QuakeRecord {
  readonly magnitude: number;
  readonly magnitudeType: string;
  readonly depthKm: number;
  readonly feltRadiusKm: number;
  readonly place: string;
  readonly feltDescription: string;
  readonly tsunami: Tsunami;
  readonly shakemapUrl: string;
  readonly sourceUrl: string;
}

export const QUAKE_FEEDS = ["bmkg-latest", "bmkg-recent", "bmkg-felt", "usgs-summary"] as const;
export type QuakeFeed = (typeof QUAKE_FEEDS)[number];

export interface QuakeReportRecord {
  readonly source: HazardSource;
  readonly feed: QuakeFeed;
  readonly sourceEventId: string;
  readonly occurredAt: Date;
  readonly location: Point;
  readonly magnitude: number;
  readonly magnitudeType: string;
  readonly depthKm: number;
  readonly place: string;
  readonly tsunami: Tsunami;
  readonly sourceUrl: string;
  readonly fetchedAt: Date;
}

export const CAP_SEVERITIES = ["minor", "moderate", "severe", "extreme", "unknown"] as const;
export type CapSeverity = (typeof CAP_SEVERITIES)[number];

export const CAP_MSG_TYPES = ["alert", "update", "cancel"] as const;
export type CapMsgType = (typeof CAP_MSG_TYPES)[number];

export interface CapTexts {
  readonly event: string;
  readonly headline: string;
  readonly description: string;
  readonly instruction: string;
}

export interface WeatherRecord {
  readonly capIdentifier: string;
  readonly msgType: CapMsgType;
  readonly severity: CapSeverity;
  readonly urgency: string;
  readonly certainty: string;
  readonly eventCode: string;
  readonly sentAt: Date;
  readonly effectiveAt: Date;
  readonly onsetAt: Date | null;
  readonly areaDesc: string;
  readonly areaKm2: number;
  readonly messageCount: number;
  readonly webUrl: string;
  readonly sourceUrl: string;
  readonly textId: CapTexts;
  /** Teks bahasa Inggris; semua kosong bila BMKG tidak menerbitkannya. */
  readonly textEn: CapTexts;
}

export const FLOOD_INDICATORS = ["discharge", "rainfall"] as const;
export type FloodIndicator = (typeof FLOOD_INDICATORS)[number];

export interface FloodDayRecord {
  /** Tengah malam UTC. */
  readonly date: Date;
  readonly level: number;
  readonly possible: boolean;
  readonly dischargeM3s: number | null;
  readonly dischargeP75M3s: number | null;
  readonly dischargeMaxM3s: number | null;
  readonly fromEnsemble: boolean;
  readonly rain3hMm: number | null;
  readonly rain6hMm: number | null;
  readonly rain24hMm: number | null;
  /** 0 untuk debit atau di bawah Info. */
  readonly windowHours: number;
}

export interface FloodRecord {
  readonly indicator: FloodIndicator;
  readonly siteId: string;
  readonly siteName: string;
  readonly river: string;
  readonly model: string;
  readonly issuedAt: Date;
  readonly outlookLevel: number;
  readonly possible: boolean;
  readonly peakDate: Date | null;
  readonly maxLevel: AlertLevel;
  readonly openedAt: Date;
  readonly lastExceededAt: Date;
  /** 4 nilai debit, atau 12 nilai indeks hujan (jendela 3, 6, 24 jam berurutan). */
  readonly thresholds: readonly number[];
  readonly days: readonly FloodDayRecord[];
}

export const FIRE_PRODUCTS = [
  "VIIRS_SNPP_NRT",
  "VIIRS_NOAA20_NRT",
  "VIIRS_NOAA21_NRT",
  "MODIS_NRT",
] as const;
export type FireProduct = (typeof FIRE_PRODUCTS)[number];

export interface FireRecord {
  readonly detectionCount: number;
  readonly overpassCount: number;
  readonly highCount: number;
  readonly firstDetectedAt: Date;
  readonly lastDetectedAt: Date;
  readonly products: readonly FireProduct[];
  readonly maxFrpMw: number | null;
}

/** Detail lengkap satu kejadian; hanya detail jenisnya sendiri yang terisi. */
export interface HazardDetailRecord {
  readonly hazard: HazardRecord;
  readonly quake: QuakeRecord | null;
  readonly quakeReports: readonly QuakeReportRecord[];
  readonly weather: WeatherRecord | null;
  readonly flood: FloodRecord | null;
  readonly fire: FireRecord | null;
}

export interface ImpactedRegionRecord {
  readonly code: string;
  readonly name: string;
  readonly kind: "kelurahan" | "desa";
  readonly districtCode: string;
  readonly districtName: string;
  readonly cityCode: string;
  readonly cityName: string;
  readonly cityKind: "kabupaten" | "kota";
  readonly distanceKm: number;
  readonly level: number;
  readonly withinFelt: boolean;
  readonly coverage: number | null;
}

/** Dasar estimasi wilayah terdampak per jenis (geo-processor hanya menulisnya untuk gempa dan cuaca). */
export type ImpactBasis = "felt_radius" | "area_overlap" | "none";

export function impactBasis(kind: HazardKind): ImpactBasis {
  switch (kind) {
    case "quake":
      return "felt_radius";
    case "weather":
      return "area_overlap";
    case "flood":
    case "fire":
    case "aq":
      return "none";
  }
}
