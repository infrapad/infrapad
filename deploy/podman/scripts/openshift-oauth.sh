#!/usr/bin/env bash
# OpenShift OAuth setup for local development.
#
# Subcommands:
#   bootstrap                        Idempotent overall reconciliation (default
#                                    when called without arguments): namespace,
#                                    service account, RBAC, token Secret,
#                                    delegation kubeconfig, TLS, cookie secret,
#                                    restricted OAuthClient, and the
#                                    rendered oauth-proxy config. Supports cached
#                                    offline restarts.
#   refresh-ca                       Refresh only the public ingress CA,
#                                    preserving the OAuth cache.
#   client <setup|validate-cache|cleanup>
#                                    Guarded lifecycle of the restricted
#                                    OAuthClient.
#
# Produces all generated files under deploy/podman/generated/ and reconciles
# cluster objects in the namespace identified by the current kubeconfig
# context.

set -Eeuo pipefail
umask 077 # Protect new temporary credentials before they are moved into place.

# ---------------------------------------------------------------------------
# Paths
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
GEN_DIR="$DEPLOY_DIR/generated"
CONFIG_TEMPLATE="$DEPLOY_DIR/oauth-proxy.cfg.template"
METADATA_FILE="$GEN_DIR/oauth-metadata.json"
INFRAPAD_AUTH_UPSTREAM="${INFRAPAD_AUTH_UPSTREAM:-http://127.0.0.1:8088}"
INGRESS_CA_FILE="$GEN_DIR/ingress-ca.crt"

# The proxy service-account token used to build the delegation kubeconfig.
# Distinct from oauth-client-secret, which holds the OAuthClient secret.
PROXY_SA_TOKEN_FILE="$GEN_DIR/proxy-sa-token"
# Pre-rename filename for the same proxy-SA token; migrated in place once.
LEGACY_SA_TOKEN_FILE="$GEN_DIR/client-secret"

# Cluster-side names
NAMESPACE="infrapad-local"
SA_NAME="infrapad-oauth-proxy"
TOKEN_SECRET="infrapad-oauth-proxy-token"

# API URL is resolved by discovery (online bootstrap) or from cached metadata
# (client subcommand and cached offline path).
API_URL=""

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
usage() {
  echo "Usage: $0 [bootstrap|refresh-ca|client <setup|validate-cache|cleanup>]"
  echo "Runs 'bootstrap' when invoked without arguments."
}

die() { echo "ERROR: $*" >&2; exit 1; }

require_tool() {
  command -v "$1" &>/dev/null || die "'$1' is required but not found in PATH."
}

# Runtime state for the OAuthClient lock/work directories and mkcert output.
# A single EXIT trap cleans up whatever is currently held.
LOCK_DIR=""
WORK_DIR=""
MKCERT_TMP=""
cleanup_runtime() {
  if [ -n "$WORK_DIR" ]; then rm -rf "$WORK_DIR"; fi
  if [ -n "$LOCK_DIR" ]; then rmdir "$LOCK_DIR" 2>/dev/null || true; fi
  if [ -n "$MKCERT_TMP" ]; then rm -rf "$MKCERT_TMP"; fi
}
trap cleanup_runtime EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

load_oauth_metadata() {
  local values
  values="$(jq -er '
    [.apiUrl, .authorizationEndpoint, .tokenEndpoint]
    | if all(.[]; type == "string" and length > 0)
      then @tsv
      else error("OAuth metadata fields must be non-empty strings")
      end
  ' "$METADATA_FILE")" || return 1

  IFS=$'\t' read -r API_URL AUTHORIZATION_ENDPOINT TOKEN_ENDPOINT <<< "$values"
}

write_oauth_metadata() {
  jq -n \
    --arg api_url "$API_URL" \
    --arg authorization_endpoint "$AUTHORIZATION_ENDPOINT" \
    --arg token_endpoint "$TOKEN_ENDPOINT" \
    '{
      apiUrl: $api_url,
      authorizationEndpoint: $authorization_endpoint,
      tokenEndpoint: $token_endpoint,
      delegationReady: true,
      oauthClientReady: true
    }' > "$METADATA_FILE.tmp"
  chmod 0644 "$METADATA_FILE.tmp"
  mv -f "$METADATA_FILE.tmp" "$METADATA_FILE"
}

render_oauth_proxy_config() {
  [ -r "$CONFIG_TEMPLATE" ] || die "OAuth proxy config template ($CONFIG_TEMPLATE) is not readable."

  echo "Writing oauth-proxy.cfg…"
  jq -Rrs \
    --arg api_url "$API_URL" \
    --arg authorization_endpoint "$AUTHORIZATION_ENDPOINT" \
    --arg token_endpoint "$TOKEN_ENDPOINT" \
    --arg auth_upstream "$INFRAPAD_AUTH_UPSTREAM" \
    '
      gsub("@API_URL@"; $api_url)
      | gsub("@AUTHORIZATION_ENDPOINT@"; $authorization_endpoint)
      | gsub("@TOKEN_ENDPOINT@"; $token_endpoint)
      | gsub("@INFRAPAD_AUTH_UPSTREAM@"; $auth_upstream)
      | if contains("@API_URL@")
          or contains("@AUTHORIZATION_ENDPOINT@")
          or contains("@TOKEN_ENDPOINT@")
          or contains("@INFRAPAD_AUTH_UPSTREAM@")
        then error("unresolved placeholder in OAuth proxy config template")
        else .
        end
    ' "$CONFIG_TEMPLATE" > "$GEN_DIR/oauth-proxy.cfg.tmp"
  chmod 0644 "$GEN_DIR/oauth-proxy.cfg.tmp"
  mv -f "$GEN_DIR/oauth-proxy.cfg.tmp" "$GEN_DIR/oauth-proxy.cfg"
}

# ---------------------------------------------------------------------------
# Proxy-SA token file migration
# ---------------------------------------------------------------------------
# One-time in-place migration of the legacy 'client-secret' filename (which
# always held the proxy service-account token) to 'proxy-sa-token'. Existing
# caches keep working offline; the file is not mounted anywhere.
migrate_proxy_sa_token() {
  if [ ! -e "$PROXY_SA_TOKEN_FILE" ] && [ -e "$LEGACY_SA_TOKEN_FILE" ]; then
    mv "$LEGACY_SA_TOKEN_FILE" "$PROXY_SA_TOKEN_FILE" \
      || die "Could not migrate '$LEGACY_SA_TOKEN_FILE' to '$PROXY_SA_TOKEN_FILE'."
    echo "Migrated legacy '$LEGACY_SA_TOKEN_FILE' to '$PROXY_SA_TOKEN_FILE'."
  fi
}

# The pre-OAuthClient cache is sufficient for an artifact-preserving migration.
has_delegation_cache() {
  migrate_proxy_sa_token
  local artifact
  local artifacts=(
    "$METADATA_FILE"
    "$PROXY_SA_TOKEN_FILE"
    "$GEN_DIR/kubeconfig"
    "$GEN_DIR/cluster-ca.crt"
    "$GEN_DIR/cookie-secret"
    "$GEN_DIR/tls.crt"
    "$GEN_DIR/tls.key"
  )

  for artifact in "${artifacts[@]}"; do
    [ -s "$artifact" ] || return 1
  done
  # Older caches contain a host-user kubeconfig and cannot enable delegation.
  jq -e '.delegationReady == true' "$METADATA_FILE" &>/dev/null
}

# oauthClientReady = OAuthClient registered. Caches from before the rename
# (or without any OAuth client) fail completeness and take the one-time online
# migration path in the bootstrap flow.
has_complete_cache() {
  has_delegation_cache \
    && [ -s "$GEN_DIR/oauth-client-secret" ] \
    && [ -s "$GEN_DIR/oauth-client-state.json" ] \
    && jq -e '.oauthClientReady == true' "$METADATA_FILE" &>/dev/null
}

# ---------------------------------------------------------------------------
# Opt-in ingress CA preparation helpers
# ---------------------------------------------------------------------------

# Fetch the public default-ingress CA from the cluster and write it atomically.
# Uses the host kubeconfig (not the proxy SA kubeconfig). The ConfigMap is public.
fetch_ingress_ca() {
  echo "Fetching public default-ingress CA for metrics upstream TLS…"
  local ca_pem
  ca_pem="$(oc -n openshift-config-managed get configmap default-ingress-cert \
    -o jsonpath='{.data.ca-bundle\.crt}' 2>/dev/null)" \
    || die "Cannot read openshift-config-managed/default-ingress-cert ConfigMap." \
           "Verify cluster connectivity and that your kubeconfig context has read access."
  [ -n "$ca_pem" ] || die "default-ingress-cert ConfigMap returned empty ca-bundle.crt."
  # Validate usable PEM material.
  echo "$ca_pem" | openssl x509 -noout 2>/dev/null \
    || die "Fetched CA bundle does not contain valid PEM certificate material."
  echo "$ca_pem" > "$INGRESS_CA_FILE.tmp"
  chmod 0644 "$INGRESS_CA_FILE.tmp"
  mv -f "$INGRESS_CA_FILE.tmp" "$INGRESS_CA_FILE"
  echo "Wrote ingress CA to $INGRESS_CA_FILE"
}

# Check whether metrics upstream is configured (opt-in).
metrics_upstream_enabled() {
  [ -n "${INFRAPAD_PROMETHEUS_UPSTREAM_URL:-}" ]
}

# Ensure ingress CA is available when metrics upstream is enabled.
# If the user supplied an explicit CA file, skip generated trust entirely.
prepare_metrics_trust() {
  if ! metrics_upstream_enabled; then
    return 0
  fi
  # Reuse existing generated trust if present and valid.
  if [ -s "$INGRESS_CA_FILE" ]; then
    if openssl x509 -in "$INGRESS_CA_FILE" -noout 2>/dev/null; then
      echo "Reusing existing generated ingress CA: $INGRESS_CA_FILE"
      return 0
    fi
    echo "Existing ingress CA is invalid; will re-fetch." >&2
  fi
  fetch_ingress_ca
}

# Offline check: fail clearly when metrics trust is missing.
check_metrics_trust_offline() {
  if ! metrics_upstream_enabled; then
    return 0
  fi
  if [ -s "$INGRESS_CA_FILE" ] && openssl x509 -in "$INGRESS_CA_FILE" -noout 2>/dev/null; then
    echo "Reusing existing generated ingress CA: $INGRESS_CA_FILE"
    return 0
  fi
  die "Metrics upstream is configured but no ingress CA is available offline." \
      "Run the bootstrap while connected to the cluster, or supply" \
      "INFRAPAD_PROMETHEUS_UPSTREAM_CA_FILE with an appropriate CA bundle."
}

# ---------------------------------------------------------------------------
# Restricted OAuthClient (guarded lifecycle)
# ---------------------------------------------------------------------------
# Why a separate regular OAuthClient instead of just the proxy service
# account: an SA-backed OAuth client can only be granted basic user scopes
# and role scopes within its own namespace, so it cannot obtain
# role:cluster-monitoring-view:openshift-monitoring. This restricted regular
# OAuthClient with explicit scopeRestrictions and prompted consent is the
# only way to grant that scope without RBAC changes.
OAUTH_CLIENT_ID="infrapad-local-login"
OAUTH_CALLBACK="https://localhost:8443/oauth/callback"
OAUTH_CLIENT_OWNER="infrapad.io/local-monitoring-client-owner"
OAUTH_CLIENT_RESOURCE="/apis/oauth.openshift.io/v1/oauthclients/$OAUTH_CLIENT_ID"
OAUTH_CLIENT_RESTRICTIONS='[
  {"literals": ["user:info", "user:check-access"]},
  {"clusterRole": {"roleNames": ["cluster-monitoring-view"],
                   "namespaces": ["openshift-monitoring"], "allowEscalation": false}}
]'
OAUTH_CLIENT_STATE_FILE="$GEN_DIR/oauth-client-state.json"
OAUTH_CLIENT_SECRET_FILE="$GEN_DIR/oauth-client-secret"

# GNU and BSD stat use different formats; support both local development hosts.
owner_mode() {
  stat -c '%u %a' "$1" 2>/dev/null || stat -f '%u %Lp' "$1" 2>/dev/null
}
private_file() {
  [ -f "$1" ] && [ ! -L "$1" ] && [ "$(owner_mode "$1")" = "$EUID 600" ] \
    || die "Client state/secret must be owned regular files with mode 0600."
}

# Prepare the guarded client context: validate the generated directory,
# resolve the API URL, and take the atomic lock. The ERR trap converts
# unexpected local failures into a retained-assets error, matching the
# original standalone client script's behavior.
oauth_client_begin() {
  require_tool jq
  require_tool openssl

  [ -d "$GEN_DIR" ] && [ ! -L "$GEN_DIR" ] && [ "$(owner_mode "$GEN_DIR")" = "$EUID 700" ] \
    || die "Generated directory must be owned and mode 0700, not a symlink."
  GEN_DIR="$(cd "$GEN_DIR" && pwd -P)"

  if [ -z "$API_URL" ]; then
    API_URL="$(jq -er '.apiUrl | strings | select(length > 0)' "$METADATA_FILE" 2>/dev/null)" \
      || die "Generated OAuth metadata is unreadable or invalid."
  fi
  while [[ "$API_URL" == */ ]]; do API_URL="${API_URL%/}"; done
  [[ "$API_URL" =~ ^https://(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9._-]+)(:[0-9]+)?$ ]] \
    || die "A fixed HTTPS cluster API URL is required."

  # An atomic directory lock avoids platform-specific flock/Python dependencies.
  LOCK_DIR="$GEN_DIR/.oauth-client.lock.d"
  mkdir "$LOCK_DIR" 2>/dev/null \
    || die "Concurrent operation or interrupted-operation lock; inspect .oauth-client.lock.d."
  WORK_DIR="$(mktemp -d "$GEN_DIR/.oauth-client.XXXXXX")"
  CLIENT_FILE="$WORK_DIR/client.json"

  trap 'die "Unreadable/invalid private state or failed local operation; retained assets require inspection."' ERR
}

# Release the guarded client context after a successful action.
oauth_client_end() {
  rm -rf "$WORK_DIR"
  WORK_DIR=""
  rmdir "$LOCK_DIR" 2>/dev/null || true
  LOCK_DIR=""
  trap - ERR
}

# API objects and errors can contain the client secret. Capture privately, never echo.
api_call() {
  local output="$1"
  shift
  oc --request-timeout=20s --server="$API_URL" --insecure-skip-tls-verify=false \
    --certificate-authority="$GEN_DIR/cluster-ca.crt" "$@" > "$output" 2>/dev/null \
    || die "OAuthClient API operation failed; check host login, connectivity, verified API CA, and OAuthClient privileges. Private state retained."
}
check_host() {
  require_tool oc
  oc --request-timeout=20s config view --minify -o json > "$WORK_DIR/host.json" 2>/dev/null \
    || die "Host kubeconfig inspection failed; check the selected context."
  jq -e --arg api "$API_URL" \
    '.clusters[0].cluster.server | sub("/+$"; "") == $api' "$WORK_DIR/host.json" >/dev/null 2>&1 \
    || die "Host context and generated cache target different clusters; nothing was changed."
  [ -f "$GEN_DIR/cluster-ca.crt" ] || die "Matching API CA is missing; no insecure request was attempted."
}

load_state() {
  HAS_STATE=false
  if [ ! -e "$OAUTH_CLIENT_STATE_FILE" ] && [ ! -L "$OAUTH_CLIENT_STATE_FILE" ]; then
    [ ! -e "$OAUTH_CLIENT_SECRET_FILE" ] && [ ! -L "$OAUTH_CLIENT_SECRET_FILE" ] \
      || die "Client secret exists without ownership state; refusing to replace it."
    return
  fi
  private_file "$OAUTH_CLIENT_STATE_FILE"
  private_file "$OAUTH_CLIENT_SECRET_FILE"
  jq -e --arg api "$API_URL" --arg client_id "$OAUTH_CLIENT_ID" '
    .version == 1 and .api == $api and .client_id == $client_id
    and (.owner | type == "string" and length > 0) and has("uid")
    and (.uid == null or (.uid | type == "string" and length > 0))
  ' "$OAUTH_CLIENT_STATE_FILE" >/dev/null 2>&1 \
    || die "Client state belongs to another cluster/client or is invalid; nothing was changed." \
           "After a client-ID change, remove generated/oauth-client-state.json and generated/oauth-client-secret once, then rerun."
  [ -s "$OAUTH_CLIENT_SECRET_FILE" ] || die "Private client secret is empty."
  local digest
  digest="$(openssl dgst -sha256 "$OAUTH_CLIENT_SECRET_FILE" 2>/dev/null)"
  jq -e --arg hash "${digest##* }" '.secret_sha256 == $hash' "$OAUTH_CLIENT_STATE_FILE" >/dev/null 2>&1 \
    || die "Client secret does not match private state; refusing to rotate or adopt it."
  HAS_STATE=true
}

oauth_client_fetch() {
  api_call "$CLIENT_FILE" get oauthclient "$OAUTH_CLIENT_ID" --ignore-not-found -o json
  if [ ! -s "$CLIENT_FILE" ]; then printf 'null\n' > "$CLIENT_FILE"; fi
  jq -e 'type == "object" or . == null' "$CLIENT_FILE" >/dev/null 2>&1 \
    || die "Invalid API response; raw output suppressed to protect credentials."
}
oauth_client_exists() { jq -e '. != null' "$CLIENT_FILE" >/dev/null; }

oauth_client_prepare_manifest() {
  jq -n --slurpfile state "$OAUTH_CLIENT_STATE_FILE" --rawfile secret "$OAUTH_CLIENT_SECRET_FILE" \
    --arg client_id "$OAUTH_CLIENT_ID" --arg callback "$OAUTH_CALLBACK" --arg owner "$OAUTH_CLIENT_OWNER" \
    --argjson restrictions "$OAUTH_CLIENT_RESTRICTIONS" '{
      apiVersion: "oauth.openshift.io/v1", kind: "OAuthClient",
      metadata: {name: $client_id, annotations: {($owner): $state[0].owner}},
      secret: $secret, redirectURIs: [$callback], grantMethod: "prompt",
      scopeRestrictions: $restrictions, respondWithChallenges: false
    }' > "$WORK_DIR/manifest.json" 2>/dev/null
}
check_owned() {
  oauth_client_prepare_manifest
  jq -e --slurpfile state "$OAUTH_CLIENT_STATE_FILE" --slurpfile expected "$WORK_DIR/manifest.json" \
    --arg owner "$OAUTH_CLIENT_OWNER" '
      .metadata as $m | $expected[0] as $e |
      $m.name == $e.metadata.name and $m.annotations[$owner] == $state[0].owner
      and ($m.uid | type == "string" and length > 0)
      and ($m.resourceVersion | type == "string" and length > 0)
      and $m.deletionTimestamp == null
      and ($state[0].uid == null or $state[0].uid == $m.uid)
      and .secret == $e.secret and .redirectURIs == $e.redirectURIs
      and .grantMethod == $e.grantMethod and .scopeRestrictions == $e.scopeRestrictions
      and ((.additionalSecrets // []) | length == 0)
      and (.respondWithChallenges // false) == false
    ' "$CLIENT_FILE" >/dev/null 2>&1 \
    || die "Existing OAuthClient ownership/configuration mismatch; refusing to modify or delete it."
}

oauth_client_setup() {
  check_host
  load_state
  oauth_client_fetch
  if [ "$HAS_STATE" = false ]; then
    if oauth_client_exists; then die "OAuthClient already exists without matching private state; refusing to adopt it."; fi
    local secret owner digest
    secret="$(openssl rand -hex 48)"
    owner="$(openssl rand -hex 24)"
    digest="$(printf '%s' "$secret" | openssl dgst -sha256)"
    jq -n --arg api "$API_URL" --arg client_id "$OAUTH_CLIENT_ID" --arg owner "$owner" \
      --arg hash "${digest##* }" '{version: 1, api: $api, client_id: $client_id,
        owner: $owner, uid: null, secret_sha256: $hash}' > "$WORK_DIR/state.json"
    # Pending ownership precedes registration, allowing safe retry after API interruption.
    mv "$WORK_DIR/state.json" "$OAUTH_CLIENT_STATE_FILE"
    # oauth-proxy 5.0 reads the file verbatim. Exclusive creation, NO trailing newline.
    (set -o noclobber; printf '%s' "$secret" > "$OAUTH_CLIENT_SECRET_FILE") 2>/dev/null
    unset secret
  fi
  if ! oauth_client_exists; then
    jq -e '.uid == null' "$OAUTH_CLIENT_STATE_FILE" >/dev/null \
      || die "Registered OAuthClient disappeared; refusing to recreate it or rotate credentials."
    oauth_client_prepare_manifest
    api_call "$CLIENT_FILE" create -f - -o json < "$WORK_DIR/manifest.json"
  fi
  check_owned
  if jq -e '.uid == null' "$OAUTH_CLIENT_STATE_FILE" >/dev/null; then
    jq --slurpfile client "$CLIENT_FILE" '.uid = $client[0].metadata.uid' "$OAUTH_CLIENT_STATE_FILE" > "$WORK_DIR/state.json"
    mv "$WORK_DIR/state.json" "$OAUTH_CLIENT_STATE_FILE"
  fi
  echo "Restricted OAuthClient ready: $OAUTH_CLIENT_ID (prompted consent; no user RBAC changes)."
}

oauth_client_validate_cache() {
  load_state
  [ "$HAS_STATE" = true ] && jq -e '.uid != null' "$OAUTH_CLIENT_STATE_FILE" >/dev/null \
    || die "Login OAuth client cache is incomplete; run bootstrap online once."
  # Local integrity cannot establish that the cluster registration still exists.
}

oauth_client_cleanup() {
  check_host
  load_state
  oauth_client_fetch
  if [ "$HAS_STATE" = false ]; then
    if oauth_client_exists; then die "OAuthClient exists without ownership state; refusing to delete it."; fi
    echo "No deployment-owned OAuthClient/state to clean up."
    return
  fi
  if oauth_client_exists; then
    check_owned
    jq '{apiVersion: "v1", kind: "DeleteOptions", preconditions: {
      uid: .metadata.uid, resourceVersion: .metadata.resourceVersion}}
    ' "$CLIENT_FILE" > "$WORK_DIR/delete-options.json"
    api_call "$WORK_DIR/delete-response.json" delete --raw "$OAUTH_CLIENT_RESOURCE" -f - < "$WORK_DIR/delete-options.json"
    oauth_client_fetch
    if oauth_client_exists; then die "Client deletion is incomplete; state retained for an explicit retry."; fi
  fi
  rm "$OAUTH_CLIENT_SECRET_FILE" "$OAUTH_CLIENT_STATE_FILE"
  echo "Removed deployment-owned OAuthClient/secret/state only; proxy SA and other artifacts unchanged."
}

# Run one guarded OAuthClient action. Acquires the lock and ERR trap for the
# duration of the action; used by both the bootstrap flow and the `client`
# subcommand.
oauth_client() {
  local action="$1"
  oauth_client_begin
  case "$action" in
    setup) oauth_client_setup ;;
    validate-cache) oauth_client_validate_cache ;;
    cleanup) oauth_client_cleanup ;;
    *) die "Unknown OAuthClient action '$action'." ;;
  esac
  oauth_client_end
}

# ---------------------------------------------------------------------------
# refresh-ca subcommand
# ---------------------------------------------------------------------------
cmd_refresh_ca() {
  require_tool oc
  require_tool openssl
  echo "Refreshing ingress CA only (preserving OAuth cache)…"
  mkdir -p "$GEN_DIR"
  fetch_ingress_ca
  echo "Certificate refresh complete. Supply the path to INFRAPAD_PROMETHEUS_UPSTREAM_CA_FILE:"
  echo "  export INFRAPAD_PROMETHEUS_UPSTREAM_CA_FILE=\"$INGRESS_CA_FILE\""
}

# ---------------------------------------------------------------------------
# client subcommand
# ---------------------------------------------------------------------------
cmd_client() {
  local action="${1:-}"
  case "$action" in
    setup|validate-cache|cleanup) shift ;;
    *) usage >&2; exit 1 ;;
  esac
  [ "$#" -eq 0 ] || die "Unknown extra argument for 'client'."
  oauth_client "$action"
}

# ---------------------------------------------------------------------------
# bootstrap subcommand (default)
# ---------------------------------------------------------------------------
cmd_bootstrap() {
  # -----------------------------------------------------------------------
  # Cached offline path
  # -----------------------------------------------------------------------
  require_tool jq

  if has_delegation_cache; then
    if load_oauth_metadata; then
      if ! has_complete_cache; then
        echo "Migrating cached credentials to the restricted OAuthClient (online once)…"
        echo "Preserving proxy service-account credentials, cookie secret, certificates, and trust files."
        oauth_client setup
        write_oauth_metadata
      fi
      oauth_client validate-cache
      echo "Using cached OpenShift OAuth metadata and generated credentials."
      render_oauth_proxy_config
      check_metrics_trust_offline
      echo "OAuth bootstrap complete. Regenerated $GEN_DIR/oauth-proxy.cfg without contacting OpenShift."
      exit 0
    fi
    echo "Cached OAuth metadata is invalid; falling back to online bootstrap." >&2
  fi

  # -----------------------------------------------------------------------
  # Online prerequisite checks
  # -----------------------------------------------------------------------
  require_tool oc
  require_tool mkcert

  # KUBECONFIG must be readable and non-empty.
  if [ -z "${KUBECONFIG:-}" ]; then
    # oc/kubectl default
    KUBECONFIG="${HOME}/.kube/config"
  fi
  [ -r "$KUBECONFIG" ] || die "KUBECONFIG ($KUBECONFIG) is not readable."
  [ -s "$KUBECONFIG" ] || die "KUBECONFIG ($KUBECONFIG) is empty."

  # -----------------------------------------------------------------------
  # Cluster connectivity
  # -----------------------------------------------------------------------
  echo "Verifying cluster connectivity…"
  oc whoami &>/dev/null || die "Cannot reach the cluster. Check your KUBECONFIG and current context."

  # -----------------------------------------------------------------------
  # Discover the current context's API URL and CA; host credentials stay on host.
  # -----------------------------------------------------------------------
  # Extract API server URL
  API_URL="$(oc config view --flatten --minify \
    -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null)" \
    || die "Could not extract API URL from kubeconfig."
  [ -n "$API_URL" ] || die "API URL is empty in the kubeconfig."

  # -----------------------------------------------------------------------
  # Extract cluster CA
  # -----------------------------------------------------------------------
  echo "Extracting cluster CA…"
  CLUSTER_CA_B64="$(oc config view --flatten --minify \
    -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' 2>/dev/null)" || true

  if [ -z "$CLUSTER_CA_B64" ]; then
    # Try to get CA from the API server directly
    CLUSTER_CA_B64="$(oc get configmap kube-root-ca.crt -n kube-system -o jsonpath='{.data.ca\.crt}' 2>/dev/null | base64 -w0)" \
      || die "Could not extract cluster CA from kubeconfig or cluster."
  fi
  [ -n "$CLUSTER_CA_B64" ] || die "Cluster CA data is empty."

  # -----------------------------------------------------------------------
  # OAuth discovery
  # -----------------------------------------------------------------------
  echo "Querying OAuth discovery endpoint…"
  OAUTH_META="$(oc get --raw '/.well-known/oauth-authorization-server' 2>/dev/null)" \
    || die "OAuth discovery failed. Is this an OpenShift cluster?"

  extract_json() {
    local json="$1" key="$2"
    echo "$json" | jq -er --arg key "$key" '.[$key] | strings' 2>/dev/null
  }

  AUTHORIZATION_ENDPOINT="$(extract_json "$OAUTH_META" authorization_endpoint)" \
    || die "Could not parse authorization_endpoint from OAuth discovery."
  TOKEN_ENDPOINT="$(extract_json "$OAUTH_META" token_endpoint)" \
    || die "Could not parse token_endpoint from OAuth discovery."
  [ -n "$AUTHORIZATION_ENDPOINT" ] || die "authorization_endpoint is empty."
  [ -n "$TOKEN_ENDPOINT" ] || die "token_endpoint is empty."

  echo "  Authorization: $AUTHORIZATION_ENDPOINT"
  echo "  Token:         $TOKEN_ENDPOINT"

  # -----------------------------------------------------------------------
  # Generated directory
  # -----------------------------------------------------------------------
  mkdir -p "$GEN_DIR"
  chmod 0700 "$GEN_DIR"

  # -----------------------------------------------------------------------
  # Reconcile namespace
  # -----------------------------------------------------------------------
  echo "Reconciling namespace '$NAMESPACE'…"
  if ! oc get namespace "$NAMESPACE" &>/dev/null; then
    oc create namespace "$NAMESPACE"
  fi

  # -----------------------------------------------------------------------
  # Reconcile the proxy service account. It is the delegation identity only
  # (TokenReview/SubjectAccessReview, OAuth serving-cert watch); browser login
  # goes through the separate OAuthClient below, so no OAuth redirect
  # annotation is needed on the service account.
  # -----------------------------------------------------------------------
  echo "Reconciling service account '$SA_NAME'…"
  if ! oc get serviceaccount "$SA_NAME" -n "$NAMESPACE" &>/dev/null; then
    oc create serviceaccount "$SA_NAME" -n "$NAMESPACE"
  fi

  # -----------------------------------------------------------------------
  # Reconcile the universal bearer marker and proxy's review/informer permissions.
  # This marker is an authn gate, not an InfraPad data-authorization rule. Neither
  # the virtual resource nor the named object needs to exist in the API server.
  # -----------------------------------------------------------------------
  echo "Reconciling bearer delegation RBAC…"
  oc apply -f - <<EOF || die "Cannot reconcile InfraPad bearer marker Role/RoleBinding in $NAMESPACE. Check host RBAC."
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: infrapad-browser-api-access
  namespace: ${NAMESPACE}
rules:
- apiGroups: ["infrapad.local"]
  resources: ["access"]
  resourceNames: ["browser-api"]
  verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: infrapad-browser-api-access
  namespace: ${NAMESPACE}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: infrapad-browser-api-access
subjects:
- kind: Group
  name: system:authenticated
  apiGroup: rbac.authorization.k8s.io
EOF

  oc apply -f - <<EOF || die "Cannot bind $SA_NAME to system:auth-delegator. Check host cluster RBAC."
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: infrapad-local-oauth-proxy-auth-delegator
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:auth-delegator
subjects:
- kind: ServiceAccount
  name: ${SA_NAME}
  namespace: ${NAMESPACE}
EOF

  # The OpenShift provider watches this named ConfigMap for OAuth serving CA
  # changes even when using an explicit CA for validate_url.
  oc apply -f - <<EOF || die "Cannot grant $SA_NAME the oauth-serving-cert informer permissions in openshift-config-managed. Check host RBAC."
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: infrapad-oauth-serving-cert-reader
  namespace: openshift-config-managed
rules:
- apiGroups: [""]
  resources: ["configmaps"]
  resourceNames: ["oauth-serving-cert"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: infrapad-oauth-serving-cert-reader
  namespace: openshift-config-managed
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: infrapad-oauth-serving-cert-reader
subjects:
- kind: ServiceAccount
  name: ${SA_NAME}
  namespace: ${NAMESPACE}
EOF

  # -----------------------------------------------------------------------
  # Reconcile token Secret
  # -----------------------------------------------------------------------
  echo "Reconciling token Secret '$TOKEN_SECRET'…"
  if oc get secret "$TOKEN_SECRET" -n "$NAMESPACE" &>/dev/null; then
    # Validate existing secret
    SECRET_TYPE="$(oc get secret "$TOKEN_SECRET" -n "$NAMESPACE" -o jsonpath='{.type}')"
    [ "$SECRET_TYPE" = "kubernetes.io/service-account-token" ] \
      || die "Secret '$TOKEN_SECRET' exists but has incompatible type '$SECRET_TYPE' (expected kubernetes.io/service-account-token). Remove it manually and rerun."

    SA_ANNOTATION="$(oc get secret "$TOKEN_SECRET" -n "$NAMESPACE" \
      -o jsonpath='{.metadata.annotations.kubernetes\.io/service-account\.name}')"
    [ "$SA_ANNOTATION" = "$SA_NAME" ] \
      || die "Secret '$TOKEN_SECRET' is bound to service account '$SA_ANNOTATION', not '$SA_NAME'. Remove it manually and rerun."
  else
    # Create the token secret
    oc apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: ${TOKEN_SECRET}
  namespace: ${NAMESPACE}
  annotations:
    kubernetes.io/service-account.name: ${SA_NAME}
type: kubernetes.io/service-account-token
EOF
  fi

  # Wait for the controller to populate the token (bounded timeout).
  echo "Waiting for token to be populated…"
  MAX_WAIT=60
  WAITED=0
  while true; do
    SA_TOKEN="$(oc get secret "$TOKEN_SECRET" -n "$NAMESPACE" \
      -o jsonpath='{.data.token}' 2>/dev/null)" || true
    if [ -n "$SA_TOKEN" ]; then
      break
    fi
    if [ "$WAITED" -ge "$MAX_WAIT" ]; then
      die "Timed out (${MAX_WAIT}s) waiting for token in Secret '$TOKEN_SECRET'. Check the token controller."
    fi
    sleep 2
    WAITED=$((WAITED + 2))
  done

  # -----------------------------------------------------------------------
  # Materialize the proxy SA token — never echo. Used ONLY for the delegation
  # kubeconfig, distinct from the OAuthClient's oauth-client-secret.
  # -----------------------------------------------------------------------
  echo "Writing proxy-sa-token…"
  echo "$SA_TOKEN" | base64 -d > "$PROXY_SA_TOKEN_FILE.tmp"
  chmod 0600 "$PROXY_SA_TOKEN_FILE.tmp"
  mv -f "$PROXY_SA_TOKEN_FILE.tmp" "$PROXY_SA_TOKEN_FILE"

  # -----------------------------------------------------------------------
  # Materialize a standalone kubeconfig containing ONLY the proxy SA token.
  # jq reads the token from a file, never from a process argument or host context.
  # JSON is valid kubeconfig YAML; the CA is inlined for the container.
  # -----------------------------------------------------------------------
  migrate_proxy_sa_token
  echo "Writing proxy service-account kubeconfig…"
  jq -n --arg server "$API_URL" --arg ca "$CLUSTER_CA_B64" \
    --rawfile token "$PROXY_SA_TOKEN_FILE" '{
      apiVersion: "v1", kind: "Config", "current-context": "infrapad-oauth-proxy",
      clusters: [{name: "selected-cluster", cluster: {server: $server, "certificate-authority-data": $ca}}],
      contexts: [{name: "infrapad-oauth-proxy", context: {cluster: "selected-cluster", user: "infrapad-oauth-proxy"}}],
      users: [{name: "infrapad-oauth-proxy", user: {token: ($token | rtrimstr("\n"))}}]
    }' > "$GEN_DIR/kubeconfig.tmp" || die "Could not write proxy kubeconfig."
  chmod 0600 "$GEN_DIR/kubeconfig.tmp"
  mv -f "$GEN_DIR/kubeconfig.tmp" "$GEN_DIR/kubeconfig"

  # Verify actual review calls as the proxy identity, not just the host's
  # permissions. Keep sample bearer values out of command lines and logs.
  echo "Checking proxy service-account review and informer permissions…"
  PROXY_OC=(oc --kubeconfig "$GEN_DIR/kubeconfig")
  "${PROXY_OC[@]}" create -f - >/dev/null <<EOF || die "Proxy service account cannot create TokenReviews; check system:auth-delegator binding."
apiVersion: authentication.k8s.io/v1
kind: TokenReview
spec:
  token: invalid-bootstrap-probe
EOF
  "${PROXY_OC[@]}" create -f - >/dev/null <<EOF || die "Proxy service account cannot create SubjectAccessReviews; check system:auth-delegator binding."
apiVersion: authorization.k8s.io/v1
kind: SubjectAccessReview
spec:
  user: invalid-bootstrap-probe
  resourceAttributes:
    namespace: ${NAMESPACE}
    group: infrapad.local
    resource: access
    name: browser-api
    verb: get
EOF
  for verb in get list watch; do
    "${PROXY_OC[@]}" auth can-i "$verb" configmaps/oauth-serving-cert -n openshift-config-managed | grep -qx yes \
      || die "Proxy service account lacks $verb on openshift-config-managed/oauth-serving-cert."
  done
  # Evaluate the marker for the authenticated group with a real SAR. This does
  # not require host impersonation permission or a real InfraPad resource/CRD.
  "${PROXY_OC[@]}" create -f - -o json <<EOF | jq -e '.status.allowed == true' >/dev/null \
    || die "Marker SAR for system:authenticated is not allowed; check InfraPad RoleBinding."
apiVersion: authorization.k8s.io/v1
kind: SubjectAccessReview
spec:
  user: infrapad-bootstrap-probe
  groups: ["system:authenticated"]
  resourceAttributes:
    namespace: ${NAMESPACE}
    group: infrapad.local
    resource: access
    name: browser-api
    verb: get
EOF

  # -----------------------------------------------------------------------
  # Materialize cluster CA
  # -----------------------------------------------------------------------
  echo "Writing cluster CA…"
  echo "$CLUSTER_CA_B64" | base64 -d > "$GEN_DIR/cluster-ca.crt.tmp"
  chmod 0644 "$GEN_DIR/cluster-ca.crt.tmp"
  mv -f "$GEN_DIR/cluster-ca.crt.tmp" "$GEN_DIR/cluster-ca.crt"

  # -----------------------------------------------------------------------
  # Cookie secret — generate once, reuse
  # -----------------------------------------------------------------------
  if [ -s "$GEN_DIR/cookie-secret" ]; then
    chmod 0600 "$GEN_DIR/cookie-secret"
    echo "Reusing existing cookie-secret."
  else
    echo "Generating cookie-secret…"
    require_tool openssl
    openssl rand -base64 32 | tr '+/' '-_' | tr -d '=\n' > "$GEN_DIR/cookie-secret.tmp"
    chmod 0600 "$GEN_DIR/cookie-secret.tmp"
    mv -f "$GEN_DIR/cookie-secret.tmp" "$GEN_DIR/cookie-secret"
  fi

  # -----------------------------------------------------------------------
  # TLS certificate via mkcert — generate once, reuse
  # -----------------------------------------------------------------------
  if [ -s "$GEN_DIR/tls.crt" ] && [ -s "$GEN_DIR/tls.key" ]; then
    chmod 0600 "$GEN_DIR/tls.key"
    echo "Reusing existing TLS certificate and key."
  else
    echo "Generating TLS certificate with mkcert…"
    # Ensure mkcert has a usable CA. mkcert supports a keyless CA directory
    # (rootCA.pem without rootCA-key.pem) for trust installation only; that
    # state cannot sign the local certificate we need here.
    MKCERT_CAROOT="$(mkcert -CAROOT 2>/dev/null)" \
      || die "Could not determine mkcert's CA directory. Check mkcert and CAROOT."
    [ -n "$MKCERT_CAROOT" ] \
      || die "mkcert returned an empty CA directory. Check mkcert and CAROOT."

    echo "The generation will ask for sudo to install the certificates to global store…"
    mkcert -install \
      || die "mkcert -install failed. Check the mkcert CA directory and local trust-store permissions."
    [ -r "$MKCERT_CAROOT/rootCA.pem" ] \
      || die "mkcert CA certificate is missing or unreadable at '$MKCERT_CAROOT/rootCA.pem'."
    [ -r "$MKCERT_CAROOT/rootCA-key.pem" ] \
      || die "mkcert CA private key is missing or unreadable at '$MKCERT_CAROOT/rootCA-key.pem'. Restore the key or reset the incomplete mkcert CA before rerunning."

    # Remove any half-written files from a previous failed attempt.
    rm -f "$GEN_DIR/tls.crt.tmp" "$GEN_DIR/tls.key.tmp" \
          "$GEN_DIR/tls.crt" "$GEN_DIR/tls.key"

    # mkcert writes cert and key with predictable names based on its arguments.
    # We use a temp directory to collect the output atomically; the global EXIT
    # trap removes it on failure.
    MKCERT_TMP="$(mktemp -d "$GEN_DIR/mkcert.XXXXXX")"
    mkcert -cert-file "$MKCERT_TMP/tls.crt" -key-file "$MKCERT_TMP/tls.key" \
      localhost 127.0.0.1 ::1

    chmod 0644 "$MKCERT_TMP/tls.crt"
    chmod 0600 "$MKCERT_TMP/tls.key"
    mv -f "$MKCERT_TMP/tls.crt" "$GEN_DIR/tls.crt"
    mv -f "$MKCERT_TMP/tls.key" "$GEN_DIR/tls.key"
    rm -rf "$MKCERT_TMP"
    MKCERT_TMP=""
  fi

  # Register a separate restricted OAuthClient; SA identity remains delegation-only.
  oauth_client setup

  # Cache only after the complete online reconciliation succeeds. This prevents a
  # partially failed run from making stale credentials eligible for offline use.
  echo "Writing OAuth metadata cache…"
  write_oauth_metadata
  render_oauth_proxy_config

  # -----------------------------------------------------------------------
  # Opt-in ingress CA for metrics upstream
  # -----------------------------------------------------------------------
  require_tool openssl
  prepare_metrics_trust

  echo "OAuth bootstrap complete. Generated files in $GEN_DIR"
  if metrics_upstream_enabled; then
    echo ""
    echo "Metrics upstream detected. Export the following for Go:"
    echo "  export INFRAPAD_PROMETHEUS_UPSTREAM_CA_FILE=\"$INGRESS_CA_FILE\""
  fi
}

# ---------------------------------------------------------------------------
# Subcommand dispatch
# ---------------------------------------------------------------------------
case "${1:-}" in
  -h|--help)
    usage
    exit 0
    ;;
  ""|bootstrap)
    if [ "${1:-}" = "bootstrap" ]; then shift; fi
    [ "$#" -eq 0 ] || die "'bootstrap' takes no arguments."
    cmd_bootstrap
    ;;
  refresh-ca)
    shift
    [ "$#" -eq 0 ] || die "'refresh-ca' takes no arguments."
    cmd_refresh_ca
    ;;
  client)
    shift
    cmd_client "$@"
    ;;
  *)
    usage >&2
    exit 1
    ;;
esac
