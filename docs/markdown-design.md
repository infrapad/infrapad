# Infrapad Markdown Format

This document describes how the infrapad document model maps to a
markdown-based serialization format. See
[infrapad-markdown-example.md](infrapad-markdown-example.md) for a
complete example.

## Document model recap

An infrapad **Doc** is an ordered sequence of **Blocks**. Each block
has a type, content, and per-revision metadata.

```
Doc
  Uid          string        – unique document identifier
  Title        string
  Namespace    string
  Status       "active" | "archived"

Block
  BlockNumber     int        – position in the document (1-based, monotonic)
  RevisionNumber  int        – version of this block (1-based, monotonic)
  AuthorID        string     – who created this revision
  Type            string     – determines content schema ("markdown", "alerts_matcher", …)
  Status          string     – "progressing" | "published" | "deleted"
  Content         varies     – type-specific payload
```

## Directive syntax

The format uses the [generic directives/plugins syntax][directives-spec]
proposed for CommonMark. Directives provide a standard way to extend
markdown with custom elements without inventing ad-hoc syntax. The
three tiers are:

- **Text directives** (single colon) — inline: `:name[label]{attrs}`
- **Leaf directives** (double colon) — block, no content: `::name[label]{attrs}`
- **Container directives** (triple colon) — block with content: `:::name … :::`

Infrapad uses **leaf directives** (`::infrapad_block{…}`) as block
boundary markers. The reference implementation of the syntax is
[micromark-extension-directive][micromark-directive].

[directives-spec]: https://talk.commonmark.org/t/generic-directives-plugins-syntax/444
[micromark-directive]: https://github.com/micromark/micromark-extension-directive

## Mapping to markdown

### Frontmatter: Doc metadata

YAML frontmatter carries document-level fields:

```yaml
---
doc: 42cd704a-f697-4a78-9c29-3c7235c9500f
title: Payment service crash loop
namespace: payments
status: active
---
```

### Block boundaries: `::infrapad_block` leaf directives

Each block starts with a leaf directive (double-colon) that carries the
block metadata as attributes:

```
::infrapad_block{type=alerts_matcher block=1 rev=2 author=incident_detector:123}
```

| Attribute | Model field            |
|-----------|------------------------|
| `type`    | `Block.Type`           |
| `block`   | `Block.BlockNumber`    |
| `rev`     | `Block.RevisionNumber` |
| `author`  | `Block.AuthorID`       |

A block's **content** is everything between its `::infrapad_block`
directive and the next `::infrapad_block` directive (or end of file).

### Block content serialization

By default, block content is serialized as a **YAML code fence**
(` ```yaml … ``` `). The YAML body follows the schema defined by the
block's type. This keeps structured data unambiguous while the
surrounding markdown stays renderable in any viewer. See the
`alerts_matcher` block in the example document (block 1).

The `markdown` block type is an exception: its content region is
**literal markdown** — no wrapper, no code fence. Standard markdown
constructs (headings, lists, code blocks, inline formatting) are used
as-is. See the example document's blocks 2 and 3.

## Parsing rules

1. Parse YAML frontmatter delimited by `---`.
2. Split the remaining body on `::infrapad_block{…}` directives.
   Text before the first directive is an error (or ignored).
3. For each directive, extract attributes from `{…}`.
4. Collect everything after the directive line until the next
   `::infrapad_block` or EOF — this is the block content.
5. Deserialize the content according to `type`:
   - `markdown` → use the raw text (trimmed of leading/trailing blank
     lines).
   - `alerts_matcher` (and other structured types) → extract the YAML
     code fence body and unmarshal it.

## Serialization rules

1. Emit YAML frontmatter with doc-level fields.
2. For each block in order:
   a. Emit `::infrapad_block{type=… block=… rev=… author=…}`.
   b. Emit the content:
      - `markdown` → write the text verbatim.
      - structured types → wrap the YAML serialization in a
        ` ```yaml … ``` ` fence.

## Design notes

**Why a single `::infrapad_block` directive name?**  The block type is
data, not syntax. Using one directive name with a `type` attribute
keeps the parser simple — it only needs to recognize one pattern — and
makes adding new block types a data-layer concern rather than a
parser change.

**Why YAML code fences for structured content?**  A fenced code block
is the natural markdown container for "non-markdown data". It renders
readably in any markdown viewer and clearly separates structured
content from surrounding prose. YAML was chosen over JSON for
readability.
