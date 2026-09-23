import createFetchClient, { type ClientOptions } from "openapi-fetch";

import type { components, paths } from "./gen/core-api.js";

export type { components, paths };
export type Region = components["schemas"]["Region"];
export type RegionPage = components["schemas"]["RegionPage"];
export type Problem = components["schemas"]["Problem"];

/** Membuat klien Core API. Semua path, parameter, dan respons bertipe dari OpenAPI. */
export function createClient(options: ClientOptions & { baseUrl: string }) {
  return createFetchClient<paths>(options);
}
