import { createContext } from "react-router";

// The Cloudflare bindings for a request, set by the Worker entry (workers/app.ts) and read by loaders and actions with
// context.get(cloudflareContext). React Router 8 always runs middleware, so the load context is a RouterContextProvider of
// typed entries, not a plain object.
export const cloudflareContext = createContext<{ env: Env; ctx: ExecutionContext }>();
