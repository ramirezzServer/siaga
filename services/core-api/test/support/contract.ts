import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { Ajv2020 } from "ajv/dist/2020.js";
import addFormats from "ajv-formats";
import { parse } from "yaml";

/** Kontrak OpenAPI yang sama dengan yang dipakai generator klien. */
export const openapiPath = fileURLToPath(
  new URL("../../../../contracts/openapi/core-api.yaml", import.meta.url),
);

interface OpenApiDoc {
  paths: Record<string, Record<string, unknown>>;
  components: { schemas: Record<string, unknown> };
}

export const openapi = parse(readFileSync(openapiPath, "utf8")) as OpenApiDoc;

// OpenAPI 3.1 = JSON Schema 2020-12, jadi Ajv2020 bisa memvalidasi langsung
// terhadap components.schemas di dokumen kontrak.
const ajv = new Ajv2020({ strict: false, allErrors: true });
addFormats.default(ajv);
ajv.addSchema(openapi, "core-api");

/** Memastikan body sesuai schema kontrak; melempar galat berisi rinciannya bila tidak. */
export function expectSchema(name: string, body: unknown): void {
  const validate = ajv.getSchema(`core-api#/components/schemas/${name}`);
  if (!validate) {
    throw new Error(`schema ${name} tidak ada di kontrak`);
  }
  if (!validate(body)) {
    throw new Error(
      `body tidak sesuai schema ${name}: ${ajv.errorsText(validate.errors, { separator: "\n" })}`,
    );
  }
}

/** Semua operasi GET di kontrak sebagai pola rute Fastify (`{id}` menjadi `:id`). */
export function contractRoutes(): string[] {
  return Object.entries(openapi.paths)
    .filter(([, ops]) => "get" in ops)
    .map(([path]) => path.replace(/\{([^}]+)\}/g, ":$1"));
}
