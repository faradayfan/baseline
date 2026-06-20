import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  // Where the dev server proxies API calls. Defaults to a locally-run backend;
  // set VITE_BACKEND_TARGET to point elsewhere — e.g. a port-forward to the
  // cluster backend (http://localhost:8080).
  const env = loadEnv(mode, process.cwd(), "");
  const target = env.VITE_BACKEND_TARGET || "http://localhost:8080";

  return {
    plugins: [react(), tailwindcss()],
    server: {
      port: 5173,
      // Proxy /api/* to the Go backend, rewriting the /api prefix to /v1 (the
      // backend mounts its routes at /v1/...). Same-origin in the browser, so
      // no CORS. The X-Baseline-Principal header is set by the client per
      // request — there are no cookies to forward (header auth, not sessions).
      proxy: {
        "/api": {
          target,
          changeOrigin: false,
          rewrite: (p) => p.replace(/^\/api/, "/v1"),
        },
        "/healthz": { target, changeOrigin: false },
        "/readyz": { target, changeOrigin: false },
      },
    },
  };
});
