# InfraPad Markdown format

InfraPad documents can be exported to Markdown, edited, and pushed back. The
server renders, interprets, and validates this format. The CLI
(`infrapad md pull/push`) only transfers files. See
[infrapad-markdown-example.md](infrapad-markdown-example.md) for a complete
document. The structural parser is in `shared/markdown`.

## Structure

````markdown
---
document: 42cd704a-f697-4a78-9c29-3c7235c9500f
title: Checkout latency incident
namespace: payments
status: active
---
::infrapad_block{block=1 rev=2 type=Prometheus/AlertsMatcher author=jane timestamp=2026-07-12T12:42:03Z}
```yaml infrapad
content:
  labels_matchers:
    - alertname:
        - CheckoutLatencyHigh
  since: "2026-07-12T12:42:00Z"
```
::infrapad_block{block=2 rev=1 type=Core/Markdown author=jane timestamp=2026-07-12T12:50:00Z}
# Notes

Free-form Markdown body.
````

A document has three parts:

1. **YAML frontmatter** with `document` (ID), `title`, `namespace`, and
   `status`. The `document` field identifies the target of a push. The other
   fields are informational and are ignored on push.
2. One **`::infrapad_block{...}` leaf directive** per block, on its own line.
3. After each directive, an optional **structured header** followed by the
   **body**, which extends to the next directive or the end of the file.

The directive uses the leaf form of the [generic directives][directives-spec]
syntax. Text directives aren't used because they would collide with
ordinary text such as `12:42`.

[directives-spec]: https://talk.commonmark.org/t/generic-directives-plugins-syntax/444

## Directive attributes

| Attribute | Meaning |
|-----------|---------|
| `block` | Block number, assigned by the server. |
| `rev` | Revision the file was based on, assigned by the server. It's used to detect edits and conflicts. |
| `type` | Canonical content type (`Core/Markdown`, `Slack/Conversation`, ...). |
| `author` | Author of the revision, if known. |
| `parent` | Generating block. It's present only on generated blocks. |
| `timestamp` | Event time in RFC 3339 with nanosecond precision. |

- Values containing spaces, quotes, `\`, `{`, `}`, `=`, or line breaks are
  double-quoted, using backslash escapes (`\\`, `\"`, `\n`, `\r`).
- Blocks appear in chronological order: event timestamp, then block number.
  Their position in the file doesn't represent block numbers. Generated
  blocks from different integrations can interleave with ordinary blocks.
- `block`, `rev`, `type`, `author`, `parent`, and `timestamp` of existing blocks can't be
  changed through Markdown (`HOST_METADATA_CHANGED`). Block status isn't
  exported. Deleted blocks are omitted.

## Structured header

A fence with the info string `yaml infrapad` **immediately** after the
directive is the block's structured header. Its fields are nested under
`content:`:

````
::infrapad_block{block=3 rev=1 type=AIAgent/Message parent=1 timestamp=2026-01-01T10:01:00.0002Z}
```yaml infrapad
content:
  agent: triage-bot
  extras:
    slack_ts: "1767261660.000200"
  url: https://example.slack.com/archives/C0PAYMENTS/p1767261660000200
```
Latency correlates with the `checkout-db` failover.
````

- The content type selects which fields appear in the header. The header
  isn't a dump of the API record: keys, timestamps, and grouped membership
  aren't included. For example, `Core/Markdown` shows only `extras`, the
  alert matcher shows all its fields, and `Slack/Conversation` has no
  header.
- If the header contains backticks, the server uses a longer fence.
- Any other fence, including a plain ` ```yaml ` fence, is part of the body.

## Body

The body is the content type's Markdown rendering. For `Core/Markdown` and
`AIAgent/Message`, it's the text itself. For `Prometheus/AlertsMatcher`,
it's empty. For `Slack/Conversation`, it's a rendered list of messages.

## Push behavior

`infrapad md push` sends the file unchanged to
`POST /v1/documents/{id}/markdown`. The server then:

1. Parses the structure. Malformed framing is rejected
   (`INVALID_MARKDOWN`).
2. Compares each existing block with the revision named by its `rev`.
   Headers are compared by value and bodies ignoring trailing newlines.
   Untouched blocks are skipped, including stale and generated ones.
3. Applies edited headers (`UpdateMarkdownHeader`) and then edited bodies
   (`UpdateMarkdown`) through the content type. It validates the result
   against the type's schema. A result identical to the stored record
   creates no revision.
4. Creates `block=new` sections in the order they appear, from their header
   and body. Generated-only types such as `Slack/Conversation` can't be
   created.
5. Commits everything in one transaction and returns the refreshed Markdown
   and the list of added and updated blocks. The CLI writes the refreshed
   Markdown back to the file.

Rejections leave the document unchanged:

| Reason | HTTP | Cause |
|--------|------|-------|
| `GENERATED_BLOCK` | 403 | An edited block has a `parent`. Generated content is read-only. |
| `STALE_REVISION` | 409 | An edited block's `rev` is no longer the latest. Pull and reapply. |
| `DELETED_BLOCK` | 400 | An edited block was deleted meanwhile. |
| `UNSUPPORTED_EDIT` | 400 | The type doesn't support editing that part (for example, the alert matcher body). |
| `INVALID_CONTENT` | 400 | Schema validation failed. Unknown fields are rejected outside `extras`. |

Removing a block from the file **doesn't** delete it. Markdown push doesn't
support deletion.

## Offline inspection

`infrapad md parse --file doc.md` prints the frontmatter and, for each
block, its directive attributes, header (as generic YAML), and raw body. It
works without a server and only checks the structure. Content types,
schemas, permissions, and stale revisions are checked by the server on push.
