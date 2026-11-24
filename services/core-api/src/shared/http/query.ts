import { z } from "zod";

import { InvalidParameterError } from "../domain/errors.js";

// Pesan validasi bawaan Zod dalam bahasa Indonesia (aturan proyek: pesan error berbahasa Indonesia).
z.config(z.locales.id());

/** Memvalidasi query/path dengan Zod; galat menjadi 400 dengan rincian per parameter. */
export function parseInput<T extends z.ZodType>(schema: T, input: unknown): z.infer<T> {
  const parsed = schema.safeParse(input ?? {});
  if (!parsed.success) {
    throw new InvalidParameterError(
      parsed.error.issues.map((i) => ({
        parameter: i.path.length > 0 ? i.path.map(String).join(".") : "(query)",
        message: i.message,
      })),
    );
  }
  return parsed.data;
}

/** Nilai query tunggal: bila parameter diulang, ambil yang terakhir. */
export function single(v: unknown): unknown {
  return Array.isArray(v) ? v.at(-1) : v;
}

export const queryString = (max = 200) => z.preprocess(single, z.string().max(max));

export const queryInt = (min: number, max: number) =>
  z
    .preprocess(
      single,
      z
        .string()
        .regex(/^[0-9]{1,7}$/, "harus bilangan bulat")
        .transform(Number),
    )
    .pipe(z.number().int().min(min).max(max));

export const queryBool = z.preprocess(
  single,
  z.enum(["true", "false"]).transform((v) => v === "true"),
);

export const queryDateTime = z.preprocess(
  single,
  z.iso
    .datetime({ offset: true, message: "harus waktu RFC 3339, misal 2026-11-24T10:00:00Z" })
    .transform((v) => new Date(v)),
);

/** Parameter berulang (`kind=a&kind=b`) atau satu nilai, menjadi array. */
export const queryArray = <T extends z.ZodType>(item: T, max: number) =>
  z.preprocess((v: unknown): unknown => (Array.isArray(v) ? v : [v]), z.array(item).max(max));

/** Satu nilai dari daftar tetap (parameter diulang: yang terakhir). */
export const queryEnum = <const T extends readonly [string, ...string[]]>(values: T) =>
  z.preprocess(single, z.enum(values));
