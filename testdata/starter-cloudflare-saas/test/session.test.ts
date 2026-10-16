import { describe, expect, it } from "vitest";
import { readCookie, SESSION_COOKIE, sessionCookie, signSession, verifySession } from "../src/lib/session";

const SECRET = "unit-test-secret-0123456789";
const now = 1_800_000_000;

describe("signed session tokens (Web Crypto HMAC)", () => {
  it("round-trips a payload", async () => {
    const token = await signSession({ sub: "u1", email: "a@example.com", exp: now + 60 }, SECRET);
    expect(await verifySession(token, SECRET, now)).toEqual({ sub: "u1", email: "a@example.com", exp: now + 60 });
  });

  it("rejects a token signed with a different secret", async () => {
    const token = await signSession({ sub: "u1", email: "a@example.com", exp: now + 60 }, SECRET);
    expect(await verifySession(token, "another-secret-0123456789", now)).toBeNull();
  });

  it("rejects a token whose payload was tampered with", async () => {
    const token = await signSession({ sub: "u1", email: "a@example.com", exp: now + 60 }, SECRET);
    const [, signature] = token.split(".");
    const forged = btoa(JSON.stringify({ sub: "admin", email: "a@example.com", exp: now + 60 }))
      .replaceAll("+", "-")
      .replaceAll("/", "_")
      .replaceAll("=", "");
    expect(await verifySession(`${forged}.${signature}`, SECRET, now)).toBeNull();
  });

  it("rejects expired and malformed tokens", async () => {
    const token = await signSession({ sub: "u1", email: "a@example.com", exp: now - 1 }, SECRET);
    expect(await verifySession(token, SECRET, now)).toBeNull();
    for (const bad of ["", "abc", "a.b.c", "!!!.???"]) {
      expect(await verifySession(bad, SECRET, now)).toBeNull();
    }
  });

  it("refuses to sign with a short secret", async () => {
    await expect(signSession({ sub: "u", email: "a@b.co", exp: now }, "short")).rejects.toThrow(/SESSION_SECRET/);
  });

  it("builds HttpOnly SameSite cookies, adding Secure only over https", () => {
    expect(sessionCookie("tok", { secure: false })).toBe(`${SESSION_COOKIE}=tok; Path=/; HttpOnly; SameSite=Lax; Max-Age=604800`);
    expect(sessionCookie("tok", { secure: true })).toContain("; Secure");
    expect(readCookie("a=1; session=tok; b=2", "session")).toBe("tok");
    expect(readCookie(null, "session")).toBeNull();
  });
});
