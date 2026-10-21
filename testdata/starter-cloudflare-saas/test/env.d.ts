// Adds the test-only TEST_MIGRATIONS binding (injected by vitest.config.ts) to Env.
declare namespace Cloudflare {
  interface Env {
    TEST_MIGRATIONS: import("cloudflare:test").D1Migration[];
  }
}
