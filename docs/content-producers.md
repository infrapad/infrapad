# Submitting content snapshots to InfraPad

This guide is for producers that mirror external content (for example a
Slack incident thread) into InfraPad: it covers creating an expandable
primary block and refreshing it through the `submitPayload` API. For editing
ordinary document content, see the [CLI Markdown
workflow](../cli/README.md#infrapad-md-markdown-workflow); for adding content
types to the server, see [content-extensions.md](content-extensions.md).

## Concepts

- An expandable content type is a **reference** to external content, such as
  a `Slack/Thread` block holding a Slack permalink. The block itself stores
  only the reference.
- You refresh it by submitting a **payload snapshot**: the complete external
  state (the whole thread). The server's extension validates the payload,
  expands it into **generated child blocks** (`Slack/Conversation` messages
  and `AIAgent/Message` replies), and reconciles them with what is already
  stored. Payloads are never persisted.
- Generated blocks record their generating parent, can't be edited directly,
  and are refreshed only by resubmitting a snapshot.
- Blocks are ordered by event timestamp, then block number. Generated Slack
  messages use the Slack message time, so they interleave with other content
  in the document.

## Walkthrough

This walkthrough targets the local development stack (`task pd:dev:run` and
`task server:run`; see [DEVELOPMENT.md](../DEVELOPMENT.md)). It needs `curl`
and `jq`. Against a real deployment, set `INFRAPAD_API_URL` and use a token
from your identity provider instead of the development `/auth/token` route.

```bash
API=${INFRAPAD_API_URL:-http://localhost:8089}
export INFRAPAD_TOKEN=$(curl -fsS --data-urlencode 'username=producer-demo' "$API/auth/token")
api() {
  curl -sS --fail-with-body -H "Authorization: Bearer $INFRAPAD_TOKEN" \
    -H 'Content-Type: application/json' "$@"
}
```

### 1. Create a document and a Slack thread primary

```bash
DOC=$(api -X POST "$API/v1/documents" \
  -d '{"title": "Checkout latency incident", "namespace": "payments"}' | jq -r '.document.name')

THREAD=$(api -X POST "$API/v1/$DOC/blocks" -d '{
  "block": {
    "type": "Slack/Thread",
    "record": {"content": {"url": "https://example.slack.com/archives/C0PAYMENTS/p1767261600000100"}}
  }
}' | jq -r '.block.blockNumber')
echo "$DOC block $THREAD"
```

`$DOC` is the resource name (`documents/<id>`). The thread URL must be a
Slack thread permalink (`https://<workspace>/archives/<channel>/p<16 digits>`).
The server derives message permalinks from it.

### 2. Submit a complete snapshot

The request body is `{"payload": ...}`. The payload is the complete thread
(the parent and all replies) and a per-snapshot map from Slack user ID to
agent name. Messages from mapped users become `AIAgent/Message` blocks. All
other messages become `Slack/Conversation` items. The server validates the
payload against the thread type's schema and its semantic rules before
expanding it (see [Payload validation](#payload-validation)).

```bash
cat > snapshot.json <<'EOF'
{
  "payload": {
    "agents": {"U_TRIAGE": "triage-bot"},
    "messages": [
      {"ts": "1767261600.000100", "user": "U_ALICE", "user_name": "Alice",
       "text": "Checkout p99 latency is above 2s since 10:00 UTC."},
      {"ts": "1767261660.000200", "user": "U_TRIAGE", "user_name": "Triage bot",
       "text": "Latency correlates with the 09:58 `checkout-db` failover."},
      {"ts": "1767261720.000300", "user": "U_BOB", "user_name": "Bob",
       "text": "Confirmed, the connection pool is still reconnecting.",
       "previews": [{"title": "CheckoutLatencyHigh", "title_link": "https://alerts.example/checkout",
                     "updated_at": "2026-01-01T10:02:00Z", "text": "p99 > 2s",
                     "fields": [{"label": "Severity", "value": "warning"}]}]}
    ]
  }
}
EOF
api -X POST "$API/v1/$DOC/blocks/$THREAD:submitPayload" -d @snapshot.json \
  | jq -r '.document.blocks[] | "\(.blockNumber) rev=\(.revisionNumber) \(.type) parent=\(.parentBlockNumber // "-")"'
```

```text
2 rev=1 Slack/Conversation parent=1
3 rev=1 AIAgent/Message parent=1
4 rev=1 Slack/Conversation parent=1
1 rev=1 Slack/Thread parent=-
```

The agent reply splits the human messages into two conversation blocks. The
messages are from January 2026, so they sort before the thread reference,
whose event time is its creation time. The response contains the complete
refreshed document, including `presentation` and `markdown` for each block.

### 3. Refresh the snapshot

Submitting the same file again changes nothing: all revisions stay at 1.
Now edit a message and append a reply. The edit keeps the message's `ts`,
which is its identity:

```bash
jq '.payload.messages[0].text = "Checkout p99 latency is above 2s since 10:00 UTC (EU only)."
    | .payload.messages += [{"ts": "1767261780.000400", "user": "U_ALICE", "user_name": "Alice",
                              "text": "Pool recycled; latency back to normal."}]' \
  snapshot.json > snapshot-2.json
api -X POST "$API/v1/$DOC/blocks/$THREAD:submitPayload" -d @snapshot-2.json \
  | jq -r '.document.blocks[] | "\(.blockNumber) rev=\(.revisionNumber) \(.type)"'
```

```text
2 rev=2 Slack/Conversation
3 rev=1 AIAgent/Message
4 rev=2 Slack/Conversation
1 rev=1 Slack/Thread
```

Block numbers stay the same. Only the changed conversation blocks get new
revisions; the earlier content is still in
`GET /v1/$DOC/blocks/2/history`. The generated blocks also appear in the
UI and in exported Markdown; agents editing a document must leave them
untouched.

## Snapshot semantics

- **Complete replacement.** Every submission describes the entire source
  state. The server compares the output with the stored generated blocks:
  - unchanged blocks keep their revision,
  - changed blocks get a new revision under the same block number,
  - new items are added,
  - generated output that isn't in the snapshot is deleted (history is
    kept).

  Never send only the new messages.
- **Stable identities.** For Slack, a message's identity is its `ts`. Keep it
  unchanged when message text or previews change. Changing the agent mapping
  moves the message between conversation and agent blocks.
- **Identical submissions** create no revisions. Retrying is safe.
- **Empty snapshot.** `{"payload": {"messages": []}}` removes all generated
  output. Missing or `null` `messages` are rejected.
- **Grouping.** The server chooses the conversation block boundaries. It
  splits around interleaved content and doesn't merge existing blocks just
  because a message between them disappears.
- **Targets.** Submit only to live, non-generated blocks whose type is
  expandable. Other targets are rejected (`EXPANSION_UNSUPPORTED`,
  `GENERATED_BLOCK`, `DELETED_BLOCK`, `BLOCK_NOT_FOUND`). Concurrent
  submissions to one document are serialized, and the last one wins.
- **Atomic failures.** Any validation or extension failure rejects the
  whole submission, and the document stays unchanged. Errors use standard
  gRPC details: `ErrorInfo` (stable `reason`, block, and content-type
  metadata) and `BadRequest` field violations. For example:

  ```text
  HTTP 400 INVALID_PAYLOAD: Slack/Thread: invalid payload: messages.0.color: field not allowed
  ```

  Unknown fields are rejected everywhere except inside `extras` objects.

## Payload validation

Every expandable type declares a schema for its payload, validated before
any expansion runs. For `Slack/Thread` it is generated from the Go
`slack.Snapshot` type (see the [schema workflow](../server/README.md#extension-cue-schemas)).
Validation is strict:

- Unknown fields are rejected. The example error above (`messages.0.color:
field not allowed`) is exactly this check: a Slack API field the payload
schema doesn't define.
- Field types are checked (`messages` must be an array of objects, `ts` a
  string, `agents` a string map, ...).
- Only `extras` objects accept arbitrary metadata.

On top of the schema, the extension applies semantic checks that a schema
can't express. For Slack snapshots:

- `ts` must be a well-formed Slack timestamp
  (`1767261600.000100`) and unique within the snapshot.
- `agents` keys and values must be nonblank.
- `messages` must be an explicit array: missing or `null` is rejected, an
  empty array means "no generated output".

Failures reject the whole submission with `INVALID_PAYLOAD` and the field
paths in the `BadRequest` details (for example
`payload.messages.1.ts`). The extension's generated output is validated
against the same strict schemas, so an invalid snapshot can never land as
partial content.

## Producer responsibilities

The server doesn't fetch anything from Slack or other external systems, and
it doesn't convert raw Slack API responses. The producer:

- fetches and aggregates the **complete** thread (paging through replies)
  before each submission,
- resolves user names (`user_name`) and, optionally, mention-resolved text
  (`readable_text`),
- converts raw attachments and unfurls into typed `previews` (`title`,
  `title_link`/`url`, `text`, `fields`, `updated_at`, ...),
- decides which Slack users are AI agents (`agents`),
- decides when to refresh (polling, events, or a final snapshot).

See `slack.Snapshot` in `server/pkg/extensions/slack/slack.go` for the
complete payload schema.

## Fuller example

`deploy/example/monitored-app/simulate.sh` runs a complete incident against
a live Prometheus demo: staged snapshots produced by `simulate-slack.py`
(initial thread, edited message with agent triage, recovery replies) and
Markdown editing of ordinary blocks. See its
[README](../deploy/example/monitored-app/README.md#incident-simulation).
