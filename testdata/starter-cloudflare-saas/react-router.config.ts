import type { Config } from "@react-router/dev/config";

export default {
  ssr: true,
  future: {
    // Required by @cloudflare/vite-plugin: run the SSR build in the Cloudflare environment.
    v8_viteEnvironmentApi: true,
  },
} satisfies Config;
