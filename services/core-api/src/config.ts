import { z } from "zod";

/** Konfigurasi dari environment (12-factor). Galat konfigurasi menghentikan start. */
const schema = z.object({
  DATABASE_URL: z.string().regex(/^postgres(ql)?:\/\//, "harus URL postgres://"),
  PORT: z.coerce.number().int().min(1).max(65535).default(8083),
  HOST: z.string().default("0.0.0.0"),
  // Origin web yang boleh memanggil API dari browser, dipisah koma. Kosong = CORS mati.
  CORS_ORIGINS: z
    .string()
    .default("")
    .transform((s) =>
      s
        .split(",")
        .map((o) => o.trim())
        .filter((o) => o !== ""),
    )
    .pipe(z.array(z.url())),
  DB_POOL_MAX: z.coerce.number().int().min(1).max(50).default(10),
  DB_STATEMENT_TIMEOUT_MS: z.coerce.number().int().min(100).max(60_000).default(5_000),
  LOG_LEVEL: z.enum(["error", "warn", "log", "debug"]).default("log"),
  LOG_FORMAT: z.enum(["json", "text"]).default("json"),
  SIAGA_VERSION: z.string().default("dev"),
});

export type Config = z.infer<typeof schema>;

export function loadConfig(env: Record<string, string | undefined>): Config {
  const parsed = schema.safeParse(env);
  if (!parsed.success) {
    const issues = parsed.error.issues.map((i) => `${i.path.join(".")}: ${i.message}`).join("; ");
    throw new Error(`konfigurasi core-api tidak valid: ${issues}`);
  }
  return parsed.data;
}
