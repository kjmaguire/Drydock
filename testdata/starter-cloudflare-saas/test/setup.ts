import { applyD1Migrations } from "cloudflare:test";
import { env } from "cloudflare:workers";

// Runs before each test file; migrations are idempotent (tracked in d1_migrations).
await applyD1Migrations(env.DB, env.TEST_MIGRATIONS);
