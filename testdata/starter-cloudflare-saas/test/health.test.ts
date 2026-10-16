import { describe, expect, it } from "vitest";
import { api, json } from "./helpers";

describe("health and routing", () => {
  it("GET /api/health reports ok with a working database", async () => {
    const response = await api("/api/health");
    expect(response.status).toBe(200);
    expect(await json(response)).toEqual({ status: "ok", environment: "test", database: true });
    expect(response.headers.get("Cache-Control")).toBe("no-store");
  });

  it("unknown API routes return a JSON 404 with an error code", async () => {
    const response = await api("/api/nope");
    expect(response.status).toBe(404);
    expect(await json(response)).toEqual({ error: { code: "not_found", message: "Not found." } });
  });
});
