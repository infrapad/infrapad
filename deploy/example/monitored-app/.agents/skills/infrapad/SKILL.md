---
name: infrapad
description: Work with infrapad documents using the infrapad CLI. Use for pulling, pushing, reading, and writing infrapad-flavoured markdown documents.
---

# Infrapad

Infrapad is a collaborative incident/change tracking platform. Documents are stored on an infrapad server and can be pulled/pushed as markdown files using the `infrapad` CLI.

## Document Structure

An infrapad document consists of:

- **Front matter** (YAML between `---` fences) with metadata: `document` (ID), `title`, `namespace`, `status`.
- **Blocks**, each starting with a directive line set by the server:

```
::infrapad_block{block=4 rev=1 type=AIAgent/Message parent=3 timestamp=2026-11-10T16:30:15Z}
```

  - `block`/`rev`: block number and the revision your file is based on.
  - `type`: the content type, e.g. `Core/Markdown`, `Prometheus/AlertsMatcher`,
    `Slack/Thread`, `Slack/Conversation`, `AIAgent/Message`.
  - `author`, `parent`, `timestamp`: server-managed metadata. `parent=N` marks a
    **generated** block (e.g. Slack messages produced from the thread block N).
- An optional **structured header**: a `` ```yaml infrapad `` fence directly after
  the directive, with the type's fields nested under `content:`. Other code
  fences (including plain `` ```yaml ``) are ordinary body text.
- The **body**: Markdown up to the next directive.

Blocks are listed in chronological order (event timestamp), not by block number,
so Slack messages and agent replies interleave with alerts and notes.

### Example document

See `deploy/example/monitored-app/tmp/incident.md` (after running
`simulate.sh start`) for a real document. The general structure is:

    ---
    document: abc-123
    title: Example incident
    namespace: monitored-app
    status: active
    ---
    ::infrapad_block{block=1 rev=1 type=Prometheus/AlertsMatcher timestamp=2026-01-01T00:00:05Z}
    ```yaml infrapad
    content:
      labels_matchers:
        - alertname:
            - MonitoredAppEndpointDown
          endpoint:
            - endpoint1
      since: "2026-01-01T00:00:00Z"
    ```
    ::infrapad_block{block=2 rev=1 type=Core/Markdown timestamp=2026-01-01T00:00:06Z}
    # Investigation notes

    Some notes about the incident.
    ::infrapad_block{block=4 rev=1 type=AIAgent/Message parent=3 timestamp=2026-01-01T00:00:09Z}
    ```yaml infrapad
    content:
      agent: infrapad-triage
    ```
    Both endpoints are failing together.

### Editing rules

- **Never edit generated blocks** (those with `parent=`): Slack conversations and
  AI-agent messages are refreshed from their source. Editing them makes the whole
  push fail (`GENERATED_BLOCK`).
- **Never change directive lines** of existing blocks (type, author, parent,
  timestamp are host metadata) and keep `yaml infrapad` headers intact unless you
  intentionally change a structured field (e.g. an alert matcher's `until`).
- **Add your notes as new blocks** (`block=new`, see below) rather than rewriting
  existing ones. Deleting a block from the file does not delete it on the server.

## CLI Commands

The infrapad CLI communicates with the configured HTTP proxy or server. Its
local-development default is `http://localhost:8089`; override it with
`--api-url` or `INFRAPAD_API_URL`. If `INFRAPAD_TOKEN` is non-empty, the CLI
sends its opaque value as a bearer credential on every remote request. It sends
no authorization header when the variable is unset or empty.

### Markdown Pull/Push Workflow

This is the primary workflow for reading and writing document content.

**Pull** — download a document as infrapad-flavoured markdown:

```bash
infrapad md pull --document <name> --file <path>
# Omit --file to print to stdout
```

**Push** — upload local changes back to the server:

```bash
infrapad md push --file <path>
```

The server compares the file with the revisions it was pulled from and applies only the blocks you edited plus new `block=new` blocks, atomically (all or nothing). The output reports the updated/added blocks, and the file is rewritten with the refreshed document (new block numbers, revisions).

If the push fails with a stale-revision conflict (HTTP 409), someone changed the same block meanwhile: pull again and reapply your edit. Untouched blocks never conflict.

### Typical Agent Workflow

1. **Pull** the document to a local file: `infrapad md pull --document <name> --file incident.md`
2. **Read** the file to understand the current state.
3. **Edit** the file: append new blocks or modify existing ordinary (non-generated) blocks.
4. **Push** changes back: `infrapad md push --file incident.md` (the server applies edited and new blocks).

### Adding a New Block via Markdown

To add a new block, append it to the pulled markdown file with `block=new`:

```
::infrapad_block{type=Core/Markdown block=new}
# My New Section

Content goes here.
```

Then push with: `infrapad md push --file incident.md`
