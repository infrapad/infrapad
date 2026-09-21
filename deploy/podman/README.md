# Podman development services

## Authentication modes

The combined development environment (`task dev` or `task podman:dev:start`) uses the insecure dummy authentication proxy by default. Set `INFRAPAD_AUTH` to select a mode:

- unset, empty, or `dummy`: dummy proxy at `http://localhost:8089`;
- `none`: no authentication proxy;
- `openshift`: OpenShift OAuth proxy at `https://localhost:8443`.

Direct InfraPad HTTP at `http://localhost:8088` and gRPC at `localhost:50061` remain available in every mode.

The dummy proxy performs **no signature, issuer, audience, or expiry validation and provides no security**. Use it only for trusted local testing. Generate a token with:

```sh
token="$(task podman:dummy-auth:token -- --username alice --email alice@example.com)"
curl -H "Authorization: Bearer $token" http://localhost:8089/api/v1/...
```

The focused proxy tasks are `task podman:dummy-auth-proxy` and `task podman:oauth-proxy`. Run the dummy proxy tests with `task podman:dummy-auth:test`.
