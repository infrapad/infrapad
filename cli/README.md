# Infrapad CLI

Command-line interface for the Infrapad server.

## Build & Install

```bash
# Build the binary
task build

# Install to $GOPATH/bin
task install
```

## Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--grpc-addr` | `localhost:50061` | gRPC server address (also `GRPC_ADDR` env var) |
| `-o, --output` | `table` | Output format: `table` or `json` |

## Commands

### `infrapad document`: Manage documents

**Create a document:**

```bash
infrapad document create --title "Payment service crash loop" --namespace payments
```

| Flag | Required | Description |
|------|----------|-------------|
| `--title` | yes | Document title |
| `--namespace` | yes | Document namespace |

**List all documents:**

```bash
infrapad document list
```

**Get a document by name:**

```bash
infrapad document get <id>
```

### `infrapad block`: Manage blocks within a document

Blocks are the building units of a document. Each block has a type (e.g.
`markdown`, `alerts_matcher`), a sequential block number, and a revision number
that increments on every update.

**Add a block:**

```bash
infrapad block add --document <id> --type markdown \
  --content '{"text": "initial investigation writeup\nneeds further analysis"}'
```

| Flag | Required | Description |
|------|----------|-------------|
| `--document` | yes | Parent document name |
| `--type` | yes | Block type (e.g. `markdown`, `alerts_matcher`) |
| `--content` | no | Block content as a JSON object (default `{}`) |

**Update a block:**

```bash
infrapad block update --document <id> --block-number 1 --type alerts_matcher \
  --content '{"LabelsMatchers": [{"name": ["CrashLoopBackOff"]}, {"name": ["KubeNodeNotReady"]}]}'
```

| Flag | Required | Description |
|------|----------|-------------|
| `--document` | yes | Parent document name |
| `--block-number` | yes | Block number to update |
| `--type` | yes | Block type |
| `--content` | no | Block content as a JSON object (default `{}`) |

**Get a block:**

```bash
infrapad block get --document <id> --block-number 1
```

**List blocks in a document:**

```bash
infrapad block list --document <id>
```

**Show revision history for a block:**

```bash
infrapad block history --document <id> --block-number 1
```

### `infrapad md`: Markdown workflow

The markdown commands provide a file-based workflow for reading and writing
document content. Documents are represented as infrapad-flavoured markdown:
YAML front matter followed by block sections separated by `::infrapad_block`
directives.

#### Document format

    ---
    document: 1e7c6823-8d2a-4013-907b-7a42144b765d
    title: Monitored app incident
    namespace: monitored-app
    status: active
    ---
    ::infrapad_block{block=1 rev=2 type=alerts_matcher}
    ```yaml
    LabelsMatchers:
      - endpoint: [endpoint1]
        name: [MonitoredAppEndpointDown]
    Since: "2026-08-20T15:15:03Z"
    Until: "2026-08-20T15:19:08Z"
    ```
    ::infrapad_block{block=2 rev=1 type=markdown}
    # Investigation Summary

    Both endpoints are returning HTTP 500.

- **Front matter** contains the document metadata (`document`, `title`, `namespace`, `status`).
- **Block directives** (`::infrapad_block{block=N rev=R type=T}`) mark the start of each block.
- **Structured blocks** (e.g. `alerts_matcher`) wrap their content in a `` ```yaml `` fence.
- **Markdown blocks** contain free-form markdown text.

#### Pull: download a document as markdown

```bash
# Write to a file
infrapad md pull --document <id> --file incident.md

# Print to stdout
infrapad md pull --document <id>

# Pull latest changes to a previously synchronized file
infrapad md pull --file incident.md
```

| Flag | Required | Description |
|------|----------|-------------|
| `--document` | yes for new files | Document name or ID |
| `--file` | no | Output file path (prints to stdout if omitted) |

#### Push: upload local changes back to the server

```bash
infrapad md push --file incident.md
```

The command automatically detects which blocks have been modified locally
(by comparing with the current server state) and pushes only the changed
blocks. New blocks (with `block=new` in the directive) are always pushed.

| Flag | Required | Description |
|------|----------|-------------|
| `--file` | yes | Path to the markdown file |

After pushing, the file is automatically refreshed from the server to reflect
the latest state (updated revision numbers, assigned block numbers for new blocks).

#### Parse: inspect a markdown file

```bash
infrapad md parse --file incident.md
```

Prints the parsed structure of the file (document metadata, block types, content)
without contacting the server. Useful for verifying edits before pushing.

## Typical Workflow

### Pull -> Edit -> Push

The standard workflow for working with infrapad documents:

```bash
# 1. Pull the document
infrapad md pull --document <id> --file incident.md

# 2. Read and edit the file locally
#    (modify existing blocks or append new ones)

# 3. Push changes back to the server
infrapad md push --file incident.md
#    Only modified and new blocks are pushed.
```

### Adding multiple new blocks at once

Multiple `block=new` sections can be added to a file and pushed together:

```bash
cat >> incident.md <<'EOF'

::infrapad_block{type=markdown block=new}
# Investigation Summary

Root cause: OOM kill due to memory limit set too low.

::infrapad_block{type=markdown block=new}
# Recommended Actions

1. Increase memory limit from 256Mi to 512Mi.
2. Add horizontal pod autoscaler.
EOF

infrapad md push --file incident.md
```

Both blocks are pushed in order, and the file is refreshed with their assigned
block numbers and revision 1.

## Testing

```bash
# Unit tests
task test:unit

# E2E tests (requires a running infrapad server)
task test:e2e

# All tests
task test:all
```
