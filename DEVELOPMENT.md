# InfraPad development

## One-time frontend setup

Install the frontend dependencies once from the repository root:

```sh
cd ui
npm ci
cd ..
```

`task ui:dev` starts Vite only; it does not install dependencies or manage the backend and containers.

## Standalone UI with dummy authentication

Run the Podman environment, Go server, and Vite in three separate terminals from the repository root:

```sh
# Terminal 1
INFRAPAD_UI=hot-reload task podman:dev:run
```

```sh
# Terminal 2
task server:run
```

```sh
# Terminal 3
task ui:dev
```

Open [http://localhost:8089/ui/documents](http://localhost:8089/ui/documents). The dummy authentication proxy is the browser-facing endpoint; it forwards UI and hot-reload traffic to Vite, while Vite forwards `/v1` API and `/ui/config` configuration traffic (including forwarded identity headers) to the running Go server. Both the Go server and database are required for this workflow. Port 5173 is not a supported browser entry point.

## Standalone UI with OpenShift authentication

The same development layout can use the OpenShift OAuth proxy. Keep the authentication mode consistent in the Podman and UI terminals:

```sh
# Terminal 1
INFRAPAD_UI=hot-reload INFRAPAD_AUTH=openshift task podman:dev:run
```

```sh
# Terminal 2
task server:run
```

```sh
# Terminal 3
INFRAPAD_AUTH=openshift task ui:dev
```

Open [https://localhost:8443/ui/documents](https://localhost:8443/ui/documents). OpenShift bootstrap requires a logged-in `oc` context on its first run. Later starts can reuse the generated local credentials and cached discovery metadata while regenerating the proxy configuration for the selected UI upstream.

For the normal non-hot-reload environment, omit `INFRAPAD_UI`; authentication proxies then forward to the Go server on port 8088 as before.

## Optional server binary reload

In either three-terminal workflow, you can use `task server:watch` instead of `task server:run` in the server terminal. It builds and starts the server once, then waits for a newly published `server/infrapad-server`. After changing Go code, run `task server:build` separately (in another terminal). A successful build atomically publishes the binary; the watcher gracefully stops the old server before starting the new one. Failed builds leave the running server untouched. The watcher does **not** rebuild on source changes; `task server:run` and root `task dev` retain their usual behavior. In hot-reload mode the `/ui/config` endpoint is served by Go, so refresh the server binary with `task server:build` when changing its behavior; frontend changes still hot-reload independently.

## Built standalone UI

Build the assets once, then serve the output from Go behind the authentication proxy (without Vite):

```sh
cd ui && npm run build && cd ..
# Start the database and proxy in normal server mode (omit INFRAPAD_UI=hot-reload).
# In a separate terminal, from the repository root:
task server:run -- --ui-dir ui/apps/standalone/dist/
```

Browse through the proxy at [http://localhost:8089/ui/documents](http://localhost:8089/ui/documents) (or the OpenShift proxy at `https://localhost:8443/ui/documents`), not directly through Go. The Go server serves `/ui/` and `/ui/config`; `/` redirects to `/ui/documents` when the directory is enabled. The assets are external to the Go binary: rebuilding with `cd ui && npm run build` updates the configured directory without rebuilding Go. Without a configured directory the server remains API-only, but `/ui/config` is still available.

`--ui-dir` overrides `INFRAPAD_UI_DIR`. `--prometheus-url` overrides `INFRAPAD_PROMETHEUS_URL` (default `http://localhost:9090`); this URL must be reachable by the browser, not the Go server. The InfraPad API base stays `/v1`. Forwarded identity is only trustworthy when UI traffic reaches Go via the authentication proxy; direct `/v1/`, gRPC and health access remain anonymous.
