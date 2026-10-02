#!/usr/bin/env bash
# Idempotent bootstrap for the OpenShift OAuth proxy used in local development.
# Produces all generated files under deploy/podman/generated/ and reconciles
# the required namespace, service account, RBAC, and token Secret on the cluster
# identified by the current kubeconfig context.

set -euo pipefail
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

# Cluster-side names
NAMESPACE="infrapad-local"
SA_NAME="infrapad-oauth-proxy"
TOKEN_SECRET="infrapad-oauth-proxy-token"
REDIRECT_ANNOTATION="serviceaccounts.openshift.io/oauth-redirecturi.localhost"
REDIRECT_VALUE="https://localhost:8443/oauth/callback"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
die() { echo "ERROR: $*" >&2; exit 1; }

require_tool() {
  command -v "$1" &>/dev/null || die "'$1' is required but not found in PATH."
}

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

has_complete_cache() {
  local artifact
  local artifacts=(
    "$METADATA_FILE"
    "$GEN_DIR/client-secret"
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

write_oauth_metadata() {
  jq -n \
    --arg api_url "$API_URL" \
    --arg authorization_endpoint "$AUTHORIZATION_ENDPOINT" \
    --arg token_endpoint "$TOKEN_ENDPOINT" \
    '{
      apiUrl: $api_url,
      authorizationEndpoint: $authorization_endpoint,
      tokenEndpoint: $token_endpoint,
      delegationReady: true
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
# Cached offline path
# ---------------------------------------------------------------------------
require_tool jq

if has_complete_cache; then
  if load_oauth_metadata; then
    echo "Using cached OpenShift OAuth metadata and generated credentials."
    render_oauth_proxy_config
    echo "OAuth bootstrap complete. Regenerated $GEN_DIR/oauth-proxy.cfg without contacting OpenShift."
    exit 0
  fi
  echo "Cached OAuth metadata is invalid; falling back to online bootstrap." >&2
fi

# ---------------------------------------------------------------------------
# Online prerequisite checks
# ---------------------------------------------------------------------------
require_tool oc
require_tool mkcert

# KUBECONFIG must be readable and non-empty.
if [ -z "${KUBECONFIG:-}" ]; then
  # oc/kubectl default
  KUBECONFIG="${HOME}/.kube/config"
fi
[ -r "$KUBECONFIG" ] || die "KUBECONFIG ($KUBECONFIG) is not readable."
[ -s "$KUBECONFIG" ] || die "KUBECONFIG ($KUBECONFIG) is empty."

# ---------------------------------------------------------------------------
# Cluster connectivity
# ---------------------------------------------------------------------------
echo "Verifying cluster connectivity…"
oc whoami &>/dev/null || die "Cannot reach the cluster. Check your KUBECONFIG and current context."

# ---------------------------------------------------------------------------
# Discover the current context's API URL and CA; host credentials stay on host.
# ---------------------------------------------------------------------------
# Extract API server URL
API_URL="$(oc config view --flatten --minify \
  -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null)" \
  || die "Could not extract API URL from kubeconfig."
[ -n "$API_URL" ] || die "API URL is empty in the kubeconfig."

# ---------------------------------------------------------------------------
# Extract cluster CA
# ---------------------------------------------------------------------------
echo "Extracting cluster CA…"
CLUSTER_CA_B64="$(oc config view --flatten --minify \
  -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' 2>/dev/null)" || true

if [ -z "$CLUSTER_CA_B64" ]; then
  # Try to get CA from the API server directly
  CLUSTER_CA_B64="$(oc get configmap kube-root-ca.crt -n kube-system -o jsonpath='{.data.ca\.crt}' 2>/dev/null | base64 -w0)" \
    || die "Could not extract cluster CA from kubeconfig or cluster."
fi
[ -n "$CLUSTER_CA_B64" ] || die "Cluster CA data is empty."

# ---------------------------------------------------------------------------
# OAuth discovery
# ---------------------------------------------------------------------------
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

# ---------------------------------------------------------------------------
# Generated directory
# ---------------------------------------------------------------------------
mkdir -p "$GEN_DIR"
chmod 0700 "$GEN_DIR"

# ---------------------------------------------------------------------------
# Reconcile namespace
# ---------------------------------------------------------------------------
echo "Reconciling namespace '$NAMESPACE'…"
if ! oc get namespace "$NAMESPACE" &>/dev/null; then
  oc create namespace "$NAMESPACE"
fi

# ---------------------------------------------------------------------------
# Reconcile service account with redirect annotation
# ---------------------------------------------------------------------------
echo "Reconciling service account '$SA_NAME'…"
if ! oc get serviceaccount "$SA_NAME" -n "$NAMESPACE" &>/dev/null; then
  oc create serviceaccount "$SA_NAME" -n "$NAMESPACE"
fi

# Ensure the redirect annotation is present (patch is idempotent).
oc annotate serviceaccount "$SA_NAME" -n "$NAMESPACE" \
  "${REDIRECT_ANNOTATION}=${REDIRECT_VALUE}" --overwrite

# ---------------------------------------------------------------------------
# Reconcile the universal bearer marker and proxy's review/informer permissions.
# This marker is an authn gate, not an InfraPad data-authorization rule. Neither
# the virtual resource nor the named object needs to exist in the API server.
# ---------------------------------------------------------------------------
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

# ---------------------------------------------------------------------------
# Reconcile token Secret
# ---------------------------------------------------------------------------
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

# ---------------------------------------------------------------------------
# Materialize client-secret (decoded token) — never echo
# ---------------------------------------------------------------------------
echo "Writing client-secret…"
echo "$SA_TOKEN" | base64 -d > "$GEN_DIR/client-secret.tmp"
chmod 0600 "$GEN_DIR/client-secret.tmp"
mv -f "$GEN_DIR/client-secret.tmp" "$GEN_DIR/client-secret"

# ---------------------------------------------------------------------------
# Materialize a standalone kubeconfig containing ONLY the proxy SA token.
# jq reads the token from a file, never from a process argument or host context.
# JSON is valid kubeconfig YAML; the CA is inlined for the container.
# ---------------------------------------------------------------------------
echo "Writing proxy service-account kubeconfig…"
jq -n --arg server "$API_URL" --arg ca "$CLUSTER_CA_B64" \
  --rawfile token "$GEN_DIR/client-secret" '{
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

# ---------------------------------------------------------------------------
# Materialize cluster CA
# ---------------------------------------------------------------------------
echo "Writing cluster CA…"
echo "$CLUSTER_CA_B64" | base64 -d > "$GEN_DIR/cluster-ca.crt.tmp"
chmod 0644 "$GEN_DIR/cluster-ca.crt.tmp"
mv -f "$GEN_DIR/cluster-ca.crt.tmp" "$GEN_DIR/cluster-ca.crt"

# ---------------------------------------------------------------------------
# Cookie secret — generate once, reuse
# ---------------------------------------------------------------------------
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

# ---------------------------------------------------------------------------
# TLS certificate via mkcert — generate once, reuse
# ---------------------------------------------------------------------------
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
  # We use a temp directory to collect the output atomically.
  MKCERT_TMP="$(mktemp -d "$GEN_DIR/mkcert.XXXXXX")"
  trap 'rm -rf "$MKCERT_TMP"' EXIT
  mkcert -cert-file "$MKCERT_TMP/tls.crt" -key-file "$MKCERT_TMP/tls.key" \
    localhost 127.0.0.1 ::1

  chmod 0644 "$MKCERT_TMP/tls.crt"
  chmod 0600 "$MKCERT_TMP/tls.key"
  mv -f "$MKCERT_TMP/tls.crt" "$GEN_DIR/tls.crt"
  mv -f "$MKCERT_TMP/tls.key" "$GEN_DIR/tls.key"
  rm -rf "$MKCERT_TMP"
  trap - EXIT
fi

# Cache only after the complete online reconciliation succeeds. This prevents a
# partially failed run from making stale credentials eligible for offline use.
echo "Writing OAuth metadata cache…"
write_oauth_metadata
render_oauth_proxy_config

echo "OAuth bootstrap complete. Generated files in $GEN_DIR"
