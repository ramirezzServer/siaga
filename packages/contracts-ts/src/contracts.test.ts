import { create, fromBinary, toBinary, toJson } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { describe, expect, it } from "vitest";

import {
  AlertLevel,
  CapMsgType,
  CapSeverity,
  AirQualityObservationSchema,
  FireConfidence,
  FireDetectionSchema,
  GridWeatherForecastSchema,
  HazardCreatedSchema,
  HazardKind,
  HazardSchema,
  QuakeFeed,
  QuakeReportSchema,
  RegionForecastSchema,
  RiverDischargeForecastSchema,
  Source,
  subjects,
  TsunamiPotential,
  WeatherWarningSchema,
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
    expect(subjects.raw("quake", "bmkg")).toBe("raw.quake.bmkg");
    expect(subjects.raw("weather", "bmkg")).toBe("raw.weather.bmkg");
    expect(subjects.raw("forecast", "bmkg")).toBe("raw.forecast.bmkg");
    expect(subjects.dlq("geo-processor")).toBe("dlq.geo-processor");
    expect(() => subjects.dlq("geo.processor")).toThrow();
  });
});

describe("kontrak event raw", () => {
  it("laporan gempa bolak-balik biner dan JSON memakai nama enum", () => {
    const report = create(QuakeReportSchema, {
      meta: {
        connector: "bmkg-autogempa",
        fetchedAt: timestampFromDate(new Date("2026-09-23T12:17:30Z")),
        archiveKey: "bmkg-autogempa/2026/09/23/121730Z-0123456789ab.json.gz",
        payloadSha256: "0".repeat(64),
      },
      source: Source.BMKG,
      feed: QuakeFeed.BMKG_LATEST,
      sourceEventId: "20260923121656",
      occurredAt: timestampFromDate(new Date("2026-09-23T12:16:56Z")),
      epicenter: { latitude: -8.51, longitude: 123.27 },
      magnitude: 1.8,
      depthKm: 12,
      feltDescription: "II-III Kab. Lembata",
      tsunamiPotential: TsunamiPotential.UNSPECIFIED,
    });
    const decoded = fromBinary(QuakeReportSchema, toBinary(QuakeReportSchema, report));
    expect(decoded).toEqual(report);
    expect(toJson(QuakeReportSchema, decoded)).toMatchObject({
      source: "SOURCE_BMKG",
      feed: "QUAKE_FEED_BMKG_LATEST",
    });
  });

  it("peringatan CAP bolak-balik biner dengan poligon dan dua bahasa", () => {
    const ring = [
      { latitude: -6.9, longitude: 107.6 },
      { latitude: -6.9, longitude: 107.7 },
      { latitude: -6.8, longitude: 107.7 },
      { latitude: -6.9, longitude: 107.6 },
    ];
    const warning = create(WeatherWarningSchema, {
      source: Source.BMKG,
      identifier: "2.49.0.1.360.0.2026.09.24.07.32.001",
      sent: timestampFromDate(new Date("2026-09-24T07:00:00Z")),
      msgType: CapMsgType.ALERT,
      severity: CapSeverity.SEVERE,
      expires: timestampFromDate(new Date("2026-09-24T09:00:00Z")),
      texts: [
        { language: "en", headline: "Thunderstorm in West Java" },
        { language: "id", headline: "Hujan Lebat disertai Petir di Jawa Barat" },
      ],
      areas: [{ areaDesc: "Jawa Barat", polygons: [{ points: ring }] }],
    });
    const decoded = fromBinary(WeatherWarningSchema, toBinary(WeatherWarningSchema, warning));
    expect(decoded).toEqual(warning);
    expect(toJson(WeatherWarningSchema, decoded)).toMatchObject({
      msgType: "CAP_MSG_TYPE_ALERT",
      severity: "CAP_SEVERITY_SEVERE",
    });
  });

  it("prakiraan adm4 membedakan jarak pandang kosong dan nol", () => {
    const forecast = create(RegionForecastSchema, {
      source: Source.BMKG,
      regionCode: "32.73.01.1001",
      steps: [
        { validTime: timestampFromDate(new Date("2026-09-24T07:00:00Z")), temperatureC: 28 },
        { validTime: timestampFromDate(new Date("2026-09-24T10:00:00Z")), visibilityM: 0 },
      ],
    });
    const decoded = fromBinary(RegionForecastSchema, toBinary(RegionForecastSchema, forecast));
    expect(decoded.steps[0]?.visibilityM).toBeUndefined();
    expect(decoded.steps[1]?.visibilityM).toBe(0);
  });

  it("keluaran model grid membedakan nilai kosong dan nol", () => {
    const grid = create(GridWeatherForecastSchema, {
      source: Source.OPEN_METEO,
      site: {
        id: "grid:-7.00:107.50",
        requested: { latitude: -7, longitude: 107.5 },
        cell: { latitude: -6.994727, longitude: 107.468346 },
      },
      model: "best_match",
      steps: [
        {
          validTime: timestampFromDate(new Date("2026-09-24T00:00:00Z")),
          precipitationMm: 0,
          weatherCode: 3,
        },
        { validTime: timestampFromDate(new Date("2026-09-24T01:00:00Z")), temperatureC: 23.9 },
      ],
    });
    const decoded = fromBinary(
      GridWeatherForecastSchema,
      toBinary(GridWeatherForecastSchema, grid),
    );
    expect(decoded.steps[0]?.precipitationMm).toBe(0);
    expect(decoded.steps[1]?.precipitationMm).toBeUndefined();
    expect(decoded.site?.elevationM).toBeUndefined();
    expect(subjects.raw("forecast", "openmeteo")).toBe("raw.forecast.openmeteo");
  });

  it("debit sungai membawa titik pantau dan statistik ensemble", () => {
    const flood = create(RiverDischargeForecastSchema, {
      source: Source.OPEN_METEO,
      site: { id: "river:citarum-dayeuhkolot", name: "Dayeuhkolot", river: "Citarum" },
      model: "glofas_v4",
      steps: [
        {
          validDate: timestampFromDate(new Date("2026-09-24T00:00:00Z")),
          dischargeM3s: 3.37,
          ensembleMaxM3s: 17.79,
        },
      ],
    });
    const decoded = fromBinary(
      RiverDischargeForecastSchema,
      toBinary(RiverDischargeForecastSchema, flood),
    );
    expect(decoded).toEqual(flood);
    expect(toJson(RiverDischargeForecastSchema, decoded)).toMatchObject({
      site: { river: "Citarum" },
    });
    expect(subjects.raw("flood", "openmeteo")).toBe("raw.flood.openmeteo");
  });
  it("pengukuran udara stasiun membawa satuan sumber per sensor", () => {
    const obs = create(AirQualityObservationSchema, {
      source: Source.OPENAQ,
      station: {
        id: "openaq:2178",
        name: "Stasiun Uji",
        location: { latitude: -6.9, longitude: 107.6 },
        provider: "AirGradient",
      },
      readings: [
        {
          sensorId: 3916n,
          parameter: "pm25",
          units: "µg/m³",
          value: 41.5,
          observedAt: timestampFromDate(new Date("2026-09-24T05:00:00Z")),
        },
      ],
    });
    const decoded = fromBinary(
      AirQualityObservationSchema,
      toBinary(AirQualityObservationSchema, obs),
    );
    expect(decoded.station?.isMonitor).toBe(false);
    expect(decoded.readings[0]?.sensorId).toBe(3916n);
    expect(decoded.readings[0]?.units).toBe("µg/m³");
    expect(subjects.raw("aq", "openaq")).toBe("raw.aq.openaq");
  });

  it("titik panas VIIRS tanpa persentase keyakinan", () => {
    const fire = create(FireDetectionSchema, {
      source: Source.NASA_FIRMS,
      id: "VIIRS_SNPP_NRT:20260924T0536:-6.91234:107.61234",
      product: "VIIRS_SNPP_NRT",
      instrument: "VIIRS",
      confidence: FireConfidence.NOMINAL,
      brightnessK: 330.2,
      frpMw: 0,
    });
    const decoded = fromBinary(FireDetectionSchema, toBinary(FireDetectionSchema, fire));
    expect(decoded.confidencePct).toBeUndefined();
    expect(decoded.frpMw).toBe(0);
    expect(decoded.backgroundBrightnessK).toBeUndefined();
    expect(subjects.raw("fire", "firms")).toBe("raw.fire.firms");
  });
});
