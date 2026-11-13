#!/usr/bin/env bash
#
# simulate.sh — Monitored app incident simulation
#
# Usage:
#   simulate.sh start
#   simulate.sh resolve
#   simulate.sh refresh [incident.md]
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TMP_DIR="${SCRIPT_DIR}/tmp"
INCIDENT_FILE="${TMP_DIR}/incident.md"
DOC_NAME_FILE="${TMP_DIR}/.doc_name"
SLACK_BLOCK_FILE="${TMP_DIR}/.slack_block"
SLACK_PAYLOAD_FILE="${TMP_DIR}/slack-payload.json"
MATCHER_PAYLOAD_FILE="${TMP_DIR}/matcher-payload.json"
INFRAPAD_ENV_FILE="${SCRIPT_DIR}/.env"

MONITORED_APP_URL="${MONITORED_APP_URL:-http://localhost:8080}"
PROMETHEUS_URL="${PROMETHEUS_URL:-http://localhost:9090}"
# A just-created demo incident has little alert history. Backdate the
# incident since by this much, and extend snapshot series spanning less
# than this to cover it.
SNAPSHOT_BACKFILL_SECONDS="${SNAPSHOT_BACKFILL_SECONDS:-60}"

INFRAPAD_PROJECT_DIR="${INFRAPAD_PROJECT_DIR:-$(cd "${SCRIPT_DIR}/../../.." && pwd)}"

declare -g AUTH_USE_ENV=""

# ─── helpers ────────────────────────────────────────────────────────────────

log() { echo "==> $*"; }

fail() { echo "ERROR: $*" >&2; exit 1; }

auth_env_error() {
  fail "${INFRAPAD_ENV_FILE} could not provide a non-empty INFRAPAD_TOKEN. Remove it and rerun the command to regenerate it."
}

persist_infrapad_token() {
  local token="$1"
  local temp_file quoted_token

  # Prepare the complete file with restrictive permissions before publishing it.
  # A hard link creates the final name atomically and cannot replace a file that
  # appeared while the token request was in progress.
  temp_file=$(mktemp "${INFRAPAD_ENV_FILE}.tmp.XXXXXX") || \
    fail "Could not create a temporary InfraPad environment file."
  chmod 600 "$temp_file" || {
    rm -f -- "$temp_file"
    fail "Could not restrict permissions on the temporary InfraPad environment file."
  }
  trap "rm -f -- $(printf '%q' "$temp_file")" EXIT

  quoted_token=${token//\'/\'\\\'\'}
  if ! printf "export INFRAPAD_TOKEN='%s'\n" "$quoted_token" > "$temp_file"; then
    fail "Could not write the temporary InfraPad environment file."
  fi
  if ! ln "$temp_file" "$INFRAPAD_ENV_FILE"; then
    fail "Could not create ${INFRAPAD_ENV_FILE}; it was not overwritten."
  fi

  rm -f -- "$temp_file"
  trap - EXIT
}

# Preserve caller credentials, reuse a previously acquired development token,
# or make one best-effort acquisition attempt against the effective API URL.
configure_infrapad_auth() {
  if [ -n "${INFRAPAD_TOKEN:-}" ]; then
    return
  fi

  if [ -e "$INFRAPAD_ENV_FILE" ] || [ -L "$INFRAPAD_ENV_FILE" ]; then
    if ! source "$INFRAPAD_ENV_FILE"; then
      auth_env_error
    fi
    if [ -z "${INFRAPAD_TOKEN:-}" ]; then
      auth_env_error
    fi

    AUTH_USE_ENV=true
    export INFRAPAD_TOKEN
    return
  fi

  local api_url="${INFRAPAD_API_URL:-http://localhost:8089}"
  local token
  if token=$(curl --fail --silent --show-error \
    --connect-timeout 2 --max-time 10 \
    --data-urlencode 'username=monitored-app-simulation' \
    "${api_url%/}/auth/token" 2>/dev/null) && [ -n "$token" ]; then
    persist_infrapad_token "$token"
    INFRAPAD_TOKEN="$token"
    AUTH_USE_ENV=true
    export INFRAPAD_TOKEN
  else
    echo "WARNING: could not obtain an InfraPad development token; continuing anonymously." >&2
  fi
}

# Resolve the infrapad CLI to use. Prefer building from source when this
# script lives inside the infrapad project checkout (./cli exists); this is
# not the case when the monitored-app directory is copied/used standalone
# outside of the project. In that case fall back to a pre-installed
# `infrapad` binary on PATH.
resolve_infrapad_cli() {
  if [ -n "${INFRAPAD_CLI:-}" ]; then
    return
  fi

  if [ -d "${INFRAPAD_PROJECT_DIR}/cli" ]; then
    INFRAPAD_CLI="go run ${INFRAPAD_PROJECT_DIR}/cli"
  elif command -v infrapad &>/dev/null; then
    INFRAPAD_CLI="infrapad"
  else
    echo "ERROR: infrapad CLI not found on PATH and ${INFRAPAD_PROJECT_DIR}/cli does not exist." >&2
    echo "       Install it by running 'task cli:install' from the infrapad project root, or set \$INFRAPAD_CLI explicitly." >&2
    exit 1
  fi
}

resolve_infrapad_cli

check_endpoint() {
  local ep="$1"
  local status
  status=$(curl -s -o /dev/null -w '%{http_code}' "${MONITORED_APP_URL}/${ep}") || true
  echo "$status"
}

set_endpoint() {
  local ep="$1" val="$2"
  curl -s -X POST -d "$val" "${MONITORED_APP_URL}/${ep}" > /dev/null
}

# Print the number of distinct endpoints with a firing demo alert. A failed
# or malformed alert-status request is an error, never evidence of zero alerts.
count_firing_alerts() {
  local response
  response=$(curl --fail --silent --show-error --max-time 10 "${PROMETHEUS_URL}/api/v1/alerts") || \
    fail "Prometheus alert-status request to ${PROMETHEUS_URL}/api/v1/alerts failed."
  echo "$response" | jq -e \
    'if .status == "success" and (.data.alerts | type) == "array" then
       [.data.alerts[]
        | select(.state == "firing" and .labels.alertname == "MonitoredAppEndpointDown")
        | .labels.endpoint] | unique | length
     else error("unexpected response") end' || \
    fail "Prometheus alert-status response was malformed."
}

wait_for_alerts() {
  local max_wait=120
  local interval=5
  local elapsed=0
  log "Waiting for Prometheus to fire alerts for both endpoints..."
  while [ "$elapsed" -lt "$max_wait" ]; do
    local firing
    firing=$(count_firing_alerts) || exit 1
    if [ "$firing" -ge 2 ]; then
      log "Both alerts are firing."
      return 0
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
    log "  ...waiting (${elapsed}s/${max_wait}s, firing=${firing}/2)"
  done
  fail "Timed out waiting for both alerts to fire."
}

wait_for_alerts_resolved() {
  local max_wait=120
  local interval=5
  local elapsed=0
  log "Waiting for Prometheus alerts to clear for both endpoints..."
  while [ "$elapsed" -lt "$max_wait" ]; do
    local firing
    firing=$(count_firing_alerts) || exit 1
    if [ "$firing" -eq 0 ]; then
      log "All alerts have cleared."
      return 0
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
    log "  ...waiting (${elapsed}s/${max_wait}s, firing=${firing})"
  done
  fail "Timed out waiting for alerts to clear."
}

# ─── start ─────────────────────────────────────────────────────────────────

cmd_start() {
  local since
  since=$(jq -nr --argjson t "$(( $(date -u +%s) - SNAPSHOT_BACKFILL_SECONDS ))" '$t | todate')

  # 1. Validate the demo app is running (both endpoints healthy + metrics).
  log "Validating monitored app is running..."

  for ep in endpoint1 endpoint2; do
    local code
    code=$(check_endpoint "$ep")
    log "  ${ep}: HTTP ${code}"
  done

  local metrics_code
  metrics_code=$(curl -s -o /dev/null -w '%{http_code}' "${MONITORED_APP_URL}/metrics") || true
  if [ "$metrics_code" != "200" ]; then
    fail "Prometheus metrics endpoint returned HTTP ${metrics_code}"
  fi
  log "  /metrics: HTTP ${metrics_code}"

  # 2. Make both endpoints fail.
  log "Setting both endpoints to fail..."
  set_endpoint endpoint1 fail
  set_endpoint endpoint2 fail

  # Verify they are failing.
  for ep in endpoint1 endpoint2; do
    local code
    code=$(check_endpoint "$ep")
    if [ "$code" != "500" ]; then
      fail "${ep} expected HTTP 500, got ${code}"
    fi
    log "  ${ep}: HTTP ${code} (failing as expected)"
  done

  # 3. Wait for Prometheus to fire the alerts.
  wait_for_alerts

  # 4. Fetch and validate the query-results snapshot before any document
  # mutation; a fetch failure stops here without creating a document.
  mkdir -p "$TMP_DIR"
  prepare_matcher_payload "$since"

  # 5. Create the infrapad document.
  log "Creating infrapad document..."

  local create_output
  create_output=$(${INFRAPAD_CLI} document create \
    --title "Monitored app incident on $(date '+%Y-%m-%d %H:%M:%S')" \
    --namespace "monitored-app" \
    -o json)

  local doc_name
  doc_name=$(echo "$create_output" | jq -r '.name')
  log "  Document created: ${doc_name}"

  # Save doc name for later use by resolve.
  rm -f "$SLACK_BLOCK_FILE" "$SLACK_PAYLOAD_FILE"
  echo "$doc_name" > "$DOC_NAME_FILE"

  # Add alerts_matcher block.
  local content
  content=$(jq -n --arg since "$since" '{
    labels_matchers: [
      {alertname: ["MonitoredAppEndpointDown"], endpoint: ["endpoint1"]},
      {alertname: ["MonitoredAppEndpointDown"], endpoint: ["endpoint2"]}
    ],
    since: $since,
    until: "0001-01-01T00:00:00Z"
  }')

  local matcher_number
  matcher_number=$(${INFRAPAD_CLI} block add \
    --document "$doc_name" \
    --type Prometheus/AlertsMatcher \
    --content "$content" \
    -o json | jq -er '.blockNumber')

  log "  alerts_matcher block ${matcher_number} added."
  submit_matcher_payload "$doc_name" "$matcher_number"

  # 6. Record the initial incident report from the triggered alerts.
  local notes
  notes=$(cat <<'EOF'
# Monitored app incident

Prometheus is firing `MonitoredAppEndpointDown` alerts for both `endpoint1`
and `endpoint2`. Both endpoints are returning HTTP 500.

The cause and scope of the incident have not yet been determined.
EOF
)
  ${INFRAPAD_CLI} block add --document "$doc_name" --type Core/Markdown \
    --content "$(jq -n --arg text "$notes" '{text: $text}')"

  # 7. Create a Slack primary and submit two complete payloads. The second
  # enriches an existing message and adds interleaved human/agent replies.
  # Start Slack after the matcher, notes, and thread reference, rather than
  # backdating its messages to the beginning of the alert query window.
  local thread_ts thread_output thread_number slack_since
  thread_ts=$(date -u +%s)
  thread_output=$(${INFRAPAD_CLI} block add --document "$doc_name" --type Slack/Thread \
    --content "$(jq -n --arg url "https://slack.example/archives/CMONITOREDAPP/p${thread_ts}000000" '{url: $url}')" \
    -o json)
  thread_number=$(echo "$thread_output" | jq -er '.blockNumber')
  echo "$thread_number" > "$SLACK_BLOCK_FILE"
  slack_since=$(echo "$thread_output" | jq -er '.record.timestamp')
  update_slack_payload initial "$slack_since"
  submit_slack_payload "$doc_name" "$thread_number"
  update_slack_payload triage "$slack_since"
  submit_slack_payload "$doc_name" "$thread_number"
  log "  Simulated Slack conversation and AI-agent triage added (including revision history)."

  # 8. Pull the complete document to markdown.
  ${INFRAPAD_CLI} md pull --document "$doc_name" --file "$INCIDENT_FILE"

  log "Incident started. Document written to ${INCIDENT_FILE}"
  log "Open ${INFRAPAD_API_URL:-http://localhost:8089}/ui/${doc_name} to view the cards and the stored alert snapshot."

  if [ -n "${AUTH_USE_ENV}" ]; then
      echo "For authentication, source .env file before running the agent: \`source .env; AGENT_CMD\`"
  fi
}

# ─── resolve ───────────────────────────────────────────────────────────────

cmd_resolve() {
  if [ ! -f "$DOC_NAME_FILE" ]; then
    fail "No active incident found. Run 'simulate.sh start' first."
  fi

  local doc_name
  doc_name=$(cat "$DOC_NAME_FILE")
  log "Resolving incident for document: ${doc_name}"

  # 1. Resolve the incident (set endpoints back to ok).
  log "Setting both endpoints to ok..."
  set_endpoint endpoint1 ok
  set_endpoint endpoint2 ok

  for ep in endpoint1 endpoint2; do
    local code
    code=$(check_endpoint "$ep")
    if [ "$code" != "200" ]; then
      fail "${ep} expected HTTP 200, got ${code}"
    fi
    log "  ${ep}: HTTP ${code} (recovered)"
  done

  # 2. Wait for Prometheus alerts to clear.
  wait_for_alerts_resolved

  # 3. Prepare a complete replacement snapshot from the existing matcher's
  # since before changing document content. The incident until is captured
  # first so that the snapshot window (ending at fetch time) covers it.
  local until
  until=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
  find_matcher "$doc_name"
  prepare_matcher_payload "$MATCHER_SINCE"

  # 4. Update the alert matcher's Until field. This metadata change clears
  # the stored query results until the prepared snapshot is submitted.
  log "Updating alerts_matcher block with Until timestamp..."

  # Pull latest to get current state.
  ${INFRAPAD_CLI} md pull --document "$doc_name" --file "$INCIDENT_FILE"

  # Select the alert matcher by type, not by document position: Slack's
  # event timestamps can put agent/thread headers before the metric block.
  # The server omits an unset (zero) until, so replace any existing value
  # and otherwise add it inside content next to since.
  awk -v until="$until" '
    /^::infrapad_block\{/ { target = ($0 ~ / type=Prometheus\/AlertsMatcher( |})/) }
    !done && target && /^```yaml infrapad/ { inhdr = 1; print; next }
    inhdr && /^[[:space:]]*until:/ { next }
    inhdr && /^```/ {
      print indent "until: \"" until "\""
      inhdr = 0; done = 1; print; next
    }
    inhdr && match($0, /^[[:space:]]*since:/) { indent = substr($0, 1, RLENGTH - 6) }
    { print }
    END { if (!done) exit 1 }
  ' "$INCIDENT_FILE" > "${INCIDENT_FILE}.tmp" || fail "Could not locate the Prometheus alert matcher header."
  mv "${INCIDENT_FILE}.tmp" "$INCIDENT_FILE"

  # The server applies only the changed alert matcher header.
  ${INFRAPAD_CLI} md push --file "$INCIDENT_FILE"
  log "  alerts_matcher block updated."

  # 5. Submit the replacement snapshot to the same matcher block.
  submit_matcher_payload "$doc_name" "$MATCHER_NUMBER"

  # 6. Refresh the complete Slack payload with human/agent recovery replies.
  # Older incidents created before the Slack simulation have no saved state.
  if [ -f "$SLACK_BLOCK_FILE" ] && [ -f "$SLACK_PAYLOAD_FILE" ]; then
    local thread_number
    thread_number=$(<"$SLACK_BLOCK_FILE")
    update_slack_payload resolved "$until"
    submit_slack_payload "$doc_name" "$thread_number"
    log "  Slack conversation updated with recovery and verification replies."
  fi

  # The snapshot (and generated Slack blocks) changed revisions; refresh
  # before appending ordinary Markdown. Pushing the unchanged matcher header
  # back preserves its stored query results.
  ${INFRAPAD_CLI} md pull --document "$doc_name" --file "$INCIDENT_FILE"

  # 7. Add a markdown block about the alerts being resolved.
  log "Adding resolution markdown block..."

  # Append a new markdown block to the file.
  # Remove trailing blank lines, then append the resolution block.
  sed -i -e :a -e '/^\s*$/{ $d; N; ba; }' "$INCIDENT_FILE"
  cat >> "$INCIDENT_FILE" <<EOF
::infrapad_block{type=Core/Markdown block=new}
# Incident resolved

The alerts have been resolved at ${until}. Both endpoints are back to healthy state.

- endpoint1: recovered
- endpoint2: recovered
EOF

  ${INFRAPAD_CLI} md push --file "$INCIDENT_FILE"
  log "  Resolution block added."

  log "Incident resolved. Document updated at ${INCIDENT_FILE}"
}

# ─── refresh ───────────────────────────────────────────────────────────────

# Re-fetch and resubmit the alert matcher snapshot of an existing incident
# (ongoing or resolved) without any other document or endpoint change.
cmd_refresh() {
  local md_file="${1:-$INCIDENT_FILE}"
  [ -f "$md_file" ] || \
    fail "Markdown document ${md_file} not found. Pass a pulled incident file or run 'simulate.sh start' first."
  local doc_id doc_name
  doc_id=$(markdown_document_id "$md_file") || \
    fail "Could not read the document id from the front matter of ${md_file}."
  doc_name="documents/${doc_id}"
  log "Refreshing the alert matcher snapshot for document: ${doc_name}"

  find_matcher "$doc_name"
  mkdir -p "$TMP_DIR"
  prepare_matcher_payload "$MATCHER_SINCE"
  submit_matcher_payload "$doc_name" "$MATCHER_NUMBER"

  log "Snapshot refreshed. Open ${INFRAPAD_API_URL:-http://localhost:8089}/ui/${doc_name} to view it."
}

# Print the `document:` id from the Markdown file's YAML front matter.
markdown_document_id() {
  awk '
    NR == 1 { if ($0 != "---") exit 1; next }
    /^---[[:space:]]*$/ { exit 1 }
    /^document:/ {
      sub(/^document:[[:space:]]*/, ""); gsub(/["\047[:space:]]/, "")
      sub(/^documents\//, "")
      if ($0 == "") exit 1
      print; found = 1; exit
    }
    END { exit !found }
  ' "$1"
}

# ─── Slack payload producer and transport ──────────────────────────────────

update_slack_payload() {
  local stage="$1" at="$2"
  python3 "${SCRIPT_DIR}/simulate-slack.py" "$stage" "$SLACK_PAYLOAD_FILE" \
    --at "$at" --app-url "$MONITORED_APP_URL" --prometheus-url "$PROMETHEUS_URL"
}

submit_slack_payload() {
  submit_payload "$1" "$2" "$SLACK_PAYLOAD_FILE" "Slack"
}

# ─── AlertsMatcher snapshot producer and transport ─────────────────────────

# Set MATCHER_NUMBER and MATCHER_SINCE from the document's (first,
# non-generated) Prometheus/AlertsMatcher block.
find_matcher() {
  local doc_name="$1" matcher
  matcher=$(${INFRAPAD_CLI} block list --document "$doc_name" -o json | jq -ec \
    'first(.[] | select(.type == "Prometheus/AlertsMatcher" and .parentBlockNumber == null))') || \
    fail "Could not locate the Prometheus alert matcher in ${doc_name}."
  MATCHER_NUMBER=$(echo "$matcher" | jq -er '.blockNumber')
  MATCHER_SINCE=$(echo "$matcher" | jq -er '.record.content.since') || \
    fail "The Prometheus alert matcher in ${doc_name} has no since."
}

prepare_matcher_payload() {
  local since="$1"
  log "Fetching Prometheus query results for the alert matcher snapshot..."
  python3 "${SCRIPT_DIR}/simulate-snapshot.py" "$MATCHER_PAYLOAD_FILE" \
    --since "$since" --prometheus-url "$PROMETHEUS_URL" \
    --backfill-seconds "$SNAPSHOT_BACKFILL_SECONDS" || \
    fail "Could not prepare the alert matcher snapshot; the document was not changed."
  log "  Snapshot saved to ${MATCHER_PAYLOAD_FILE}."
}

submit_matcher_payload() {
  submit_payload "$1" "$2" "$MATCHER_PAYLOAD_FILE" "Alert matcher snapshot"
  log "  Snapshot submitted to alerts_matcher block $2."
}

submit_payload() {
  local doc_name="$1" block_number="$2" payload_file="$3" label="$4"
  local api_url="${INFRAPAD_API_URL:-http://localhost:8089}"
  local url="${api_url%/}/v1/${doc_name}/blocks/${block_number}:submitPayload"
  local response
  local -a auth_headers=()
  if [ -n "${INFRAPAD_TOKEN:-}" ]; then
    auth_headers=(-H "Authorization: Bearer ${INFRAPAD_TOKEN}")
  fi
  # The CLI has no payload-submission command yet. Use the public REST API
  # with exactly the same base URL and bearer token as the CLI calls.
  if ! response=$(curl --fail-with-body --silent --show-error \
    --connect-timeout 5 --max-time 30 \
    "${auth_headers[@]}" -H 'Content-Type: application/json' \
    --data-binary "@${payload_file}" "$url"); then
    echo "ERROR: ${label} payload submission failed: ${response}" >&2
    echo "       Target: ${url}" >&2
    echo "       The prepared payload is kept in ${payload_file}; retry with:" >&2
    echo "       curl --fail-with-body -H 'Authorization: Bearer \$INFRAPAD_TOKEN' -H 'Content-Type: application/json' --data-binary @${payload_file} ${url}" >&2
    exit 1
  fi
}

# ─── main ──────────────────────────────────────────────────────────────────

case "${1:-}" in
  start)
    configure_infrapad_auth
    cmd_start
    ;;
  resolve)
    configure_infrapad_auth
    cmd_resolve
    ;;
  refresh)
    configure_infrapad_auth
    cmd_refresh "${2:-}"
    ;;
  *)
    echo "Usage: $0 {start|resolve|refresh [incident.md]}" >&2
    exit 1
    ;;
esac
