import { Hono } from "hono";
import { authenticate, devLoginEnabled, loginWithEmail, logoutCookie } from "../lib/auth";
import { requireAuth } from "./middleware";
import { type AppEnv, HttpError } from "./types";

export const auth = new Hono<AppEnv>();

// STUB: accepts any email, dev only (see devLoginEnabled in src/lib/auth.ts).
auth.post("/login", async (c) => {
  const body = await c.req.json<{ email?: unknown }>().catch(() => ({}) as { email?: unknown });
  const result = await loginWithEmail(c.env, c.req.raw, body.email);
  if (!result.ok) {
    throw new HttpError(
      result.status,
      result.code,
      result.message,
      result.retryAfterSeconds ? { "Retry-After": String(result.retryAfterSeconds) } : {},
    );
  }
  c.header("Set-Cookie", result.setCookie);
  return c.json({ user: result.user, stub: true });
});

auth.post("/logout", (c) => {
  c.header("Set-Cookie", logoutCookie(c.req.raw));
  return c.body(null, 204);
});

auth.get("/me", async (c) => {
  const user = await authenticate(c.req.raw, c.env);
  return c.json({ user, devLoginEnabled: devLoginEnabled(c.env) });
});

// Example of a guarded route; `requireAuth` is what the items/billing routers use.
auth.get("/session", requireAuth, (c) => c.json({ user: c.get("user") }));
