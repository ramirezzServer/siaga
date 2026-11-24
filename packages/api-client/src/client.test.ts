import { describe, expect, it } from "vitest";

import { createClient, type Region, type SourceList } from "./index.js";

const bandung: Region = {
  code: "32.73",
  name: "Kota Bandung",
  level: 2,
  kind: "kota",
  parent_code: "32",
  label_point: { latitude: -6.92, longitude: 107.64 },
};

describe("createClient", () => {
  it("memanggil path yang benar dan mengembalikan data bertipe", async () => {
    const calls: string[] = [];
    const client = createClient({
      baseUrl: "https://api.test",
      fetch: (input: Request) => {
        calls.push(input.url);
        return Promise.resolve(Response.json(bandung));
      },
    });
    const { data, error } = await client.GET("/v1/regions/{code}", {
      params: { path: { code: "32.73" } },
    });
    expect(error).toBeUndefined();
    expect(data?.kind).toBe("kota");
    expect(calls).toEqual(["https://api.test/v1/regions/32.73"]);
  });

  it("mengembalikan Problem Details sebagai error", async () => {
    const client = createClient({
      baseUrl: "https://api.test",
      fetch: () =>
        Promise.resolve(
          new Response(
            JSON.stringify({ type: "about:blank", title: "Tidak ditemukan", status: 404 }),
            {
              status: 404,
              headers: { "content-type": "application/problem+json" },
            },
          ),
        ),
    });
    const { data, error } = await client.GET("/v1/regions/{code}", {
      params: { path: { code: "32.99" } },
    });
    expect(data).toBeUndefined();
    expect(error?.status).toBe(404);
  });
});

describe("feed kejadian", () => {
  it("parameter kind berulang diserialisasi sebagai kunci berulang", async () => {
    const urls: string[] = [];
    const client = createClient({
      baseUrl: "https://api.test",
      fetch: (input: Request) => {
        urls.push(input.url);
        return Promise.resolve(
          Response.json({ items: [], next_cursor: null, generated_at: "2026-11-24T00:00:00Z" }),
        );
      },
    });
    const { data } = await client.GET("/v1/hazards", {
      params: { query: { kind: ["quake", "weather"], min_level: 2 } },
    });
    expect(data?.items).toEqual([]);
    expect(urls).toEqual(["https://api.test/v1/hazards?kind=quake&kind=weather&min_level=2"]);
  });
});

describe("status sumber", () => {
  it("daftar status bertipe, waktu boleh null", async () => {
    const list: SourceList = {
      generated_at: "2026-11-24T15:00:00Z",
      items: [
        {
          id: "bmkg-autogempa",
          provider: "bmkg",
          dataset: "quake",
          hazard_kinds: ["quake"],
          state: "late",
          interval_seconds: 30,
          last_attempt_at: "2026-11-24T14:59:30Z",
          last_success_at: null,
          last_change_at: null,
          late_after: null,
          consecutive_failures: 4,
          failing_since: "2026-11-24T14:58:00Z",
        },
      ],
    };
    const client = createClient({
      baseUrl: "https://api.test",
      fetch: () => Promise.resolve(Response.json(list)),
    });
    const { data } = await client.GET("/v1/sources");
    expect(data?.items[0]?.state).toBe("late");
    expect(data?.items[0]?.last_success_at).toBeNull();
  });
});
