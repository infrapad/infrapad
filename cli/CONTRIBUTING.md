# Notes about the CLI code base

## Code structure

```
cli/
├── cmd/                     # Cobra command tree (root + sub-commands per resource)
│   ├── document/            #   `infrapad document` sub-commands
│   ├── block/               #   `infrapad block` sub-commands
│   └── md/                  #   `infrapad md` sub-commands
├── pkg/
│   ├── client/              # gRPC client wrapper around InfrapadService
│   ├── cliutil/             # Shared state (global flags) and helpers
│   ├── output/              # Output formatting (table, JSON) and column definitions
│   └── markdown/            # Goldmark-based parser/renderer for infrapad-flavoured markdown
└── test/
    └── e2e/                 # End-to-end shell tests (require a running server)
```

## Testing

### E2E tests

*E2E tests are the most important part of the test suite.*

**When modifying existing functionality, start by looking at the e2e tests** —
they document the expected behaviour and will catch regressions. Add or update
e2e test cases to cover your changes.

Start InfraPad and the dummy authentication proxy from the repository root in
one terminal:

```bash
task pd:dev:run
```

in separate terminal

```
task server:run
```

Then run the tests from `cli/` in another terminal:

```bash
task test:e2e
task test:e2e TEST=markdown    # run only markdown_test.sh
```

The default endpoint is `http://localhost:8089`. For debugging, a dummy token
can be obtained manually while the development stack is running:

```bash
curl --fail --silent --show-error \
  --data-urlencode 'username=cli-contributor' \
  http://localhost:8089/auth/token
```

Optional overrides can select another endpoint or preserve a caller-supplied
token:

```bash
INFRAPAD_API_URL=https://infrapad.example.com \
INFRAPAD_TOKEN="$TOKEN" \
task test:e2e
```

E2E tests are bash scripts in `test/e2e/`. They exercise the built CLI binary
against live InfraPad and proxy services and use the assertion helpers from
`_lib.sh`.

### Unit tests

```bash
task test:unit                                      # run all
task test:unit PKG=./pkg/markdown/...                # single package
task test:unit PKG=./pkg/markdown/ -- -run TestX     # single test function
```

Unit tests live next to the code they test (e.g. `pkg/markdown/parse_test.go`).

### Other code depending on the cli

The demo application in ../deploy/example/monitored-app/ depends on the cli as well.
Make sure to check the compatiblity after doing external facing chanages. This
includes both the simulate.sh script, as well as the
../deploy/example/monitored-app/.agents/skills.

Call task `cli:install` when in need to have the fresh build available inside
the GOPATH (so that the example doesn't use the old build).
