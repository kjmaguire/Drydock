import { describe, expect, it } from "vitest";
import { app } from "../src/api/app";
import { env } from "./helpers";

// Calls the Hono app directly with a modified env to prove the gate holds.
function loginWith(overrides: Partial<Env>): Promise<Response> {
  const request = new Request("http://example.com/api/auth/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email: "gate@example.com" }),
  });
  return Promise.resolve(app.fetch(request, { ...env, ...overrides } as Env));
}

describe("dev login gate", () => {
  it("returns 403 and no cookie when ALLOW_DEV_LOGIN is not true", async () => {
    const response = await loginWith({ ALLOW_DEV_LOGIN: "false" });
    expect(response.status).toBe(403);
    expect(response.headers.get("Set-Cookie")).toBeNull();
    expect(((await response.json()) as { error: { code: string } }).error.code).toBe("dev_login_disabled");
  });

  it("returns 403 in production even if ALLOW_DEV_LOGIN is accidentally true", async () => {
    const response = await loginWith({ ALLOW_DEV_LOGIN: "true", ENVIRONMENT: "production" });
    expect(response.status).toBe(403);
    expect(response.headers.get("Set-Cookie")).toBeNull();
  });
});
