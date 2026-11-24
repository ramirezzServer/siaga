import { readFileSync } from "node:fs";

import { describe, expect, it } from "vitest";
import { z } from "zod";

import { loadConfig } from "../config.js";
import { isUnavailable, parseRows, RowShapeError } from "./adapters/postgres.js";
import { InvalidParameterError } from "./domain/errors.js";
import { toProblem } from "./http/problem.js";

describe("isUnavailable", () => {
  it.each([
    [{ code: "ECONNREFUSED" }, true],
    [{ code: "57014" }, true],
    [{ code: "57P03" }, true],
    [{ code: "53300" }, true],
    [{ code: "08006" }, true],
    [new Error("timeout exceeded when trying to connect"), true],
    [{ code: "42501" }, false],
    [{ code: "23505" }, false],
    [new Error("lain"), false],
    [null, false],
    ["teks", false],
  ])("%j -> %s", (err, want) => {
    expect(isUnavailable(err)).toBe(want);
  });
});

describe("parseRows", () => {
  it("baris yang tidak sesuai menjadi RowShapeError berisi path", () => {
    const schema = z.object({ a: z.number() });
    expect(parseRows("t", schema, [{ a: 1 }])).toEqual([{ a: 1 }]);
    expect(() => parseRows("t", schema, [{ a: "x" }])).toThrow(RowShapeError);
    expect(() => parseRows("t", schema, [{ a: "x" }])).toThrow(/a:/);
  });
});

describe("toProblem", () => {
  it("validasi membawa rincian", () => {
    expect(toProblem(InvalidParameterError.of("limit", "terlalu besar"), "/x")).toMatchObject({
      status: 400,
      errors: [{ parameter: "limit", message: "terlalu besar" }],
    });
  });

  it("izin ditolak database (42501) adalah 500, bukan 503", () => {
    expect(toProblem({ code: "42501" }, "/x").status).toBe(500);
  });
});

describe("loadConfig", () => {
  it("bawaan dan daftar origin", () => {
    const c = loadConfig({
      DATABASE_URL: "postgres://u:p@h/db",
      CORS_ORIGINS: "https://a.example, https://b.example",
    });
    expect(c).toMatchObject({
      PORT: 8083,
      CORS_ORIGINS: ["https://a.example", "https://b.example"],
      LOG_FORMAT: "json",
    });
    expect(loadConfig({ DATABASE_URL: "postgres://u:p@h/db" }).CORS_ORIGINS).toEqual([]);
  });

  it("menerima semua variabel .env.example (Makefile mengekspor .env ke core-api)", () => {
    const example = readFileSync(new URL("../../../../.env.example", import.meta.url), "utf8");
    const env: Record<string, string> = {};
    for (const line of example.split("\n")) {
      const m = /^([A-Z][A-Z0-9_]*)=(.*)$/.exec(line);
      if (m?.[1] !== undefined && m[2] !== undefined) env[m[1]] = m[2];
    }
    expect(env["LOG_LEVEL"]).toBeDefined();
    expect(loadConfig({ ...env, DATABASE_URL: "postgres://u:p@h/db" }).LOG_LEVEL).toBe(
      env["LOG_LEVEL"],
    );
  });

  it("galat menyebut variabelnya", () => {
    expect(() => loadConfig({ DATABASE_URL: "mysql://x", PORT: "0" })).toThrow(
      /DATABASE_URL.*PORT|PORT.*DATABASE_URL/,
    );
    expect(() => loadConfig({ DATABASE_URL: "postgres://x", CORS_ORIGINS: "bukan-url" })).toThrow(
      /CORS_ORIGINS/,
    );
  });
});
