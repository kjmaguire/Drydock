// Signed session tokens using only Web Crypto (HMAC-SHA256). No dependencies.
//
// Token format:  base64url(JSON payload) + "." + base64url(HMAC(payload part))
// This is a minimal, stateless session. It has no revocation list; keep
// `ttlSeconds` short and rotate SESSION_SECRET to invalidate every session.

export interface SessionPayload {
  /** User id (users.id). */
  sub: string;
  email: string;
  /** Expiry, seconds since the Unix epoch. */
  exp: number;
}

export const SESSION_COOKIE = "session";
export const SESSION_TTL_SECONDS = 60 * 60 * 24 * 7;

const encoder = new TextEncoder();
const decoder = new TextDecoder();

function toBase64Url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
}

function fromBase64Url(value: string): Uint8Array<ArrayBuffer> | null {
  if (!/^[A-Za-z0-9_-]*$/.test(value)) return null;
  const padded = value.replaceAll("-", "+").replaceAll("_", "/").padEnd(Math.ceil(value.length / 4) * 4, "=");
  try {
    const binary = atob(padded);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    return bytes;
  } catch {
    return null;
  }
}

async function importKey(secret: string, usage: "sign" | "verify"): Promise<CryptoKey> {
  if (secret.length < 16) {
    throw new Error("SESSION_SECRET must be at least 16 characters");
  }
  return crypto.subtle.importKey("raw", encoder.encode(secret), { name: "HMAC", hash: "SHA-256" }, false, [usage]);
}

export async function signSession(payload: SessionPayload, secret: string): Promise<string> {
  const body = toBase64Url(encoder.encode(JSON.stringify(payload)));
  const key = await importKey(secret, "sign");
  const signature = await crypto.subtle.sign("HMAC", key, encoder.encode(body));
  return `${body}.${toBase64Url(new Uint8Array(signature))}`;
}

/** Returns the payload if the signature is valid and the token has not expired, otherwise null. */
export async function verifySession(
  token: string,
  secret: string,
  nowSeconds: number = Math.floor(Date.now() / 1000),
): Promise<SessionPayload | null> {
  const parts = token.split(".");
  if (parts.length !== 2) return null;
  const [body, signature] = parts as [string, string];
  const signatureBytes = fromBase64Url(signature);
  const bodyBytes = fromBase64Url(body);
  if (!signatureBytes || !bodyBytes) return null;

  const key = await importKey(secret, "verify");
  // crypto.subtle.verify compares in constant time.
  const valid = await crypto.subtle.verify("HMAC", key, signatureBytes, encoder.encode(body));
  if (!valid) return null;

  try {
    const payload = JSON.parse(decoder.decode(bodyBytes)) as Partial<SessionPayload>;
    if (typeof payload.sub !== "string" || typeof payload.email !== "string" || typeof payload.exp !== "number") {
      return null;
    }
    if (payload.exp <= nowSeconds) return null;
    return { sub: payload.sub, email: payload.email, exp: payload.exp };
  } catch {
    return null;
  }
}

export function readCookie(header: string | null | undefined, name: string): string | null {
  if (!header) return null;
  for (const part of header.split(";")) {
    const index = part.indexOf("=");
    if (index === -1) continue;
    if (part.slice(0, index).trim() === name) return part.slice(index + 1).trim();
  }
  return null;
}

export function sessionCookie(token: string, options: { secure: boolean; maxAge?: number }): string {
  const attributes = [
    `${SESSION_COOKIE}=${token}`,
    "Path=/",
    "HttpOnly",
    "SameSite=Lax",
    `Max-Age=${options.maxAge ?? SESSION_TTL_SECONDS}`,
  ];
  if (options.secure) attributes.push("Secure");
  return attributes.join("; ");
}

export function clearSessionCookie(secure: boolean): string {
  return sessionCookie("", { secure, maxAge: 0 });
}
