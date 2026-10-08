import { fileURLToPath } from "node:url";
import { cloudflareTest, readD1Migrations } from "@cloudflare/vitest-plugin";
import { defineConfig } from "vitest/config";

// Tests run inside real workerd (via Miniflare) with real D1 / KV / R2 simulators.
// `main` points at the API-only entry so the React Router build is not needed.
// Bindings (DB, KV, ATTACHMENTS) come from wrangler.jsonc, so tests cannot drift
// from the deployed shape; the values below are test-only overrides.
export default defineConfig(async () => {
  const migrations = await readD1Migrations(fileURLToPath(new URL("./migrations", import.meta.url)));
  return {
    plugins: [
      cloudflareTest({
        main: "./src/api/index.ts",
        wrangler: { configPath: "./wrangler.jsonc" },
        miniflare: {
          bindings: {
            TEST_MIGRATIONS: migrations,
            ENVIRONMENT: "test",
            ALLOW_DEV_LOGIN: "true",
            SESSION_SECRET: "test-only-session-secret-0123456789",
          },
        },
      }),
    ],
    test: {
      include: ["test/**/*.test.ts"],
      setupFiles: ["./test/setup.ts"],
    },
  };
});
