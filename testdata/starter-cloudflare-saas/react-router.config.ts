import type { Config } from "@react-router/dev/config";

// React Router 8: the Vite Environment API is always on (the v8_viteEnvironmentApi flag is gone), which is what
// @cloudflare/vite-plugin needs to run the SSR build in the Cloudflare environment.
export default {
  ssr: true,
} satisfies Config;
