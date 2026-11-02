# InfraPad Server

## Repository layout

## Extension CUE schemas

Extension packages define their inner-content and transient-payload Go
types; CUE schemas for them are generated with `cue get go` from the Go
sources and embedded for runtime validation.

- Commands (from the repository root; run from `server/` without the
  `server:` prefix):
  - `task server:extensions:schema:generate` — regenerate the committed
    artifacts in place.
  - `task server:extensions:schema:check` — fail when the committed
    artifacts are stale or missing, without modifying the tree.
- Layout per extension package:
  - `schema/<name>_go_gen.cue` — raw `cue get go` output (generated).
  - `schema/*_patch.cue` — optional handwritten companion constraints,
    unified with the generated definitions during assembly.
  - `schema.cue` — the self-contained embedded artifact (generated):
    raw definitions plus companions, with non-stdlib imports inlined.
- Generated definitions are closed recursively (unknown fields are
  rejected), `time.Time` fields are validated by the CUE `time` package,
  and `extras map[string]any` fields stay open to arbitrary metadata.

Adding an extension to the workflow: add its package to `INPUTS`, run
`task server:extensions:schema:generate`, and reference the resulting
definitions from the package's `content.Schema` selectors.
