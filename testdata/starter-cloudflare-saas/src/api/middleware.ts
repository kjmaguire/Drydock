import { createMiddleware } from "hono/factory";
import { authenticate } from "../lib/auth";
import { type AppEnv, HttpError } from "./types";

/** Requires a valid session cookie; exposes the user as c.get("user"). */
export const requireAuth = createMiddleware<AppEnv>(async (c, next) => {
  const user = await authenticate(c.req.raw, c.env);
  if (!user) throw new HttpError(401, "unauthorized", "Sign in to use this endpoint.");
  c.set("user", user);
  await next();
});
