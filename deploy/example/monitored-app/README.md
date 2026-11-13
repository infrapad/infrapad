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
./simulate.sh start
./simulate.sh resolve
./simulate.sh refresh [incident.md]   # optional, any time
```

### `start`

1. Validates the app is healthy (`/endpoint1`, `/endpoint2`, `/metrics`).
2. Sets both endpoints to `fail`.
3. Waits for Prometheus to report both `MonitoredAppEndpointDown` alerts as firing.
4. Fetches an alert snapshot from Prometheus (see [Alert snapshots](#alert-snapshots))
   and saves it to `./tmp/matcher-payload.json` before any document change.
5. Creates an infrapad document titled "Monitored app incident on ..." with a
   `Prometheus/AlertsMatcher` block matching both endpoints, `since` set to the start time,
   and submits the prepared snapshot to that block.
6. Adds ordinary `Core/Markdown` incident notes (the sanitized Markdown fallback).
7. Creates a synthetic `Slack/Thread` reference and submits a complete payload
   via `POST /v1/documents/{id}/blocks/{number}:submitPayload`. A second payload
   edits a message and adds human replies interleaved with AI-agent triage.
   Slack messages start just after the thread's server-assigned event timestamp,
   so the document reads alerts → notes → thread → conversation/triage. The
   matcher's `since` still covers the original incident start.
   This demonstrates Slack conversation cards with message-count fields,
   agent cards with Markdown tables/code/links, source links, and revision diffs.
8. Pulls the complete document to `./tmp/incident.md`, remembers the doc name in
   `./tmp/.doc_name`, and prints the document's UI URL.

No Slack or AI service is contacted: `simulate-slack.py` produces enriched
synthetic payloads (see [docs/content-producers.md](../../../docs/content-producers.md)
for the payload contract and a minimal REST walkthrough), and the real server performs expansion, grouping, and
presentation. `slack.example` source links are illustrative; Prometheus and
application links point to the configured services. The complete last payload
(`tmp/slack-payload.json`) and thread block number (`tmp/.slack_block`) are kept
for the resolve step. Click a conversation's `rev` link in the UI to see the
initial payload and triage enrichment. Rebuild/restart the server and UI after
code changes so that the server sends `presentation`/`markdown` fields.

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

### `resolve`

1. Sets both endpoints back to `ok` and waits until Prometheus confirms that
   the alerts cleared (a failed alert-status request is an error, not a clear).
2. Reads the existing matcher's `since` and prepares a complete replacement
   snapshot before changing the document.
3. Updates the `Prometheus/AlertsMatcher` block's `until` field to the current
   time (this metadata change clears the stored snapshot), then submits the
   replacement snapshot to the same block. These are two separate requests.
4. Resubmits the complete Slack payload, preserving earlier message identities
   and adding human recovery confirmation and an AI-agent verification reply.
   Older incidents without saved Slack state skip this step.
5. Pulls the refreshed document and appends a Markdown resolution block. The
   unchanged matcher header round-trips and keeps its snapshot.

### `refresh [incident.md]`

Re-fetches the alert snapshot and resubmits it to the existing
`Prometheus/AlertsMatcher` block, with no other document or endpoint change.
It works for ongoing incidents (the window grows to the current time) and
resolved ones. The target document is identified by the `document:` front
matter of a pulled Markdown file (default `./tmp/incident.md`). The file
itself is not modified; pushing it unchanged later keeps the new snapshot.

### Alert snapshots

The UI never queries Prometheus. `simulate-snapshot.py` (Python standard
library only) fetches `/api/v1/query_range` for firing
`ALERTS{alertname="MonitoredAppEndpointDown", endpoint=~"endpoint1|endpoint2"}`
and writes a complete `submitPayload` request, `{"payload": {"query_results": ...}}`
(contract: [docs/content-producers.md](../../../docs/content-producers.md)).

- The query is demo-specific and independent of the matcher's label alternatives.
- The window is fixed when fetched: from the matcher's `since` minus two minutes
  to the fetch time, with a 5-second step (increased in 5-second multiples to
  stay within about 10,000 points per series). The stored chart never moves
  with the browser clock; the resolve step replaces it with a window covering
  the whole incident.
- A just-started demo incident has only seconds of alert history. The
  simulation therefore backdates the incident `since` by
  `SNAPSHOT_BACKFILL_SECONDS` (default 60). A series whose samples span less
  than that is artificially extended back to span it (repeating its first
  sample at step intervals, within the query window); longer series are
  submitted unmodified. Set it to `0` to disable both.
- A successful query without matches is stored as `series: []`. HTTP/API
  errors, malformed responses, and non-finite values stop the command before
  the document is changed; empty results are never substituted.
- A failed submission stops with the target URL and a `curl` retry command.
  The prepared payload stays in `./tmp/matcher-payload.json`. There is no
  automatic rollback: if resolution fails after the `until` update, the
  matcher has no snapshot until the retry succeeds.

### Requirements

- `curl` (with `--fail-with-body` support), `jq`, and Python 3 (standard library only)
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
| `SNAPSHOT_BACKFILL_SECONDS` | `60`                | Demo-only backdating of `since`; minimum series span |
| `INFRAPAD_API_URL`     | CLI default (`http://localhost:8089`) | InfraPad proxy/server URL        |
| `INFRAPAD_TOKEN`       | unset                    | Optional bearer token for InfraPad requests  |
| `INFRAPAD_PROJECT_DIR` | repo root (auto-detected)| Used to locate `./cli` for `go run`          |
| `INFRAPAD_CLI`         | auto-detected            | Overrides CLI invocation entirely            |
