# InfraPad frontend

This npm workspace contains the reusable `@infrapad/ui` document routes and the standalone Vite host. The supported development URL is `http://localhost:8089`; port 5173 is loopback-only and is not a browser entry point.

## Local development

Follow the repository's [development guide](../DEVELOPMENT.md) for one-time installation, task-based built and hot-reload workflows, and UI tests. `task ui:build` builds `@infrapad/ui` then the standalone host; output is in `apps/standalone/dist/` with asset URLs based at `/ui/`. Go serves `/ui/config` in both development and built mode; Vite proxies it with forwarded identity headers. `task ui:dev` builds the shared package once for type declarations, then Vite resolves shared TypeScript/React and styles directly from source for HMR. Production builds and package consumers resolve the built exports instead.

## Shared package / FleetShift adapter contract

After the one-time installation described in the development guide, build the package with `task ui:build` (or `cd ui && npm run build --workspace @infrapad/ui` if only refreshing the package) **before** building a consumer. The package export map resolves `@infrapad/ui` to ESM and types in `packages/ui/dist/`, and `@infrapad/ui/styles.css` to `packages/ui/dist/styles.css`; it never exports TSX source. A host supplies React, React DOM, React Router, PatternFly core, icons, data-view, charts, and Victory; it must load PatternFly base styles and choose the root theme itself. The package supplies Markdown/sanitization, YAML, and Monaco diff libraries.

The FleetShift adapter can use its sibling checkout as a local file dependency. Import the built component and CSS once from the host, then mount it beneath the host's existing wildcard documents route. The package exports `InfraPadDocumentsProps`, `InfraPadServices`, and `InfraPadFetch`. The optional `fetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response>` prop is used for **every** document/list/detail/history and Prometheus request. URLs, JSON parsing, and HTTP/error handling remain in the package. If omitted, requests use ordinary `fetch` (as in the standalone host); no credentials are added by the package.

FleetShift should obtain its InfraPad browser origin from FleetShift's `/api/ui/config`, fetch **InfraPad's** `/ui/config` with its bearer token, resolve relative InfraPad service URLs (such as `/v1`) against that InfraPad origin, and pass the resulting URLs and a token-aware request function to the package. For example, after the adapter has loaded those trusted service URLs:

```tsx
import { InfraPadDocuments, type InfraPadFetch, type InfraPadServices } from "@infrapad/ui";
import "@infrapad/ui/styles.css";

// `services` comes from host configuration, not a document or browser-global defaults.
const services: InfraPadServices = {
  infrapadApiBaseUrl: new URL(infrapadConfig.services.infrapadApiBaseUrl, infrapadOrigin).href,
  prometheusApiBaseUrl: new URL(infrapadConfig.services.prometheusApiBaseUrl, infrapadOrigin).href,
};
const hostFetch: InfraPadFetch = async (input, init) => {
  const token = getCurrentOidcToken(); // read the current token on each call
  if (!token) throw new Error("FleetShift login required");
  const headers = new Headers(input instanceof Request ? input.headers : undefined);
  new Headers(init?.headers).forEach((value, key) => headers.set(key, value));
  headers.set("Authorization", `Bearer ${token}`);
  return fetch(input, { ...init, headers });
};

<InfraPadDocuments services={services} fetch={hostFetch} />
```

`InfraPadDocuments` owns the index and `:docId` routes, **not** the router or host mount. Link targets are relative to the mount. Keep the token out of config, URLs, document content, and global fetch; only use a deployment-trusted Prometheus URL as a bearer destination. The adapter, not this package, owns FleetShift's login/config validation. The standalone host keeps its existing `/ui/config` and identity/login flow and does not pass `fetch`. Do not import FleetShift code into this package. When installed as a file dependency, rebuild `@infrapad/ui` and reinstall/refresh the local package if the package manager copied its `dist` rather than symlinked it. `npm run check:rspack` runs a small, optional consumer build against the sibling FleetShift toolchain and CSS loaders (requires that checkout's dependencies installed); it does not replace the adapter's full Rspack build.

## Authentication and browser journey

See the [development guide](../DEVELOPMENT.md) for the canonical built and hot-reload dummy/OpenShift workflows, server/watch behavior, and `task ui:test:e2e` prerequisites. Go trusts `X-Forwarded-User` and optional `X-Forwarded-Email` only as supplied by the proxy. No authentication is added to the API or health endpoints.
