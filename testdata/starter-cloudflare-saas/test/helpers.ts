import { env, exports } from "cloudflare:workers";

type Json = Record<string, unknown>;

export function api(path: string, init: RequestInit = {}, cookie?: string): Promise<Response> {
  const headers = new Headers(init.headers);
  if (cookie) headers.set("Cookie", cookie);
  return exports.default.fetch(new Request(`http://example.com${path}`, { ...init, headers }));
}

export function postJson(path: string, body: unknown, cookie?: string, method = "POST"): Promise<Response> {
  return api(path, { method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }, cookie);
}

/** Signs in through the real login endpoint and returns the Cookie header value. */
export async function login(email: string): Promise<string> {
  // A unique client IP per login keeps these helper calls out of the KV rate limit.
  const response = await api("/api/auth/login", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "CF-Connecting-IP": `198.51.100.${Math.floor(Math.random() * 250) + 1}-${crypto.randomUUID()}`,
    },
    body: JSON.stringify({ email }),
  });
  if (response.status !== 200) throw new Error(`login failed with ${response.status}`);
  const setCookie = response.headers.get("Set-Cookie") ?? "";
  const cookie = setCookie.split(";")[0];
  if (!cookie) throw new Error("login did not set a cookie");
  return cookie;
}

export async function json<T = Json>(response: Response): Promise<T> {
  return (await response.json()) as T;
}

export { env };
