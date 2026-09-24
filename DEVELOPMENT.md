# InfraPad development

Run all `task` commands below from the repository root. Install frontend dependencies once, only if you plan to work on the UI:

```sh
cd ui && npm ci && cd ..
```

Tasks do not install npm dependencies automatically. `task dev` still only starts the Podman services and Go server; it does not build the UI or start Vite. On a fresh checkout without built assets, `task dev`, `task server:run`, and `task server:watch` work for API-only development.

## Built standalone UI (prod)

Build the frontend, then run the normal Podman services and Go server, **without Vite**:

```sh
task ui:build
# Terminal 1 (or use task dev to manage both the services and server)
INFRAPAD_UI=prod task podman:dev:run
# Terminal 2
INFRAPAD_UI=prod task server:run
```

Browse [http://localhost:8089/ui/documents](http://localhost:8089/ui/documents) through the dummy authentication proxy, not directly through Go. `INFRAPAD_UI=prod task dev` is an alternative to the two terminals above (it starts/stops the services and runs Go). If `INFRAPAD_UI` is unset/empty, prod mode is already the default. A local `.env` may set `INFRAPAD_UI=hot-reload`; explicitly set `INFRAPAD_UI=prod` for both Podman and server tasks to override it when working with built assets.

In prod mode, `server:run` and `server:watch` automatically use `../ui/apps/standalone/dist/` (relative to their `server/` working directory) **only if** `index.html` exists there. No built assets means API-only startup. `task ui:build` rebuilds the external assets without recompiling Go; refresh the browser to see them. `INFRAPAD_UI_DIR` can set a different directory manually, and an explicit `task server:run -- --ui-dir /path/to/dist` takes precedence over both. For a relative `--ui-dir`, use a path relative to `server/` (e.g. `../ui/apps/standalone/dist/`). Hot-reload mode never automatically attaches old built assets. Go's `/ui/config` is available even when static assets are not.

## Hot reload with dummy authentication

Use three separate terminals:

```sh
# Terminal 1
INFRAPAD_UI=hot-reload task podman:dev:run
# Terminal 2
task server:run
# Terminal 3
task ui:dev
```

Open [http://localhost:8089/ui/documents](http://localhost:8089/ui/documents). The proxy forwards authenticated browser and HMR traffic to Vite, which forwards `/v1` and `/ui/config` to Go on port 8088. Vite's port 5173 is not a supported browser entry point. `ui:dev` builds the shared package initially for declarations/type checking; Vite then serves shared React/TypeScript and CSS directly from `ui/packages/ui/src`, so shared edits hot-reload without another package build or watcher. Production builds and FleetShift continue to consume the built package.

## Hot reload with OpenShift authentication

Use the same three terminals with the OpenShift proxy. Match auth mode in the Podman and UI terminals:

```sh
# Terminal 1
INFRAPAD_UI=hot-reload INFRAPAD_AUTH=openshift task podman:dev:run
# Terminal 2
task server:run
# Terminal 3
INFRAPAD_AUTH=openshift task ui:dev
```

Browse [https://localhost:8443/ui/documents](https://localhost:8443/ui/documents). First-time OpenShift bootstrap requires a logged-in `oc` context; later starts can reuse local credentials and cached discovery metadata. To use **built** assets with OpenShift auth, use `task ui:build`, `INFRAPAD_UI=prod INFRAPAD_AUTH=openshift task podman:dev:run` and `INFRAPAD_UI=prod task server:run` instead; do not start Vite. The proxy remains the browser entry point in either mode.

## Server rebuilds and configuration

For any workflow, `task server:watch` can replace `task server:run`. It starts the server once and restarts it after a *manual* `task server:build` in another terminal. Successful builds atomically publish `server/infrapad-server`; failed builds leave the running server intact. `server:watch` does not rebuild on source changes. If it started API-only before the first UI build, restart the task to pick up the new assets directory. If changing Go's `/ui/config` during hot reload, rebuild Go; frontend changes still hot-reload on their own.

`--prometheus-url` overrides `INFRAPAD_PROMETHEUS_URL` (default `http://localhost:9090`); this URL must be browser-reachable. The InfraPad API base is `/v1`. Go trusts forwarded identity only when UI traffic comes via the authentication proxy; direct `/v1/`, gRPC, and health access remain anonymous. `INFRAPAD_AUTH_UPSTREAM` remains an explicit proxy upstream override for advanced setups.

## Tests

```sh
task ui:test:unit  # shared-package Vitest; independent of Go and database
task ui:test:e2e   # authenticated standalone Playwright journey
task ui:test:all   # both
task test:all      # CLI, server and UI suites
```

Start the database and a Go server on port 8088 before UI e2e tests (for example, `task podman:postgres` and `task server:run` in separate terminals). Playwright starts its own Vite and dummy-auth proxy on isolated ports 15173 and 18089; **do not** start another normal UI proxy for the test. It mocks browser document and Prometheus requests but calls Go for `/ui/config`. Set `INFRAPAD_E2E_VITE_PORT` / `INFRAPAD_E2E_AUTH_PORT` to change those ports. The root aggregate suite also needs the usual running database/services required by CLI and server tests; it does not provision them.
