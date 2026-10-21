import { describe, expect, it } from "vitest";
import { LOGIN_LIMIT } from "../src/lib/auth";
import { hitRateLimit } from "../src/lib/rate-limit";
import { api, env, json, postJson } from "./helpers";

describe("KV rate limiting", () => {
  it("allows up to the limit within a window, then blocks, then resets in the next window", async () => {
    const t0 = 1_800_000_000_000;
    const key = `unit-${crypto.randomUUID()}`;
    const options = { limit: 3, windowSeconds: 60 };
    const results = [];
    for (let i = 0; i < 4; i++) results.push(await hitRateLimit(env.KV, key, options, t0));
    expect(results.map((r) => r.allowed)).toEqual([true, true, true, false]);
    expect(results.map((r) => r.remaining)).toEqual([2, 1, 0, 0]);
    expect(results[3]?.retryAfterSeconds).toBeGreaterThan(0);
    expect((await hitRateLimit(env.KV, key, options, t0 + 61_000)).allowed).toBe(true);
  });

  it("POST /api/auth/login returns 429 with Retry-After once a client exceeds the limit", async () => {
    const headers = { "CF-Connecting-IP": "203.0.113.77", "Content-Type": "application/json" };
    const attempt = () =>
      api("/api/auth/login", { method: "POST", headers, body: JSON.stringify({ email: "burst@example.com" }) });
    for (let i = 0; i < LOGIN_LIMIT; i++) expect((await attempt()).status).toBe(200);
    const blocked = await attempt();
    expect(blocked.status).toBe(429);
    expect(Number(blocked.headers.get("Retry-After"))).toBeGreaterThan(0);
    expect((await json<{ error: { code: string } }>(blocked)).error.code).toBe("rate_limited");
    // A different client is unaffected.
    expect((await postJson("/api/auth/login", { email: "other@example.com" })).status).toBe(200);
  });
});
