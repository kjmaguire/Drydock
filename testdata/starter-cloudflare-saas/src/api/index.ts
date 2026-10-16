// API-only Worker entry. Used by the Vitest suite (see vitest.config.ts) so the
// API can be tested without the React Router build. Production uses
// workers/app.ts, which mounts this same Hono app in front of React Router.
import { app } from "./app";

export default {
  fetch: (request, env, ctx) => app.fetch(request, env, ctx),
} satisfies ExportedHandler<Env>;
