import createFetchClient, { type ClientOptions } from "openapi-fetch";

import type { components, paths } from "./gen/core-api.js";

export type { components, paths };
export type Region = components["schemas"]["Region"];
export type RegionPage = components["schemas"]["RegionPage"];
export type Problem = components["schemas"]["Problem"];
export type HazardKind = components["schemas"]["HazardKind"];
export type HazardSummary = components["schemas"]["HazardSummary"];
export type HazardPage = components["schemas"]["HazardPage"];
export type HazardDetail = components["schemas"]["HazardDetail"];
export type QuakeDetail = components["schemas"]["QuakeDetail"];
export type WeatherDetail = components["schemas"]["WeatherDetail"];
export type FloodDetail = components["schemas"]["FloodDetail"];
export type FireDetail = components["schemas"]["FireDetail"];
export type ImpactedRegion = components["schemas"]["ImpactedRegion"];
export type ImpactedRegionPage = components["schemas"]["ImpactedRegionPage"];
export type SourceStatus = components["schemas"]["SourceStatus"];
export type SourceList = components["schemas"]["SourceList"];

/** Membuat klien Core API. Semua path, parameter, dan respons bertipe dari OpenAPI. */
export function createClient(options: ClientOptions & { baseUrl: string }) {
  return createFetchClient<paths>(options);
}
