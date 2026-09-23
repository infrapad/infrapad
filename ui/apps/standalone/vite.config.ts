import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

function headerValue(value: string | string[] | undefined): string | undefined {
  if (Array.isArray(value)) return value[0];
  return value;
}

function runtimeConfiguration(): Plugin {
  return {
    name: "infrapad-runtime-configuration",
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        const requestUrl = new URL(request.url ?? "/", "http://localhost");
        if (request.method !== "GET" || requestUrl.pathname !== "/ui/config") {
          next();
          return;
        }

        const username = headerValue(request.headers["x-forwarded-user"]);
        const email = headerValue(request.headers["x-forwarded-email"]);
        const identity = username
          ? { username, ...(email ? { email } : {}) }
          : null;

        response.statusCode = 200;
        response.setHeader("Cache-Control", "no-store");
        response.setHeader("Content-Type", "application/json; charset=utf-8");
        response.end(JSON.stringify({
          identity,
          services: {
            infrapadApiBaseUrl: "/v1",
            prometheusApiBaseUrl: "http://localhost:9090",
          },
        }));
      });
    },
  };
}

const vitePort = Number(process.env.INFRAPAD_VITE_PORT ?? "5173");
const authPort = Number(process.env.INFRAPAD_AUTH_PORT ?? "8089");

export default defineConfig({
  plugins: [react(), runtimeConfiguration()],
  server: {
    host: "127.0.0.1",
    port: vitePort,
    strictPort: true,
    hmr: {
      protocol: "ws",
      host: "localhost",
      clientPort: authPort,
    },
    proxy: {
      "/v1": {
        target: "http://127.0.0.1:8088",
      },
    },
  },
  build: {
    outDir: "dist",
  },
});
