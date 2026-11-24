import { defineConfig } from "vitest/config";

// Test integrasi dengan PostgreSQL asli (make test-integration). Berkas berbagi
// database uji, jadi dijalankan berurutan.
export default defineConfig({
  test: {
    include: ["test/**/*.int.test.ts"],
    fileParallelism: false,
    testTimeout: 30_000,
    hookTimeout: 60_000,
  },
});
