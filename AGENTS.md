# Notes for the agent

When working on CLI related changes, YOU MUST READ ./cli/CONTRIBUTING.md
with the most important information about that code-base.

## Dev server refresh

- After server changes, run `task server:build` from the repository root. The running dev server watches `server/infrapad-server` and restarts automatically when the build atomically replaces it; no manual restart or alternate-port server is needed.
- Allow the watcher to restart, then verify against the normal dev-server endpoints (gRPC `localhost:50061`, HTTP `localhost:8088`), e.g. run the relevant tests in `server/test/e2e`.
- If the build reports "up to date" but you need to force a refresh, run `task --force server:build`.

## Code organization

Order functions from high-level entry points to lower-level helpers and implementation details.

## Coding Tips

When writring multi-line strings, use `utils.Dedent` from "github.com/infrapad/infrapad/server/internal/utils".
