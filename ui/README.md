# InfraPad frontend

This npm workspace contains the reusable `@infrapad/ui` document routes and the standalone Vite host. The supported development URL is `http://localhost:8089`; port 5173 is loopback-only and is not a browser entry point.

## Install and validate

```sh
cd ui
npm ci
npm run typecheck
npm run build
```

The standalone output is written to `apps/standalone/dist/`. The production bundle intentionally has no `/ui/config` implementation; the Go static-serving task will provide that endpoint.

## Authenticated development

Only one process may own port 8089. Stop a Compose-managed development proxy first if one is running (for example, `task podman:dev:stop` from the repository root). Then use two terminals from the repository root:

```sh
# Terminal 1
cd ui
npm run dev

# Terminal 2
cd deploy/podman/dummy-auth
GOWORK=off go run . proxy \
  --listen=127.0.0.1:8089 \
  --upstream=http://127.0.0.1:5173
```

Open `http://localhost:8089/documents`. Anonymous users are sent to the dummy login page and returned to the original local URL after login. Vite proxies `/v1` to the Go gateway at `http://127.0.0.1:8088`; start the server separately when using real document data.

Both page traffic and Vite's WebSocket hot updates pass through dummy auth. For a quick HMR smoke check, keep the browser open after login, change `.standalone-brand` in `apps/standalone/src/styles.css`, and verify that the masthead updates without a reload or another login.

## Browser journey

Run the journey from `ui/`:

```sh
npm run test:e2e
```

Playwright starts real Vite on `15173` and dummy-auth on `18089`, keeping the normal development ports (`5173` and `8089`) available for other processes. It mocks browser requests to InfraPad and Prometheus, but exercises the real configuration endpoint, session login, proxy, nested routes, and detail reload. Override either test-only port when needed:

```sh
INFRAPAD_E2E_VITE_PORT=15174 INFRAPAD_E2E_AUTH_PORT=18090 npm run test:e2e
```
