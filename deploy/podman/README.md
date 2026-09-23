# Podman development services

## Authentication modes

The combined development environment (`task dev` or `task podman:dev:start`) uses the insecure dummy authentication proxy by default. Set `INFRAPAD_AUTH` to select a mode:

- unset, empty, or `dummy`: dummy proxy at `http://localhost:8089`;
- `none`: no authentication proxy;
- `openshift`: OpenShift OAuth proxy at `https://localhost:8443`.

Direct InfraPad HTTP at `http://localhost:8088` and gRPC at `localhost:50061` remain available in every mode.

The dummy proxy performs **no signature, issuer, audience, or expiry validation and provides no security**. Use it only for trusted local testing.

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

The focused proxy tasks are `task podman:dummy-auth-proxy` and `task podman:oauth-proxy`. Run the dummy proxy tests with `task podman:dummy-auth:test`.
