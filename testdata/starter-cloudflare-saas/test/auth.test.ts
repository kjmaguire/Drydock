import { describe, expect, it } from "vitest";
import { devLoginEnabled } from "../src/lib/auth";
import { api, env, json, login, postJson } from "./helpers";

describe("auth stubs", () => {
  it("POST /api/auth/login accepts any email in dev and sets an HttpOnly session cookie", async () => {
    const response = await postJson("/api/auth/login", { email: "  Alice@Example.COM " });
    expect(response.status).toBe(200);
    const body = await json<{ user: { email: string }; stub: boolean }>(response);
    expect(body.user.email).toBe("alice@example.com");
    expect(body.stub).toBe(true);
    const cookie = response.headers.get("Set-Cookie") ?? "";
    expect(cookie).toMatch(/^session=[\w-]+\.[\w-]+;/);
    expect(cookie).toContain("HttpOnly");
    expect(cookie).toContain("SameSite=Lax");
  });

  it("logging in twice with the same email reuses the same user", async () => {
    const first = await json<{ user: { id: string } }>(await postJson("/api/auth/login", { email: "same@example.com" }));
    const second = await json<{ user: { id: string } }>(await postJson("/api/auth/login", { email: "same@example.com" }));
    expect(second.user.id).toBe(first.user.id);
  });

  it("rejects missing or malformed emails with 400", async () => {
    for (const body of [{}, { email: "not-an-email" }, { email: 42 }]) {
      const response = await postJson("/api/auth/login", body);
      expect(response.status, JSON.stringify(body)).toBe(400);
      expect((await json<{ error: { code: string } }>(response)).error.code).toBe("invalid_email");
    }
  });

  it("GET /api/auth/me returns the user for a valid cookie and null otherwise", async () => {
    const cookie = await login("me@example.com");
    const me = await json<{ user: { email: string } | null }>(await api("/api/auth/me", {}, cookie));
    expect(me.user?.email).toBe("me@example.com");
    const anonymous = await json<{ user: unknown }>(await api("/api/auth/me"));
    expect(anonymous.user).toBeNull();
    const forged = await json<{ user: unknown }>(await api("/api/auth/me", {}, "session=eyJhIjoxfQ.AAAA"));
    expect(forged.user).toBeNull();
  });

  it("POST /api/auth/logout clears the cookie", async () => {
    const response = await api("/api/auth/logout", { method: "POST" });
    expect(response.status).toBe(204);
    expect(response.headers.get("Set-Cookie")).toContain("Max-Age=0");
  });

  it("dev login is gated: needs ALLOW_DEV_LOGIN=true and a non-production environment", () => {
    expect(devLoginEnabled({ ALLOW_DEV_LOGIN: "true", ENVIRONMENT: "development" })).toBe(true);
    expect(devLoginEnabled({ ALLOW_DEV_LOGIN: "false", ENVIRONMENT: "development" })).toBe(false);
    expect(devLoginEnabled({ ALLOW_DEV_LOGIN: "true", ENVIRONMENT: "production" })).toBe(false);
    expect(devLoginEnabled({ ALLOW_DEV_LOGIN: "", ENVIRONMENT: "preview" })).toBe(false);
  });

  it("the Worker in this test run has dev login enabled only because the test config says so", () => {
    expect(env.ALLOW_DEV_LOGIN).toBe("true");
    expect(env.ENVIRONMENT).toBe("test");
  });
});
