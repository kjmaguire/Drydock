import { createRequestHandler, RouterContextProvider } from "react-router";
import { cloudflareContext } from "../app/context";
import { app } from "../src/api/app";

const requestHandler = createRequestHandler(() => import("virtual:react-router/server-build"), import.meta.env.MODE);

export default {
  async fetch(request, env, ctx) {
    const { pathname } = new URL(request.url);
    // /api/* is served by Hono; everything else is server-rendered by React Router.
    if (pathname === "/api" || pathname.startsWith("/api/")) {
      return app.fetch(request, env, ctx);
    }
    const context = new RouterContextProvider();
    context.set(cloudflareContext, { env, ctx });
    return requestHandler(request, context);
  },
} satisfies ExportedHandler<Env>;
