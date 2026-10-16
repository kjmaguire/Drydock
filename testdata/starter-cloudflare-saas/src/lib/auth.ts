import { hitRateLimit } from "./rate-limit";
import {
  clearSessionCookie,
  readCookie,
  SESSION_COOKIE,
  SESSION_TTL_SECONDS,
  sessionCookie,
  signSession,
  verifySession,
} from "./session";

export interface SessionUser {
  id: string;
  email: string;
}

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
export const LOGIN_LIMIT = 10;
export const LOGIN_WINDOW_SECONDS = 60;

/**
 * STUB AUTH. The only way to sign in is the dev login, which accepts ANY email
 * and therefore must never be reachable in production. It requires BOTH an
 * explicit opt-in (ALLOW_DEV_LOGIN="true", set via .dev.vars) AND an
 * environment that is not "production". Replace this module with a real
 * identity provider before shipping.
 */
export function devLoginEnabled(env: Pick<Env, "ALLOW_DEV_LOGIN" | "ENVIRONMENT">): boolean {
  return env.ALLOW_DEV_LOGIN === "true" && env.ENVIRONMENT !== "production";
}

export function isSecureRequest(request: Request): boolean {
  return new URL(request.url).protocol === "https:";
}

export function normalizeEmail(input: unknown): string | null {
  if (typeof input !== "string") return null;
  const email = input.trim().toLowerCase();
  if (email.length > 254 || !EMAIL_PATTERN.test(email)) return null;
  return email;
}

/** Returns the signed-in user from the session cookie, or null. */
export async function authenticate(request: Request, env: Env): Promise<SessionUser | null> {
  const token = readCookie(request.headers.get("Cookie"), SESSION_COOKIE);
  if (!token) return null;
  const payload = await verifySession(token, env.SESSION_SECRET);
  return payload ? { id: payload.sub, email: payload.email } : null;
}

export type LoginResult =
  | { ok: true; user: SessionUser; setCookie: string }
  | { ok: false; status: 400 | 403 | 429; code: string; message: string; retryAfterSeconds?: number };

/** Shared by POST /api/auth/login and the /login page action. */
export async function loginWithEmail(env: Env, request: Request, emailInput: unknown): Promise<LoginResult> {
  if (!devLoginEnabled(env)) {
    return { ok: false, status: 403, code: "dev_login_disabled", message: "Dev login is disabled in this environment." };
  }

  const client = request.headers.get("CF-Connecting-IP") ?? "local";
  const limit = await hitRateLimit(env.KV, `login:${client}`, { limit: LOGIN_LIMIT, windowSeconds: LOGIN_WINDOW_SECONDS });
  if (!limit.allowed) {
    return {
      ok: false,
      status: 429,
      code: "rate_limited",
      message: "Too many login attempts. Try again later.",
      retryAfterSeconds: limit.retryAfterSeconds,
    };
  }

  const email = normalizeEmail(emailInput);
  if (!email) {
    return { ok: false, status: 400, code: "invalid_email", message: "A valid email address is required." };
  }

  const row = await env.DB.prepare(
    `INSERT INTO users (id, email) VALUES (?1, ?2)
     ON CONFLICT (email) DO UPDATE SET email = excluded.email
     RETURNING id, email`,
  )
    .bind(crypto.randomUUID(), email)
    .first<{ id: string; email: string }>();
  if (!row) throw new Error("user upsert returned no row");

  const exp = Math.floor(Date.now() / 1000) + SESSION_TTL_SECONDS;
  const token = await signSession({ sub: row.id, email: row.email, exp }, env.SESSION_SECRET);
  return {
    ok: true,
    user: { id: row.id, email: row.email },
    setCookie: sessionCookie(token, { secure: isSecureRequest(request) }),
  };
}

export function logoutCookie(request: Request): string {
  return clearSessionCookie(isSecureRequest(request));
}
