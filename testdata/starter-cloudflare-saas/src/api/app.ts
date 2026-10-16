import { Hono } from "hono";
import { auth } from "./auth";
import { billing } from "./billing";
import { items } from "./items";
import { type AppEnv, HttpError } from "./types";

export const app = new Hono<AppEnv>();

app.use("/api/*", async (c, next) => {
  await next();
  c.header("Cache-Control", "no-store");
});

app.get("/api/health", async (c) => {
  let database = false;
  try {
    database = (await c.env.DB.prepare("SELECT 1 AS ok").first<{ ok: number }>())?.ok === 1;
  } catch {
    database = false;
  }
  return c.json({ status: database ? "ok" : "degraded", environment: c.env.ENVIRONMENT, database }, database ? 200 : 503);
});

app.route("/api/auth", auth);
app.route("/api/items", items);
app.route("/api/billing", billing);

app.notFound((c) => c.json({ error: { code: "not_found", message: "Not found." } }, 404));

app.onError((error, c) => {
  if (error instanceof HttpError) {
    return c.json({ error: { code: error.code, message: error.message } }, error.status, error.headers);
  }
  console.error(JSON.stringify({ level: "error", message: error.message, path: c.req.path }));
  return c.json({ error: { code: "internal_error", message: "Internal server error." } }, 500);
});
