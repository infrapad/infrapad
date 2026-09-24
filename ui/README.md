# InfraPad frontend

This npm workspace contains the reusable `@infrapad/ui` document routes and the standalone Vite host. The supported development URL is `http://localhost:8089`; port 5173 is loopback-only and is not a browser entry point.

## Install and validate

```sh
cd ui
npm ci
npm run build           # builds @infrapad/ui to packages/ui/dist, then the standalone host
npm run typecheck
npm run test --workspace @infrapad/ui
```

The standalone output is written to `apps/standalone/dist/`, with asset URLs based at `/ui/`. Go serves `/ui/config` in both development and built mode; Vite forwards that request (including the proxy-derived identity headers) to the running Go server. `npm run dev` builds the shared package before starting Vite. When editing shared TypeScript during development, run `npm run watch --workspace @infrapad/ui` in another terminal to emit changes to the linked package for Vite HMR (or rebuild with `npm run build --workspace @infrapad/ui`). Shared CSS is copied to `dist` in a package build; rebuild after changing it.

## Shared package / FleetShift adapter contract

Build locally in `ui/` with `npm ci && npm run build --workspace @infrapad/ui` **before** building a consumer. The package export map resolves `@infrapad/ui` to ESM and types in `packages/ui/dist/`, and `@infrapad/ui/styles.css` to `packages/ui/dist/styles.css`; it never exports TSX source. A host supplies React, React DOM, React Router, PatternFly core, icons, data-view, charts, and Victory; it must load PatternFly base styles and choose the root theme itself. The package supplies Markdown/sanitization, YAML, and Monaco diff libraries.

The FleetShift adapter can use its sibling checkout as a local file dependency.
Import both the component and CSS once from the host, then mount it beneath the host's existing wildcard documents route:

```tsx
import { InfraPadDocuments } from "@infrapad/ui";
import "@infrapad/ui/styles.css";

<InfraPadDocuments services={{
  infrapadApiBaseUrl: "/v1", // or FleetShift's configured API origin + /v1
  prometheusApiBaseUrl: "http://localhost:9090", // host-configured Prometheus origin
}} />
```

`InfraPadDocuments` owns the index and `:docId` routes, **not** the router or host mount. Link targets are relative to the mount. The adapter supplies the existing FleetShift browser-global service settings as explicit URL strings, including `/v1` on the InfraPad base; do not import FleetShift code into this package. When installed as a file dependency, reinstall/refresh the local package after rebuilding its `dist` if the package manager copied it rather than symlinked it. `npm run check:rspack` runs a small, optional consumer build against the sibling FleetShift toolchain and CSS loaders (requires that checkout's dependencies installed); it does not replace the adapter's full Rspack build.

## Authenticated development

See the repository's [development guide](../DEVELOPMENT.md) for the canonical three-terminal dummy and OpenShift hot-reload workflows. Open the authentication proxy at `/ui/documents` (or `/ui/documents/:docId` for a detail link); Vite's port is not a supported browser entry point. The Go server and database must be running to supply `/ui/config` during hot reload.

For built mode, run `npm run build`, start Go with `--ui-dir ui/apps/standalone/dist/` from the repository root (or `INFRAPAD_UI_DIR`), and browse `/ui/documents` through the auth proxy without Vite. Built assets update independently when rebuilt; to update Go under `task server:watch`, run `task server:build`. `--prometheus-url` / `INFRAPAD_PROMETHEUS_URL` set the browser-reachable Prometheus base URL (default `http://localhost:9090`); the API base remains `/v1`. Go trusts `X-Forwarded-User` and optional `X-Forwarded-Email` only as supplied by the proxy. No authentication is added to the API or health endpoints.

## Browser journey

Run the journey from `ui/`:

```sh
npm run test:e2e
```

Start a Go server with the current binary and a database on port `8088` before running this journey (for `task server:watch`, run `task server:build` after Go changes). Playwright starts real Vite on `15173` and dummy-auth on `18089`, keeping the normal development ports (`5173` and `8089`) available for other processes. It mocks browser requests to InfraPad and Prometheus, but exercises Go's real `/ui/config` via Vite, session login, proxy, `/ui/documents` routes, and detail reload. Override either test-only port when needed:

```sh
INFRAPAD_E2E_VITE_PORT=15174 INFRAPAD_E2E_AUTH_PORT=18090 npm run test:e2e
```
