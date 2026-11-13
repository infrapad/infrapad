import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";
import { fileURLToPath, URL } from "node:url";

function rootRedirect(): Plugin {
  return {
    name: "infrapad-root-redirect",
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        if (new URL(request.url ?? "/", "http://localhost").pathname !== "/") {
          next();
          return;
        }
        response.statusCode = 302;
        response.setHeader("Location", "/ui/documents");
        response.end();
      });
    },
  };
}

const vitePort = Number(process.env.INFRAPAD_VITE_PORT ?? "5173");
const authMode = process.env.INFRAPAD_AUTH ?? "dummy";

let hmrProtocol: "ws" | "wss";
let defaultAuthPort: string;
switch (authMode) {
  case "dummy":
    hmrProtocol = "ws";
    defaultAuthPort = "8089";
    break;
  case "openshift":
    hmrProtocol = "wss";
    defaultAuthPort = "8443";
    break;
  case "none":
    throw new Error("INFRAPAD_AUTH=none is not supported by the standalone UI; use dummy or openshift");
  default:
    throw new Error(`Invalid INFRAPAD_AUTH value '${authMode}'; accepted values: dummy, openshift`);
}

const authPort = Number(process.env.INFRAPAD_AUTH_PORT ?? defaultAuthPort);

export default defineConfig(({ command }) => ({
  base: "/ui/",
  plugins: [react(), rootRedirect()],
  // Only the dev server reads shared sources. Production and other consumers
  // keep using the package's built ESM and CSS exports.
  resolve: command === "serve"
    ? {
        alias: [
          {
            find: /^@infrapad\/ui\/styles\.css$/,
            replacement: fileURLToPath(new URL("../../packages/ui/src/styles.css", import.meta.url)),
          },
          {
            find: /^@infrapad\/ui$/,
            replacement: fileURLToPath(new URL("../../packages/ui/src/index.ts", import.meta.url)),
          },
        ],
      }
    : undefined,
  server: {
    host: "127.0.0.1",
    port: vitePort,
    strictPort: true,
    hmr: {
      protocol: hmrProtocol,
      host: "localhost",
      clientPort: authPort,
    },
    proxy: {
      "/v1": {
        target: "http://127.0.0.1:8088",
      },
      "/ui/config": {
        target: "http://127.0.0.1:8088",
      },
    },
  },
  build: {
    outDir: "dist",
  },
}));
