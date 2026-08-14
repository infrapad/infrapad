---
name: infrapad
description: Work with infrapad documents using the infrapad CLI. Use for pulling, pushing, reading, and writing infrapad-flavoured markdown documents.
---

# Infrapad

Infrapad is a collaborative incident/change tracking platform. Documents are stored on an infrapad server and can be pulled/pushed as markdown files using the `infrapad` CLI.

## Document Structure

An infrapad document consists of:

- **Front matter** (YAML between `---` fences) with metadata: `doc` (ID), `title`, `namespace`, `status`.
- **Blocks** — numbered content sections, each prefixed with a directive line:

```
::infrapad_block{block=1 rev=1 type=alerts_matcher}
```

Block types can by:

- structured (e.g. `alerts_matcher`): structured YAML in a fenced code block
- free-form markdown

### Example document

See `deploy/example/monitored-app/tmp/incident.md` for a real-world example of an infrapad document. The general structure is:

    ---
    doc: abc-123
    title: Example incident
    namespace: monitored-app
    status: active
    ---
    ::infrapad_block{block=1 rev=1 type=alerts_matcher}
    ```yaml
    LabelsMatchers:
      - endpoint: [endpoint1]
        name: [MonitoredAppEndpointDown]
    Since: "2026-01-01T00:00:00Z"
    Until: "0001-01-01T00:00:00Z"
    ```
    ::infrapad_block{block=2 rev=1 type=markdown}
    # Investigation notes

    Some notes about the incident.

## CLI Commands

The infrapad CLI communicates with the server via gRPC (default `localhost:50051`, override with `--grpc-addr`).

### Markdown Pull/Push Workflow

This is the primary workflow for reading and writing document content.

**Pull** — download a document as infrapad-flavoured markdown:

```bash
infrapad md pull --doc <name> --file <path>
# Omit --file to print to stdout
```

**Push** — upload local changes to a specific block:

```bash
# Push changes to an existing block (by number):
infrapad md push --file <path> --block <number>

# Push a new block (add `::infrapad_block{... block=new}` section to the file first):
infrapad md push --file <path> --block new
```

### Typical Agent Workflow

1. **Pull** the document to a local file: `infrapad md pull --doc <name> --file incident.md`
2. **Read** the file to understand the current state.
3. **Edit** the file — modify existing blocks or append new ones.
4. **Push** changes back: `infrapad md push --file incident.md --block <number>` (or `--block new` for new blocks).

### Adding a New Block via Markdown

To add a new block, append it to the pulled markdown file with `block=new`:

```
::infrapad_block{type=markdown block=new}
# My New Section

Content goes here.
```

Then push with: `infrapad md push --file incident.md --block new`
