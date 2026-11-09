# Notes for the agent

When working on CLI related changes, YOU MUST READ ./cli/CONTRIBUTING.md
with the most important information about that code-base.

## Code organization

Order functions from high-level entry points to lower-level helpers and implementation details.

## Coding Tips

When writring multi-line strings, use `utils.Dedent` from "github.com/infrapad/infrapad/server/internal/utils".
