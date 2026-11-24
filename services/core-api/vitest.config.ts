import { defineConfig } from "vitest/config";

// Unit test: domain, use case, dan lapisan HTTP dengan pembaca palsu (tanpa database).
export default defineConfig({
  test: {
    include: ["src/**/*.test.ts"],
    coverage: {
      provider: "v8",
      // Gerbang CLAUDE.md: domain + app >= 85%.
      include: ["src/**/domain/**/*.ts", "src/**/app/**/*.ts"],
      exclude: ["src/**/*.test.ts"],
      thresholds: { lines: 85, statements: 85, functions: 85, branches: 85 },
    },
  },
});
