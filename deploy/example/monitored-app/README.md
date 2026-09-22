# monitored-app

A minimal Go HTTP service with controllable endpoint health, Prometheus metrics, and alerting.

## Endpoints

The app exposes two endpoints (`/endpoint1`, `/endpoint2`) that start healthy and can be toggled to a failure state.

### Check status

```bash
curl http://localhost:8080/endpoint1   # 200 "ok"
curl http://localhost:8080/endpoint2   # 200 "ok"
```

### Set an endpoint to fail

```bash
curl -X POST -d 'fail' http://localhost:8080/endpoint1
curl http://localhost:8080/endpoint1   # 500 "fail"
curl http://localhost:8080/endpoint2   # 200 "ok" (unaffected)
```

### Recover an endpoint

```bash
curl -X POST -d 'ok' http://localhost:8080/endpoint1
curl http://localhost:8080/endpoint1   # 200 "ok"
```

## Metrics

`GET /metrics` exposes a `monitored_app_up` gauge per endpoint:

```
monitored_app_up{endpoint="endpoint1"} 1
monitored_app_up{endpoint="endpoint2"} 1
```

The value is `1` when the endpoint returns "ok" and `0` when set to "fail".

## Alerting

When deployed via `task pd:monitored-app`, Prometheus scrapes the metrics and evaluates the `MonitoredAppEndpointDown` alert rule — it fires whenever `monitored_app_up == 0` for any endpoint. Alerts are forwarded to Alertmanager.

- Prometheus UI: http://localhost:9090
- Alertmanager UI: http://localhost:9093

## Incident Simulation

`simulate.sh` automates an end-to-end incident lifecycle against the running
app/Prometheus stack (e.g. started via `task pd:monitored-app`) and records it
as an infrapad document.

```bash
./simulate.sh incident-start
./simulate.sh incident-resolve
```

### `incident-start`

1. Validates the app is healthy (`/endpoint1`, `/endpoint2`, `/metrics`).
2. Sets both endpoints to `fail`.
3. Waits for Prometheus to report both `MonitoredAppEndpointDown` alerts as firing.
4. Creates an infrapad document titled "Monitored app incident on ..." with an
   `alerts_matcher` block matching both endpoints, `Since` set to the start time.
5. Pulls the document to `./tmp/incident.md` and remembers the doc name in
   `./tmp/.doc_name` for the resolve step.

### Agent invocation

When the simulation automatically acquires an InfraPad credential, it saves the
credential in the ignored `.env` file. From this directory, make it available
to an agent of your choice before launching the agent:

```bash
source .env
# Launch the chosen agent, then ask it to:
# /incident-investigate tmp/incident.md
```

Configure the agent to use the skills from `./.agents/skills`. If you supplied
`INFRAPAD_TOKEN` yourself, the simulation does not access `.env`; launch the
agent from the environment that already contains the token.

### `incident-resolve`

1. Sets both endpoints back to `ok`.
2. Updates the `alerts_matcher` block's `Until` field to the current time.
3. Appends a markdown block noting the alerts were resolved.

### Requirements

- `curl` and `jq`
- The `infrapad` CLI, resolved as follows:
  - `$INFRAPAD_CLI`, if set, is used as-is.
  - Otherwise, if `./cli` exists relative to the infrapad project root
    (auto-detected, or `$INFRAPAD_PROJECT_DIR` if set), `go run $INFRAPAD_PROJECT_DIR/cli` is used.
  - Otherwise, an `infrapad` binary on `$PATH` is used. Install it with
    `task cli:install` from the infrapad project root if missing.

### Configuration

The simulation passes `INFRAPAD_API_URL` and `INFRAPAD_TOKEN` to every CLI
subprocess. It configures authentication as follows:

- **Caller-provided token:** A non-empty `INFRAPAD_TOKEN` is used unchanged.
  The simulation does not read, create, or modify `.env`.
- **Saved token:** If no token was provided and `.env` exists, the simulation
  sources the file and reuses its token. An invalid file stops the command with
  instructions to remove the file and try again.
- **Automatic acquisition:** If `.env` does not exist, the simulation requests
  a token from `/auth/token` at the effective API URL. A successful token is
  exported for the current command and saved in `.env` with restrictive
  permissions.
- **Anonymous fallback:** If the request fails or returns an empty response, the
  simulation warns and continues without a token. It does not create `.env`.
  This is expected when the server does not provide the development token
  route.

Delete `.env` to acquire and save a new token on the next simulation command.

| Variable               | Default                  | Purpose                                      |
|------------------------|--------------------------|----------------------------------------------|
| `MONITORED_APP_URL`    | `http://localhost:8080`  | Base URL of the monitored app                |
| `PROMETHEUS_URL`       | `http://localhost:9090`  | Base URL of Prometheus                       |
| `INFRAPAD_API_URL`     | CLI default (`http://localhost:8089`) | InfraPad proxy/server URL        |
| `INFRAPAD_TOKEN`       | unset                    | Optional bearer token for InfraPad requests  |
| `INFRAPAD_PROJECT_DIR` | repo root (auto-detected)| Used to locate `./cli` for `go run`          |
| `INFRAPAD_CLI`         | auto-detected            | Overrides CLI invocation entirely            |
