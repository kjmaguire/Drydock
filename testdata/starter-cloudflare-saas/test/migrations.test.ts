import { applyD1Migrations } from "cloudflare:test";
import { describe, expect, it } from "vitest";
import { env } from "./helpers";

describe("D1 migrations", () => {
  it("0001_init creates the users and items tables", async () => {
    const { results } = await env.DB.prepare(
      "SELECT name FROM sqlite_master WHERE type = 'table' AND name IN ('users', 'items') ORDER BY name",
    ).all<{
      name: string;
    }>();
    expect(results.map((row) => row.name)).toEqual(["items", "users"]);
  });

  it("records the migration and is idempotent when applied again", async () => {
    await applyD1Migrations(env.DB, env.TEST_MIGRATIONS);
    const { results } = await env.DB.prepare("SELECT name FROM d1_migrations").all<{ name: string }>();
    expect(results.map((row) => row.name)).toEqual(["0001_init.sql"]);
  });

  it("enforces constraints: unique email, title length, done flag, and the user foreign key", async () => {
    await env.DB.prepare("INSERT INTO users (id, email) VALUES ('u-constraints', 'constraints@example.com')").run();
    await expect(
      env.DB.prepare("INSERT INTO users (id, email) VALUES ('u-dup', 'constraints@example.com')").run(),
    ).rejects.toThrow(/UNIQUE/i);
    await expect(env.DB.prepare("INSERT INTO items (user_id, title) VALUES ('u-constraints', '')").run()).rejects.toThrow(
      /CHECK/i,
    );
    await expect(
      env.DB.prepare("INSERT INTO items (user_id, title, done) VALUES ('u-constraints', 'x', 2)").run(),
    ).rejects.toThrow(/CHECK/i);
    await expect(env.DB.prepare("INSERT INTO items (user_id, title) VALUES ('no-such-user', 'x')").run()).rejects.toThrow(
      /FOREIGN KEY/i,
    );
  });
});
