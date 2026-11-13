# Podman development services

## Authentication modes

The combined development environment (`task dev` or `task podman:dev:start`) uses the insecure dummy authentication proxy by default. Set `INFRAPAD_AUTH` to select a mode:

- unset, empty, or `dummy`: dummy proxy at `http://localhost:8089`;
- `none`: no authentication proxy;
- `openshift`: OpenShift OAuth proxy at `https://localhost:8443`.

Direct InfraPad HTTP at `http://localhost:8088` and gRPC at `localhost:50061` remain available in every mode.

For standalone UI development, `INFRAPAD_UI=hot-reload` makes the active authentication proxy forward to Vite on port 5173 instead of the Go server on port 8088. Unset/empty or explicit `INFRAPAD_UI=prod` forwards to Go on 8088. Hot reload requires either dummy or OpenShift authentication. `INFRAPAD_AUTH_UPSTREAM` is an advanced override for the selected proxy's upstream. UI settings are ignored by unrelated focused compositions such as `task podman:postgres`. See the [development guide](../../DEVELOPMENT.md) for the full task-based UI workflows.

The dummy proxy performs **no signature, issuer, audience, or expiry validation and provides no security**. Use it only for trusted local testing. For FleetShift's HTTPS shell, the local browser-facing InfraPad URL is `http://localhost:8089` (the HTTP loopback URL). Fetch `/ui/config` there with FleetShift's current OIDC bearer, then resolve the returned `/v1` API base URL against this origin. The proxy decodes the bearer to identity headers and removes `Authorization` before forwarding to Go; it does not validate the token or authorize API access.

For local browser development only, the dummy proxy answers unauthenticated bearer preflights and sets `Access-Control-Allow-Origin: *` on `/ui/config`, `/v1`, and `/v1/` descendants, including proxy errors. It permits `Authorization` and `Content-Type` headers and does **not** enable cross-origin cookies. This is not production CORS or HTTPS hosting: a production cross-origin deployment requires its own HTTPS and authorization policy. This proxy policy does not cover the separate OpenShift OAuth proxy.

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

In `INFRAPAD_AUTH=openshift` mode, the pinned OAuth proxy allows unauthenticated `OPTIONS` preflights so FleetShift's HTTPS browser page can send an OpenShift **user** bearer to `https://localhost:8443/ui/config` and `/v1/` document requests. Bearers are authenticated by OpenShift TokenReview and checked by SubjectAccessReview against a local, virtual `get infrapad.local/access/browser-api` marker in `infrapad-local` granted to `system:authenticated` (users and service accounts). This is **not** per-user or per-document InfraPad authorization. No arbitrary OpenShift view/admin role is required. The proxy supplies only user/email identity headers; it does not forward user tokens upstream (`pass_access_token` and `pass_user_bearer_token` are disabled), and tokens are never part of `/ui/config`. Standalone OAuth cookie login remains available. Direct `:8088`/gRPC access is still a trusted-development bypass.

For the CLI, use `INFRAPAD_API_URL=https://localhost:8443` and the existing `INFRAPAD_TOKEN` OpenShift user token. From FleetShift's HTTPS page, use the same local origin for `/ui/config` and `/v1/documents` with its separately acquired OpenShift user bearer. This local-development preflight behavior applies to all OPTIONS paths, not just FleetShift, and does not make proxy-generated login redirects or errors CORS-readable. Do not use the development CORS behavior as a production origin/authorization policy or for cross-origin cookies.

The focused proxy tasks are `task podman:dummy-auth-proxy` and `task podman:oauth-proxy`. Run the dummy proxy tests with `task podman:dummy-auth:test`.

## Restricted OAuth login

The local deployment uses its own regular OAuthClient **`infrapad-local-login`**:

- Redirect: `https://localhost:8443/oauth/callback`; **`grantMethod: prompt`**.
- Allowed/requested scopes: only the basic login scopes `user:info user:check-access` (literal `scopeRestrictions`). No `user:full`, role scopes, automatic consent, or new user permissions.
- InfraPad never contacts Prometheus from the browser or server; AlertsMatcher charts render `query_results` submitted by external producers, so no monitoring permissions are requested.
- Login uses private `generated/oauth-client-secret`; its bytes match the registered secret exactly, **without a trailing newline**. Do not recreate it with newline-appending `echo`.
- `generated/proxy-sa-token` holds the proxy service account's token and builds `generated/kubeconfig`, the identity the proxy uses for delegated authentication: verifying incoming OpenShift bearer tokens (TokenReview), checking the local access marker (SubjectAccessReview), and watching the OAuth serving-cert ConfigMap.

### Initial setup

Prerequisites: Bash, `oc`, `jq`, `openssl`, and `mkcert`, plus a logged-in `oc` context. The host user needs permission to create the namespace, service account, token Secret, and RBAC bindings, and `get`/`create` access to cluster-scoped OAuthClients. Registration verifies HTTPS using the cluster API CA.

From the repository root, start the development services with OpenShift authentication:

```sh
INFRAPAD_UI=hot-reload INFRAPAD_AUTH=openshift task podman:dev:run
```

This runs bootstrap automatically, generating credentials and proxy configuration under `deploy/podman/generated/`, then starts the services. Run `task server:run` and `INFRAPAD_AUTH=openshift task ui:dev` in separate terminals. For built assets instead, follow the [development guide](../../DEVELOPMENT.md).

Open `https://localhost:8443/ui/documents`, log in, and accept the basic scopes. Subsequent starts reuse the generated credentials and cached discovery metadata, allowing offline restarts. Keep the generated directory private and preserve its client ownership state; bootstrap refuses to overwrite a registration that does not match it.

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
