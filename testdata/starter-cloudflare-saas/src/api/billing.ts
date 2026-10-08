import { Hono } from "hono";
import { requireAuth } from "./middleware";
import { type AppEnv, HttpError } from "./types";

// ===========================================================================
// STUB: BILLING. No payment provider is connected and no money moves.
//
// These endpoints exist so the product has the right shape (route names,
// payload, auth) for an agent to replace with a real provider such as Stripe
// Checkout. Responses are labelled `stub: true` and carry `X-Stub: billing`.
// ===========================================================================

export const PLANS = ["pro", "team"] as const;
export type Plan = (typeof PLANS)[number];

export const billing = new Hono<AppEnv>();

billing.use("*", requireAuth);
billing.use("*", async (c, next) => {
  await next();
  c.header("X-Stub", "billing");
});

/**
 * POST /api/billing/checkout   body: { "plan": "pro" | "team" }
 *
 * Stub payload (documented contract for the real implementation to keep):
 *   { stub: true, notice, provider: "stub", plan, checkoutSessionId, checkoutUrl, expiresAt }
 * `checkoutUrl` points at the reserved `.invalid` TLD so it can never resolve.
 */
billing.post("/checkout", async (c) => {
  const body = await c.req.json<{ plan?: unknown }>().catch(() => ({}) as { plan?: unknown });
  const plan = PLANS.find((p) => p === body.plan);
  if (!plan) throw new HttpError(400, "invalid_plan", `plan must be one of: ${PLANS.join(", ")}`);

  const checkoutSessionId = `cs_stub_${crypto.randomUUID()}`;
  return c.json({
    stub: true,
    notice: "STUB: no payment provider is connected. Replace src/api/billing.ts before charging anyone.",
    provider: "stub",
    plan,
    checkoutSessionId,
    checkoutUrl: `https://checkout.stub.invalid/session/${checkoutSessionId}`,
    expiresAt: new Date(Date.now() + 30 * 60 * 1000).toISOString(),
  });
});

/** GET /api/billing/status: stub subscription state; everyone is on the free plan. */
billing.get("/status", (c) =>
  c.json({ stub: true, notice: "STUB: no payment provider is connected.", plan: "free", status: "none" }),
);
