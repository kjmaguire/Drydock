import { createRequestHandler } from "react-router";
import { app } from "../src/api/app";

declare module "react-router" {
  export interface AppLoadContext {
    cloudflare: { env: Env; ctx: ExecutionContext };
  }
}

const requestHandler = createRequestHandler(() => import("virtual:react-router/server-build"), import.meta.env.MODE);

export default {
  async fetch(request, env, ctx) {
    const { pathname } = new URL(request.url);
    // /api/* is served by Hono; everything else is server-rendered by React Router.
    if (pathname === "/api" || pathname.startsWith("/api/")) {
      return app.fetch(request, env, ctx);
    }
    return requestHandler(request, { cloudflare: { env, ctx } });
  },
} satisfies ExportedHandler<Env>;
