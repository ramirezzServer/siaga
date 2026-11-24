import { describe, expect, it } from "vitest";
import { z } from "zod";

import { decodeCursor, decodeOffset, encodeCursor, encodeOffset } from "./cursor.js";
import { InvalidParameterError } from "./errors.js";

const schema = z.object({ c: z.string() });

describe("kursor", () => {
  it("bolak-balik encode/decode", () => {
    expect(decodeCursor(encodeCursor({ c: "32.73" }), schema)).toEqual({ c: "32.73" });
    expect(decodeOffset(encodeOffset(150))).toBe(150);
    expect(decodeOffset(undefined)).toBe(0);
  });

  it.each([
    ["karakter di luar base64url", "abc$%"],
    ["bukan JSON", Buffer.from("halo").toString("base64url")],
    ["JSON bukan objek", Buffer.from("123").toString("base64url")],
    ["versi lain", Buffer.from(JSON.stringify({ v: 2, c: "x" })).toString("base64url")],
    ["isi tidak sesuai schema", encodeCursor({ c: 5 })],
    ["terlalu panjang", "a".repeat(201)],
  ])("menolak kursor %s dengan 400 cursor", (_, cursor) => {
    try {
      decodeCursor(cursor, schema);
      expect.unreachable();
    } catch (err) {
      expect(err).toBeInstanceOf(InvalidParameterError);
      expect((err as InvalidParameterError).issues[0]?.parameter).toBe("cursor");
    }
  });

  it("menolak offset negatif", () => {
    expect(() => decodeOffset(encodeCursor({ o: -1 }))).toThrow(InvalidParameterError);
  });
});
