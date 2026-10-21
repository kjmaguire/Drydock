import { describe, expect, it } from "vitest";
import { api, json, login, postJson } from "./helpers";

describe("billing stub", () => {
  it("requires authentication", async () => {
    expect((await postJson("/api/billing/checkout", { plan: "pro" })).status).toBe(401);
    expect((await api("/api/billing/status")).status).toBe(401);
  });

  it("POST /api/billing/checkout returns the documented, clearly labelled stub payload", async () => {
    const cookie = await login("billing@example.com");
    const response = await postJson("/api/billing/checkout", { plan: "pro" }, cookie);
    expect(response.status).toBe(200);
    expect(response.headers.get("X-Stub")).toBe("billing");
    const body = await json<Record<string, unknown>>(response);
    expect(body).toMatchObject({ stub: true, provider: "stub", plan: "pro" });
    expect(String(body.notice)).toMatch(/^STUB:/);
    expect(String(body.checkoutSessionId)).toMatch(/^cs_stub_/);
    expect(String(body.checkoutUrl)).toMatch(/^https:\/\/checkout\.stub\.invalid\//);
    expect(Number.isNaN(Date.parse(String(body.expiresAt)))).toBe(false);
  });

  it("rejects unknown plans", async () => {
    const cookie = await login("billing-bad@example.com");
    const response = await postJson("/api/billing/checkout", { plan: "enterprise-gold" }, cookie);
    expect(response.status).toBe(400);
    expect((await json<{ error: { code: string } }>(response)).error.code).toBe("invalid_plan");
  });

  it("GET /api/billing/status is a labelled stub on the free plan", async () => {
    const cookie = await login("billing-status@example.com");
    const response = await api("/api/billing/status", {}, cookie);
    expect(response.headers.get("X-Stub")).toBe("billing");
    expect(await json(response)).toMatchObject({ stub: true, plan: "free" });
  });
});
