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

MONITORED_APP_URL="${MONITORED_APP_URL:-http://localhost:8080}"
PROMETHEUS_URL="${PROMETHEUS_URL:-http://localhost:9090}"

INFRAPAD_PROJECT_DIR="${INFRAPAD_PROJECT_DIR:-$(cd "${SCRIPT_DIR}/../../.." && pwd)}"

# ─── helpers ────────────────────────────────────────────────────────────────

log() { echo "==> $*"; }

fail() { echo "ERROR: $*" >&2; exit 1; }

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
  create_output=$(${INFRAPAD_CLI} doc create \
    --title "Monitored app incident on $(date '+%Y-%m-%d %H:%M:%S')" \
    --namespace "monitored-app" \
    -o json)

  local doc_name
  doc_name=$(echo "$create_output" | jq -r '.name')
  log "  Document created: ${doc_name}"

  # Save doc name for later use by incident-resolve.
  mkdir -p "$TMP_DIR"
  echo "$doc_name" > "$DOC_NAME_FILE"

  # Add alerts_matcher block.
  local content
  content=$(jq -n --arg since "$since" '{
    LabelsMatchers: [
      {name: ["MonitoredAppEndpointDown"], endpoint: ["endpoint1"]},
      {name: ["MonitoredAppEndpointDown"], endpoint: ["endpoint2"]}
    ],
    Since: $since,
    Until: "0001-01-01T00:00:00Z"
  }')

  ${INFRAPAD_CLI} block add \
    --doc "$doc_name" \
    --type alerts_matcher \
    --content "$content"

  log "  alerts_matcher block added."

  # 5. Pull the document to markdown.
  ${INFRAPAD_CLI} md pull --doc "$doc_name" --file "$INCIDENT_FILE"

  log "Incident started. Document written to ${INCIDENT_FILE}"
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

  # 3. Update the first block's Until field to the current time.
  log "Updating alerts_matcher block with Until timestamp..."

  local until
  until=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

  # Pull latest to get current state.
  ${INFRAPAD_CLI} md pull --doc "$doc_name" --file "$INCIDENT_FILE"

  # Read the Since value from the current file.
  local since
  since=$(grep -oP 'Since:\s*"?\K[^"\n]+' "$INCIDENT_FILE" | head -1 | tr -d '[:space:]')
  since=${since:-0001-01-01T00:00:00Z}

  # Update the Until in the markdown file.
  sed -i 's|Until:\s*"\?0001-01-01T00:00:00Z"\?|Until: "'"${until}"'"|' "$INCIDENT_FILE"

  # Push the updated block 1.
  ${INFRAPAD_CLI} md push --file "$INCIDENT_FILE"
  log "  alerts_matcher block updated."

  # 4. Add a markdown block about the alerts being resolved.
  log "Adding resolution markdown block..."

  # Append a new markdown block to the file.
  # Remove trailing blank lines, then append the resolution block.
  sed -i -e :a -e '/^\s*$/{ $d; N; ba; }' "$INCIDENT_FILE"
  cat >> "$INCIDENT_FILE" <<EOF
::infrapad_block{type=markdown block=new}
# Incident resolved

The alerts have been resolved at ${until}. Both endpoints are back to healthy state.

- endpoint1: recovered
- endpoint2: recovered
EOF

  ${INFRAPAD_CLI} md push --file "$INCIDENT_FILE"
  log "  Resolution block added."

  log "Incident resolved. Document updated at ${INCIDENT_FILE}"
}

# ─── main ──────────────────────────────────────────────────────────────────

case "${1:-}" in
  incident-start)
    cmd_incident_start
    ;;
  incident-resolve)
    cmd_incident_resolve
    ;;
  *)
    echo "Usage: $0 {incident-start|incident-resolve}" >&2
    exit 1
    ;;
esac
