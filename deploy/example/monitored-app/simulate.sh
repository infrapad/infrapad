#!/usr/bin/env bash
#
# simulate.sh — Monitored app incident simulation
#
# Usage:
#   simulate.sh incident-start
#   simulate.sh incident-resolve
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TMP_DIR="${SCRIPT_DIR}/tmp"
INCIDENT_FILE="${TMP_DIR}/incident.md"
DOC_NAME_FILE="${TMP_DIR}/.doc_name"
SLACK_BLOCK_FILE="${TMP_DIR}/.slack_block"
SLACK_SNAPSHOT_FILE="${TMP_DIR}/slack-snapshot.json"
INFRAPAD_ENV_FILE="${SCRIPT_DIR}/.env"

MONITORED_APP_URL="${MONITORED_APP_URL:-http://localhost:8080}"
PROMETHEUS_URL="${PROMETHEUS_URL:-http://localhost:9090}"

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

wait_for_alerts() {
  local max_wait=120
  local interval=5
  local elapsed=0
  log "Waiting for Prometheus to fire alerts for both endpoints..."
  while [ "$elapsed" -lt "$max_wait" ]; do
    local firing
    firing=$(curl -s "${PROMETHEUS_URL}/api/v1/alerts" | jq \
      '[.data.alerts[]
        | select(.state == "firing" and .labels.alertname == "MonitoredAppEndpointDown")
        | .labels.endpoint] | unique | length' 2>/dev/null) || firing=0
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
    firing=$(curl -s "${PROMETHEUS_URL}/api/v1/alerts" | jq \
      '[.data.alerts[]
        | select(.state == "firing" and .labels.alertname == "MonitoredAppEndpointDown")
        | .labels.endpoint] | unique | length' 2>/dev/null) || firing=0
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

# ─── incident-start ────────────────────────────────────────────────────────

cmd_incident_start() {
  local since
  since=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

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

  # 4. Create the infrapad document.
  log "Creating infrapad document..."

  local create_output
  create_output=$(${INFRAPAD_CLI} document create \
    --title "Monitored app incident on $(date '+%Y-%m-%d %H:%M:%S')" \
    --namespace "monitored-app" \
    -o json)

  local doc_name
  doc_name=$(echo "$create_output" | jq -r '.name')
  log "  Document created: ${doc_name}"

  # Save doc name for later use by incident-resolve.
  mkdir -p "$TMP_DIR"
  rm -f "$SLACK_BLOCK_FILE" "$SLACK_SNAPSHOT_FILE"
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

  ${INFRAPAD_CLI} block add \
    --document "$doc_name" \
    --type Prometheus/AlertsMatcher \
    --content "$content"

  log "  alerts_matcher block added."

  # 5. Record the initial incident report from the triggered alerts.
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

  # 6. Create a Slack primary and submit two complete snapshots. The second
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
  update_slack_snapshot initial "$slack_since"
  submit_slack_snapshot "$doc_name" "$thread_number"
  update_slack_snapshot triage "$slack_since"
  submit_slack_snapshot "$doc_name" "$thread_number"
  log "  Simulated Slack conversation and AI-agent triage added (including revision history)."

  # 7. Pull the complete document to markdown.
  ${INFRAPAD_CLI} md pull --document "$doc_name" --file "$INCIDENT_FILE"

  log "Incident started. Document written to ${INCIDENT_FILE}"
  log "Open ${INFRAPAD_API_URL:-http://localhost:8089}/ui/${doc_name} to view the cards and live chart."

  if [ -n "${AUTH_USE_ENV}" ]; then
      echo "For authentication, source .env file before running the agent: \`source .env; AGENT_CMD\`"
  fi
}

# ─── incident-resolve ──────────────────────────────────────────────────────

cmd_incident_resolve() {
  if [ ! -f "$DOC_NAME_FILE" ]; then
    fail "No active incident found. Run 'simulate.sh incident-start' first."
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

  # 3. Update the alert matcher's Until field to the current time.
  log "Updating alerts_matcher block with Until timestamp..."

  local until
  until=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

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

  # 4. Refresh the complete Slack snapshot with human/agent recovery replies.
  # Older incidents created before the Slack simulation have no saved state.
  if [ -f "$SLACK_BLOCK_FILE" ] && [ -f "$SLACK_SNAPSHOT_FILE" ]; then
    local thread_number
    thread_number=$(<"$SLACK_BLOCK_FILE")
    update_slack_snapshot resolved "$until"
    submit_slack_snapshot "$doc_name" "$thread_number"
    # Generated blocks changed; refresh before appending ordinary Markdown.
    ${INFRAPAD_CLI} md pull --document "$doc_name" --file "$INCIDENT_FILE"
    log "  Slack conversation updated with recovery and verification replies."
  fi

  # 5. Add a markdown block about the alerts being resolved.
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

# ─── Slack snapshot producer and transport ──────────────────────────────────

update_slack_snapshot() {
  local stage="$1" at="$2"
  python3 "${SCRIPT_DIR}/simulate-slack.py" "$stage" "$SLACK_SNAPSHOT_FILE" \
    --at "$at" --app-url "$MONITORED_APP_URL" --prometheus-url "$PROMETHEUS_URL"
}

submit_slack_snapshot() {
  local doc_name="$1" block_number="$2"
  local api_url="${INFRAPAD_API_URL:-http://localhost:8089}"
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
    --data-binary "@${SLACK_SNAPSHOT_FILE}" \
    "${api_url%/}/v1/${doc_name}/blocks/${block_number}:submitPayload"); then
    fail "Slack payload submission failed: ${response}"
  fi
}

# ─── main ──────────────────────────────────────────────────────────────────

case "${1:-}" in
  incident-start)
    configure_infrapad_auth
    cmd_incident_start
    ;;
  incident-resolve)
    configure_infrapad_auth
    cmd_incident_resolve
    ;;
  *)
    echo "Usage: $0 {incident-start|incident-resolve}" >&2
    exit 1
    ;;
esac
