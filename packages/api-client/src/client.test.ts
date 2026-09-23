import { describe, expect, it } from "vitest";

import { createClient, type Region } from "./index.js";

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
