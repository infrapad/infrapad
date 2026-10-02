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

In `INFRAPAD_AUTH=openshift` mode, the pinned OAuth proxy allows unauthenticated `OPTIONS` preflights so FleetShift's HTTPS browser page can send an OpenShift **user** bearer to `https://localhost:8443/ui/config` and `/v1/` document requests. Bearers are authenticated by OpenShift TokenReview and checked by SubjectAccessReview against a local, virtual `get infrapad.local/access/browser-api` marker in `infrapad-local` granted to `system:authenticated` (users and service accounts). This is **not** per-user or per-document InfraPad authorization. No arbitrary OpenShift view/admin role is required. Only proxy-derived user/email headers are trusted upstream; the proxy does not pass user bearer/access tokens by configuration. Standalone OAuth cookie login remains available. Direct `:8088`/gRPC access is still a trusted-development bypass.

For an existing OpenShift proxy setup created before bearer delegation, **stop the proxy**, run `rm -rf deploy/podman/generated/` once from the repository root, and restart with `INFRAPAD_AUTH=openshift task dev` (or `task podman:oauth-proxy`). Do this while connected to the cluster with a host `$KUBECONFIG` context allowed to reconcile cluster and namespaced RBAC. The bootstrap creates a new cookie secret and local TLS certificate, so old browser cookie sessions expire and the browser may need to trust the new mkcert certificate. The generated, ignored, owner-protected kubeconfig now contains only the proxy service account's token and selected cluster API URL/CA, not the host user's credentials; the host kubeconfig is still used for bootstrap. Do not copy generated credentials to FleetShift. A failed/partial bootstrap does not qualify for offline reuse; after a complete online bootstrap, cached restarts re-render the tracked proxy template without querying cluster RBAC again. Reset and rerun online if cluster permissions drift.

For the CLI, use `INFRAPAD_API_URL=https://localhost:8443` and the existing `INFRAPAD_TOKEN` OpenShift user token. From FleetShift's HTTPS page, use the same local origin for `/ui/config` and `/v1/documents` with its separately acquired OpenShift user bearer. This local-development preflight behavior applies to all OPTIONS paths, not just FleetShift, and does not make proxy-generated login redirects or errors CORS-readable. Do not use the development CORS behavior as a production origin/authorization policy or for cross-origin cookies.

The focused proxy tasks are `task podman:dummy-auth-proxy` and `task podman:oauth-proxy`. Run the dummy proxy tests with `task podman:dummy-auth:test`.
