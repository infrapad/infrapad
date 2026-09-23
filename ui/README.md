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

See the repository's [development guide](../DEVELOPMENT.md) for the canonical three-terminal dummy and OpenShift hot-reload workflows.

## Browser journey

Run the journey from `ui/`:

```sh
npm run test:e2e
```

Playwright starts real Vite on `15173` and dummy-auth on `18089`, keeping the normal development ports (`5173` and `8089`) available for other processes. It mocks browser requests to InfraPad and Prometheus, but exercises the real configuration endpoint, session login, proxy, nested routes, and detail reload. Override either test-only port when needed:

```sh
INFRAPAD_E2E_VITE_PORT=15174 INFRAPAD_E2E_AUTH_PORT=18090 npm run test:e2e
```
