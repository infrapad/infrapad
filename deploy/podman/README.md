# Podman development services

## Authentication modes

The combined development environment (`task dev` or `task podman:dev:start`) uses the insecure dummy authentication proxy by default. Set `INFRAPAD_AUTH` to select a mode:

- unset, empty, or `dummy`: dummy proxy at `http://localhost:8089`;
- `none`: no authentication proxy;
- `openshift`: OpenShift OAuth proxy at `https://localhost:8443`.

Direct InfraPad HTTP at `http://localhost:8088` and gRPC at `localhost:50061` remain available in every mode.

For standalone UI development, `INFRAPAD_UI=hot-reload` makes the active authentication proxy forward to Vite on port 5173 instead of the Go server on port 8088. Unset/empty or explicit `INFRAPAD_UI=prod` forwards to Go on 8088. Hot reload requires either dummy or OpenShift authentication. `INFRAPAD_AUTH_UPSTREAM` is an advanced override for the selected proxy's upstream. UI settings are ignored by unrelated focused compositions such as `task podman:postgres`. See the [development guide](../../DEVELOPMENT.md) for the full task-based UI workflows.

The dummy proxy performs **no signature, issuer, audience, or expiry validation and provides no security**. Use it only for trusted local testing. For FleetShift's HTTPS shell, the local browser-facing InfraPad URL is `http://localhost:8089` (the HTTP loopback URL). Fetch `/ui/config` there with FleetShift's current OIDC bearer, then resolve the returned `/v1` API base URL against this origin. The proxy decodes the bearer to identity headers and removes `Authorization` before forwarding to Go; it does not validate the token or authorize API access.

For local browser development only, the dummy proxy answers unauthenticated bearer preflights and sets `Access-Control-Allow-Origin: *` on `/ui/config`, `/v1`, and `/v1/` descendants, including proxy errors. It permits `Authorization` and `Content-Type` headers and does **not** enable cross-origin cookies. This is not production CORS or HTTPS hosting: a production cross-origin deployment requires its own HTTPS and authorization policy. This proxy policy does not cover the configured Prometheus destination or the separate OpenShift OAuth proxy.

Visit [http://localhost:8089/auth](http://localhost:8089/auth) to log in with a dummy username and optional email address, view the generated bearer token, or log out. Login stores the raw dummy token in an HTTP-only browser-session cookie. An optional `returnTo` query value is preserved through login only when it is a safe root-relative path; absent or unsafe values return to `/auth`. The proxy derives identity headers from the cookie but never forwards the authentication cookie to InfraPad. Requests without a session continue to InfraPad anonymously rather than being redirected.

Generate a token for curl or another CLI with the form endpoint:

```sh
token="$(curl --fail --silent --show-error \
  --data-urlencode 'username=alice' \
  --data-urlencode 'email=alice@example.com' \
  http://localhost:8089/auth/token)"
curl -H "Authorization: Bearer $token" http://localhost:8089/api/v1/...
```

The token endpoint does not create or change the browser session. The existing local generator remains available:

```sh
task podman:dummy-auth:token -- --username alice --email alice@example.com
```

In `INFRAPAD_AUTH=openshift` mode, the pinned OAuth proxy allows unauthenticated `OPTIONS` preflights so FleetShift's HTTPS browser page can send an OpenShift **user** bearer to `https://localhost:8443/ui/config` and `/v1/` document requests. Bearers are authenticated by OpenShift TokenReview and checked by SubjectAccessReview against a local, virtual `get infrapad.local/access/browser-api` marker in `infrapad-local` granted to `system:authenticated` (users and service accounts). This is **not** per-user or per-document InfraPad authorization. No arbitrary OpenShift view/admin role is required. The proxy supplies user/email identity headers and the authenticated user's raw `X-Forwarded-Access-Token`: `pass_access_token` covers cookie sessions and `pass_user_bearer_token` covers delegated bearers. Only the metrics relay consumes that token, constructing a fresh upstream bearer; tokens are never part of `/ui/config`. Standalone OAuth cookie login remains available. Direct `:8088`/gRPC access is still a trusted-development bypass.

For the CLI, use `INFRAPAD_API_URL=https://localhost:8443` and the existing `INFRAPAD_TOKEN` OpenShift user token. From FleetShift's HTTPS page, use the same local origin for `/ui/config` and `/v1/documents` with its separately acquired OpenShift user bearer. This local-development preflight behavior applies to all OPTIONS paths, not just FleetShift, and does not make proxy-generated login redirects or errors CORS-readable. Do not use the development CORS behavior as a production origin/authorization policy or for cross-origin cookies.

The focused proxy tasks are `task podman:dummy-auth-proxy` and `task podman:oauth-proxy`. Run the dummy proxy tests with `task podman:dummy-auth:test`.

## Restricted OAuth login for monitoring

The local deployment uses its own regular OAuthClient **`infrapad-local-login`**:

- Redirect: `https://localhost:8443/oauth/callback`; **`grantMethod: prompt`**.
- Allowed/requested scopes: `user:info user:check-access role:cluster-monitoring-view:openshift-monitoring`.
- Restrictions: the two basic literal scopes and only `cluster-monitoring-view` in `openshift-monitoring`, **`allowEscalation: false`**. No `user:full`, automatic consent, custom monitoring RBAC, or new user permissions.
- Scopes intersect the signed-in user's existing permissions. That user still needs monitoring access. The existing role includes more than GET permissions; this is a limited role token, **not a strictly GET-only token**. InfraPad's relay stays GET-only.
- Login uses private `generated/oauth-client-secret`; its bytes match the registered secret exactly, **without a trailing newline**. Do not recreate it with newline-appending `echo`.
- `generated/proxy-sa-token` holds the proxy service account's token and builds `generated/kubeconfig`, the identity the proxy uses for delegated authentication: verifying incoming OpenShift bearer tokens (TokenReview), checking the local access marker (SubjectAccessReview), and watching the OAuth serving-cert ConfigMap.

### Native-token migration and user redeployment

**The developer runs these steps; implementation does not redeploy the stack.**

1. Select the host kubeconfig matching the existing generated cache. Setup needs `get`/`create` on cluster-scoped OAuthClients, in addition to existing first-bootstrap RBAC privileges. Prerequisites: Bash, `oc`, `jq`, `openssl`; fresh TLS generation also needs `mkcert`. API registration explicitly verifies HTTPS using the matching generated API CA, even if the host kubeconfig disables verification. A cluster/context mismatch fails without changing cached credentials.
2. Render/migrate online once, preserving the existing directory and its contents. For hot reload, from the repository root:

   ```sh
   INFRAPAD_AUTH_UPSTREAM=http://127.0.0.1:5173 \
     ./deploy/podman/scripts/openshift-oauth.sh
   ```

   Use `http://127.0.0.1:8088` for built UI. Delegation-ready caches migrate only the OAuthClient, metadata, and rendered config; cookie secret, SA credentials/kubeconfig, TLS certificates, and CA files are preserved byte-for-byte. Matching client setup reuses its secret. Unexpected ownership, UID, secret, redirect, consent, or restrictions fail closed rather than being overwritten. After successful migration, offline starts need no API access. They validate local state/secret integrity, **not live registration**. An old basic-only cache cannot silently qualify for offline reuse.
3. Apply the changed Compose manifest in the **original project**, recreating only `oauth-proxy` so both the new secret mount and atomically replaced config are picked up. If the original project uses `deploy/podman/all.yaml`, for example:

   ```sh
   INFRAPAD_UI=hot-reload INFRAPAD_AUTH=openshift \
     ./deploy/podman/scripts/start.sh -f deploy/podman/all.yaml \
     -- --no-deps --force-recreate -d oauth-proxy
   ```

   Retain any original project-name/environment options. Do not start a second project on port 8443. For a stopped stack, restart through its original task/workflow with the updated manifest. No backend or frontend code/build changes are required by this correction.
4. Keep the backend's explicit `INFRAPAD_PROMETHEUS_UPSTREAM_URL` and verified upstream TLS. If the cluster's ingress CA changed, refresh **only** public trust:

   ```sh
   ./deploy/podman/scripts/openshift-oauth.sh refresh-ca
   export INFRAPAD_PROMETHEUS_UPSTREAM_CA_FILE="$PWD/deploy/podman/generated/ingress-ca.crt"
   ```

   Restart the original server/watch task if its startup URL, CA file, or trust contents changed. Rebuilding alone does not reload startup settings/trust. The Kubernetes API CA is not necessarily ingress trust; an old example CA file may be stale. Never enable insecure TLS to mask this prerequisite. `refresh-ca` does not migrate/register a client.
5. Open `https://localhost:8443/ui/documents`, log in, and accept **all three** approved scopes on the existing OpenShift consent page. The new stable cookie name `_infrapad_openshift_monitoring_session` excludes old identity-only/basic-scope sessions **without rotating the cookie secret**. Subsequent restarts keep the name/secret. Cluster SSO may supply an identity, but an old InfraPad cookie cannot establish the new scoped session.
6. Confirm redeployment before starting the separate standalone UI-verification task. Require nonempty live charts using the signed-in user's token over verified Thanos TLS. Reproducer success and local integration tests are not deployed-chart evidence. Canonical external `Authorization: Bearer …` remains supported; the known pinned-proxy case-variant forwarding limitation is deferred, not fixed here. FleetShift acceptance and monitoring-denial testing remain outside this task.

### Explicit client maintenance

Inspect/revalidate a registration online without rendering config or rotating credentials:

```sh
./deploy/podman/scripts/openshift-oauth.sh client setup
```

Stop the owned proxy before explicitly removing this deployment's client. Cleanup needs host OAuthClient `get`/`delete` privileges and the matching private state:

```sh
./deploy/podman/scripts/openshift-oauth.sh client cleanup
```

Cleanup checks ownership/configuration and uses UID/resource-version deletion preconditions. It removes only the owned OAuthClient and its secret/state; SA/RBAC, cookie secret, certificates, and other artifacts survive. Failed deletion retains state. A repeat cleanup is a no-op; a subsequent explicit bootstrap registers a new client. Keep the private state until cleanup; do not reset it to force adoption. Interrupted API creation can be retried with matching pending state. Missing/corrupt state, an interrupted local secret write, or a disappeared/replaced registered client needs inspection, not automatic credential rotation. The fixed deployment client ID supports one owner per cluster; another checkout without matching state must not overwrite it. The helper serializes operations with `generated/.oauth-client.lock.d`; after a forced termination, confirm the helper has stopped and remove that lock directory before retrying.
