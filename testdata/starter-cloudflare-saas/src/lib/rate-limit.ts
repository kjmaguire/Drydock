// Fixed-window rate limiter backed by Workers KV (the "KV feature" of this starter).
//
// CAVEAT: KV is eventually consistent and read-modify-write is not atomic, so
// this is a best-effort brake against casual abuse, not a security boundary.
// For hard limits use the Workers Rate Limiting binding or a Durable Object.

export interface RateLimitResult {
  allowed: boolean;
  limit: number;
  remaining: number;
  /** Seconds until the current window ends. */
  retryAfterSeconds: number;
}

export async function hitRateLimit(
  kv: KVNamespace,
  key: string,
  options: { limit: number; windowSeconds: number },
  nowMs: number = Date.now(),
): Promise<RateLimitResult> {
  const nowSeconds = Math.floor(nowMs / 1000);
  const window = Math.floor(nowSeconds / options.windowSeconds);
  const retryAfterSeconds = (window + 1) * options.windowSeconds - nowSeconds;
  const kvKey = `rl:${key}:${window}`;

  const current = Number((await kv.get(kvKey)) ?? "0");
  if (current >= options.limit) {
    return { allowed: false, limit: options.limit, remaining: 0, retryAfterSeconds };
  }
  // KV requires expirationTtl >= 60.
  await kv.put(kvKey, String(current + 1), { expirationTtl: Math.max(60, options.windowSeconds * 2) });
  return { allowed: true, limit: options.limit, remaining: options.limit - current - 1, retryAfterSeconds };
}
