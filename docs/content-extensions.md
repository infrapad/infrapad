# Writing content extensions

This guide is for developers who add a new content type to the InfraPad
server. It describes the contracts in `server/pkg/content` and uses the
built-in extensions as examples:

| Package | Types | Shows |
|---------|-------|-------|
| `server/pkg/extensions/core` | `Core/Markdown` | Ordinary editable block: body and header editing. |
| `server/pkg/extensions/prometheus` | `Prometheus/AlertsMatcher` | Structured header-only block with an empty body; an expander without children that stores submitted `query_results` in the primary record only. |
| `server/pkg/extensions/aiagent` | `AIAgent/Message` | Shared type emitted by other extensions, presentation, companion schema constraints. |
| `server/pkg/extensions/slack` | `Slack/Thread`, `Slack/Conversation` | Expandable primary with a transient payload, a groupable type, and output of another extension's type. |
| `server/pkg/extensions/pagerduty` | `PagerDuty/Incident`, `PagerDuty/Timeline` | Read-only generated Markdown `text` body, header reference locked after enrichment, open JSON inside a strict payload; emits the registered `Prometheus/AlertsMatcher` type (injected via `ContentTypes(matcherType)`). |
| `server/pkg/extensions/cora` | `Cora/Report` | Minimal expandable reference emitting one shared `AIAgent/Message` per payload (injected via `ContentTypes(agentType)`), source timestamp applied to the primary, header reference immutable after creation. |

This guide is for changing the InfraPad server itself. If you're writing an external
producer that submits content to a running server through its
REST API, see [content-producers.md](content-producers.md) instead. For the
Markdown representation, see [markdown-format.md](markdown-format.md).

## Who owns what

An extension owns:

- **Inner content**: a plain Go struct per type (e.g. `aiagent.Message`),
  encoded with ordinary JSON tags. It has no keys, timestamps, or block
  metadata.
- **Transient payload types** for expandable types (e.g. `slack.Payload`).
  Payloads are validated, passed to the extension, and then discarded. They
  are never stored.
- **Schemas** generated from those Go types, plus optional handwritten
  constraints.
- **Behavior**: Markdown rendering, optional Markdown editing, optional
  presentation, and expansion into generated output.

The host (the server) owns the **record** around the content:

- Block numbers, revisions, authors, and the `parent` relationship
  (provenance).
- Event timestamps, which order the document chronologically. Ordinary
  blocks default to their creation time.
- Generated keys, which are stable identities scoped to the generating
  parent, and the membership of grouped blocks.
- Validation, reconciliation of generated output, ownership rules (generated
  blocks can't be edited directly), Markdown framing, and API transport.

Extensions never see or produce the stored record envelope. They work with
`content.Record` values through the helpers in `server/pkg/content/codec.go`
(`ItemView`, `CollectionView`, `WithContent`, `NewItem`, `NewKeyedItem`,
`NewCollection`).

## The `ContentType` contract

Every type implements `content.ContentType`
(`server/pkg/content/content_type.go`):

```go
type ContentType interface {
	Name() string                                // canonical "Namespace/Name"
	ContentSchema() Schema                       // CUE artifact + definition selector
	DecodeJSON(json.RawMessage) (Content, error) // usually content.DecodeContent[T]
	RenderMarkdown(Record) (string, error)       // Markdown body only
}
```

- `Name` must match `Namespace/Name` (letters and digits). Pick a namespace
  per extension (`Slack/...`).
- `ContentSchema` describes **one** inner content value, even for groupable
  types. Host metadata is never part of it.
- `RenderMarkdown` returns only the body. The host adds the directive, the
  structured header, and block separators. Embed
  `content.ItemMarkdown(render)` (individual records) or
  `content.CollectionMarkdown(render)` (groupable collections) instead of
  decoding records by hand.

`core.MarkdownType` is a complete minimal example.

## Optional capabilities

Implement these small interfaces only when the type needs them. The registry
checks for them with type assertions.

| Interface | Purpose | Example |
|-----------|---------|---------|
| `content.MarkdownHeader` | Project selected fields into the `yaml infrapad` header, and apply edited header fields. | `Core/Markdown`, `Prometheus/AlertsMatcher`, `Slack/Thread`… |
| `content.MarkdownUpdater` | Apply an edited Markdown body (`UpdateMarkdown(existing, body)`). | `Core/Markdown`, `AIAgent/Message` |
| `content.Presenter` | Fixed UI cards: title, Markdown, labeled fields, labeled links. | `AIAgent/Message`, `Slack/Trhead`… |
| `content.Expander` | Turn a transient payload into the complete desired generated output and/or updated primary state. | `Slack/Thread`, `Prometheus/AlertsMatcher` |
| `content.GroupableContentType` | Store multiple keyed items in one block that the host groups. | `Slack/Conversation` |

### Markdown header and body

- The header is a **projection** that the extension chooses, not a dump of
  the record. Use `content.ItemMarkdownHeader(project, apply)` with a small
  header struct. `omitempty` fields are left out of the header, and edited
  headers are decoded strictly.
- Header updates replace the projected fields (omitted optional fields are
  removed). Body-owned fields and host metadata must be preserved.
- `UpdateMarkdown` receives only the body. It never sees the header and
  doesn't interpret YAML fences in the body. Return
  `content.WithContent(existing, newValue)` so that the type, timestamp, and
  key are preserved.
- Neither method may mutate `existing` argument.
- Without an updater, body edits are rejected (`UNSUPPORTED_EDIT`). Without a
  header capability, header edits are rejected. The alert matcher has an
  editable header but no body updater.
- Having these capabilities never makes generated blocks editable. The
  host's ownership policy rejects direct edits of any block with a parent.

#### The `text` field, Markdown bodies, and editing

- By convention, a type whose content has a Markdown body stores it in a
  `text` field (`Core/Markdown`, `AIAgent/Message`, `Slack/Conversation`,
  `PagerDuty/Incident`). The host doesn't interpret `text`; it's an
  ordinary content field.
- Keep body text out of the structured header: the header projects only
  structured fields (e.g. PagerDuty's `id`/`url`), the body carries `text`.
- Rendering still belongs to the extension: `RenderMarkdown` decides what
  the body is. Storing `text` doesn't make it editable by itself.
- Editing authority comes only from the capabilities above and the host
  ownership rules. `Core/Markdown` implements `MarkdownUpdater`;
  `PagerDuty/Incident` deliberately doesn't, because its `text` is a
  summary generated from the last payload and is replaced on refresh.
- A header updater may reject edits based on the current state. PagerDuty
  allows changing the `id`/`url` reference only while `text` is absent
  (before the first successful payload submission); afterward it accepts
  only an unchanged header, so Markdown round-trips still work. It implements
  `UpdateMarkdownHeader` itself, around `content.ItemMarkdownHeader`.

### Presentation

`Present` returns a `content.Presentation` with fixed cards. The UI
sanitizes the Markdown and accepts only HTTP(S) and relative links. Don't
include host metadata such as timestamps or block numbers; the UI already
shows them. Types without `Presenter` are shown through their sanitized
Markdown body. If presentation fails, the UI also falls back to the Markdown
body.

### Expansion: generated output

Some content types are **references** to external content, such as a
`Slack/Thread` pointing at a Slack permalink. Their blocks store only the
reference. When a producer submits a transient payload for the reference
(a complete Slack thread), the server asks the type's `Expander` to
compute the **generated child blocks** that should exist for it: the
conversation and agent messages. This computation is *expansion*. Its output
is owned by the reference block and can't be edited directly. Refresh is achieved
by resubmitting a new payload. Expansion can cascade: if the output itself
contains another expandable reference, the host expands that too, using a
transient payload supplied with it. The host persists only the final records,
never the payloads.

An `Expander` declares a `PayloadSchema()` and implements:

```go
Expand(ctx, existing Record, payload json.RawMessage) (ExpansionResult, error)
```

- **Complete desired output.** Return every block that should exist for
  this payload, not a delta. The host compares it with the stored output.
  It keeps block numbers, revises only changed blocks, adds new blocks,
  deletes output that is no longer returned, and creates no revisions for
  identical submissions. An explicit empty result removes all generated
  output.
- **Stable keys.** Every emitted record is keyed
  (`content.NewKeyedItem(ct, key, ts, value)`). Derive keys from source
  identities, for example `slack.MessageKey(ts)`. Keys must be unique within
  the generating parent.
- **Event timestamps.** Use the source event time. The host orders all
  blocks in the document by timestamp, so output from different integrations
  interleaves.
- **Grouping.** Groupable records are emitted as one collection
  (`NewCollection`). The emitted boundaries are only a suggestion: the host
  splits groups around interleaved content and preserves existing
  boundaries. A type can't be both groupable and an expander.
- **Cross-extension output.** Records of another extension's type are
  allowed. Use the instance registered in the registry: `slack.NewThreadType`
  receives the registered `*aiagent.MessageType`. Use `extras` for
  source-specific metadata (Slack emits `slack_ts`/`slack_user_id` in agent
  messages).
- **Downstream expansion.** An emitted record of an expandable type is
  expanded too, using `EmittedBlock.ExpansionPayload` as its transient
  payload. The host expands depth-first, rejects cycles and trees deeper
  than eight levels, and commits the whole tree atomically. See the
  test-only `Test/Ref` in `server/internal/testext`.
- **Primary state.** `ExpansionResult.Record` is the resulting primary
  record. Return `existing` when the primary doesn't change. An expander
  may emit no children and only update the primary:
  `Prometheus/AlertsMatcher` replaces its retained `query_results` (kept
  out of the Markdown header). Its header updater preserves them only
  while the labels matchers and incident bounds are unchanged.
- **Primary timestamps.** An expander may also move the primary's event
  timestamp (PagerDuty/Incident and Cora/Report do it on every valid
  payload, Slack/Thread on every non-empty one): return the updated record
  through `ExpansionResult.Record` rather than adding a host capability.
  Copy records with the shared helpers instead of rebuilding them:
  `content.WithContent(existing, value)` replaces the inner value while
  preserving type, timestamp, and key, and `content.WithTimestamp(existing,
  ts)` replaces the timestamp while preserving content and key. Neither
  mutates the input, and the host permits timestamp changes to the primary
  on reconciliation.
- **Errors.** Return `*content.ValidationError` (with `StagePayload` and
  field paths) for semantic payload problems that the schema can't express.
  Any error rejects the whole submission, and the document stays unchanged.

The extension doesn't persist anything, assign block numbers, or match
stored blocks. Reconciliation is entirely the host's job.

## Schemas and validation

The registry validates every payload and every inner content value against
the compiled CUE definitions, including output emitted by other
extensions. Unknown fields are rejected everywhere except inside an `extras`
field (`map[string]any`), which accepts arbitrary metadata. Host record
metadata is checked separately.

A `json.RawMessage` field generates CUE's `_` constraint, meaning “any
value”: an object, array, string, number, boolean, or null. Schema validation
still requires valid JSON, but imposes no structure on that field or its
nested values. PagerDuty uses this for transient alert `details`, whose
shape varies by integration. For example, `"details": {"custom": [1, "x"]}`
is allowed, but an unknown field alongside `details` in the alert is still
rejected. This exception applies only inside `details`; the surrounding
objects remain strict. Matcher code validates recognized detail formats
separately.

Schemas are generated from Go types (see [server/README.md](../server/README.md#extension-cue-schemas)):

- `<pkg>/schema/<name>_go_gen.cue`: raw `cue get go` output (generated).
- `<pkg>/schema/*_patch.cue`: optional handwritten constraints, such as
  nonblank strings or URL patterns. See `aiagent/schema/message_patch.cue`
  and `slack/schema/constraints_patch.cue`.
- `<pkg>/schema.cue`: the self-contained artifact embedded with
  `//go:embed schema.cue` (generated).

Reference definitions from Go with selectors, for example
`content.Schema{Source: schemaCUE, Definition: "#Message"}`.

## Author checklist

1. Create `server/pkg/extensions/<name>` with the inner content Go types
   (plus payload types for expanders). Add `Extras map[string]any
   \`json:"extras,omitempty"\`` where unanticipated metadata is useful.
2. Implement `content.ContentType` and the capabilities you need. Expose a
   `ContentTypes(...) []content.ContentType` constructor.
3. Add the package to `INPUTS` in `server/scripts/extension-schemas.sh`, add
   optional `schema/*_patch.cue` constraints, and run
   `task server:extensions:schema:generate`. CI-style checks use
   `task server:extensions:schema:check`.
4. Embed `schema.cue` and return the selectors from `ContentSchema` (and
   `PayloadSchema`).
5. Register the types in the explicit registry in `server/cmd/start.go`
   (`content.NewRegistry(...)`). Registration fails at startup for invalid
   names, duplicates, groupable expanders, or schema selectors that don't
   resolve.
6. Verify through the registry in package tests: build a registry with your
   types (and any types you emit), then call `ValidatePayload`, `Expand`, and
   `ValidateExpansion`. Also check Markdown rendering and presentation. See
   `slack/slack_test.go` (`expand`) and `aiagent/aiagent_test.go`.
7. Rebuild and restart the server, then try the type through the REST API
   and Markdown workflow (see [content-producers.md](content-producers.md)).
   No CLI or UI changes are needed for types that use the existing
   presentation primitives.
