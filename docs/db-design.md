# Database Design

## Overview

The database uses PostgreSQL with a **single-table inheritance (STI)** approach
for blocks. All block types and their versions are stored in one `blocks` table
with the serialized record in a JSONB `serialized_content` column.

The store is **content-type agnostic**: it persists serialized bytes and
ordinary block metadata only. It holds no extension implementations and no
content registry; validation, serialization, and deserialization of the host
records are owned by the controller through the injected content registry
(see `server/pkg/controller` and `server/pkg/content`).

The `type` column carries the canonical namespaced content type name
(e.g. `Core/Markdown`), and `serialized_content` holds the **full host record
envelope** (event timestamp, optional generated key, and the inner extension
content). The public API exposes this envelope verbatim as `Block.record`;
ordinary writes may only supply its inner `content` (plus an optional
unchanged `timestamp` echo on updates).

## Schema

### documents

Top-level entity representing an incident, observation, or analysis.

```sql
CREATE TABLE documents (
    uid                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status              TEXT NOT NULL DEFAULT 'active',
    title               TEXT NOT NULL,
    namespace           TEXT NOT NULL DEFAULT 'default',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
```


### blocks

All block types and all versions are stored in this single table.
The current version of a block is determined by the highest `revision_number`.

```sql
CREATE TABLE blocks (
    document_uid      UUID NOT NULL REFERENCES documents(uid),
    block_number      INT NOT NULL,
    revision_number   INT NOT NULL,
    author_id         TEXT NOT NULL DEFAULT '',
    type              TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'published',
    serialized_content JSONB NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    parent_block_number INT NULL,

    PRIMARY KEY (document_uid, block_number, revision_number)
);
```

`parent_block_number` carries block **provenance**: the immediate generating
block in the same document, or `NULL` for root blocks. It is host-owned
immutable metadata, kept outside `serialized_content`. It is assigned only
when the server creates a generated child (through the transaction-scoped
controller host helpers) and is copied unchanged onto every later revision,
including deletion and restoration revisions. There is no database-level
foreign key: same-document references and parent existence are enforced by
the controller under the document mutation lock (see
[Provenance lifecycle](#provenance-lifecycle)). Existing blocks from before
the column was added remain roots (`NULL`).

Both `block_number` and `revision_number` are scoped auto-increments
starting from 1 for each document (and each block respectively). They are
assigned via subqueries at insert time (see [Inserting](#inserting) below).

## Indexes

The composite PK `(document_uid, block_number, revision_number)` covers
the two primary access patterns with no additional indexes needed:

- **Fetch all blocks for a document** — prefix scan on `document_uid`
- **Fetch history of a block** — prefix scan on `(document_uid, block_number)`

All blocks of the same document are physically adjacent in the B-tree index,
making document fetches very efficient.

## Versioning

Versioning is per-block, append-only. Each edit creates a new row with
an incremented `revision_number`. No rows are ever updated or deleted
during normal operation.

The important invariant: the current version of the block always has
the highest revision number. This allows for efficient queries for current
blocks.

### Fetching Current State of a Document

```sql
SELECT DISTINCT ON (block_number) *
FROM blocks
WHERE document_uid = $1
ORDER BY block_number, revision_number DESC;
```

### Inserting a New Block {#inserting}

Creating a new block uses a subquery to find the next available `block_number`
for the document.

```sql
INSERT INTO blocks (document_uid, block_number, revision_number, author_id, type, status, serialized_content)
VALUES (
    $1,
    (SELECT COALESCE(MAX(block_number), 0) + 1
       FROM blocks
      WHERE document_uid = $1),
    1,
    $2, $3, $4, $5
);
```

### Inserting a New Revision of an Existing Block

```sql
INSERT INTO blocks (document_uid, block_number, revision_number, author_id, type, status, serialized_content)
VALUES (
    $1,
    $2,
    (SELECT MAX(revision_number) + 1
       FROM blocks
      WHERE document_uid = $1 AND block_number = $2),
    $3, $4, $5, $6
);
```

The PK constraint on `(document_uid, block_number, revision_number)` prevents
duplicate revisions in case of concurrent edits — the application can retry
on conflict.

## Event timestamps

Every host record carries an **event timestamp** inside the
`serialized_content` envelope, next to the optional generated key. It is
the time the underlying event occurred and is distinct from the row's
`created_at`, which is the creation time of that particular **revision**:
an edited block gets a new `created_at` on every revision while its event
timestamp is preserved.

- **Ordinary blocks** get their event timestamp initialized from block
  creation time; it never changes across public edits.
- **Generated blocks** keep the event timestamps supplied internally by
  their extension; host-managed revisions may change them when the source
  event times change.
- **Collections** expose their first member's event timestamp; there is no
  separate group timestamp.

The event timestamp is read-only through the public API: creations must
omit it, updates may omit it or exactly echo the stored value, and
assignments or changes are rejected as invalid input. The public API
exposes it only as `timestamp` inside the verbatim `Block.record`
envelope (collections: inside each member); there is no second
authoritative event-time value and no extra column or migration.

Latest-block document views are ordered by event timestamp ascending,
then block number ascending, in the controller after record decoding —
the store stays content-type agnostic and never interprets record
envelopes for ordering. Revision history keeps its revision-number order.

## Provenance lifecycle

Provenance relationships form a tree within a document: each block has at
most one immediate parent, and references never cross documents.

- **Assignment**: only the host assigns provenance, when it creates a
  generated child through `Controller.CreateChildBlockTx`. The parent must
  already exist and be live (non-deleted) in the same document and
  transaction, and nested trees are created parent-first. Public block
  creation rejects any supplied parent; public updates accept only an exact
  echo of the stored parent.
- **Immutability**: provenance is preserved unchanged across every
  revision. Missing parents, self-references, and cycles are rejected as
  invalid input before anything is written.
- **Deletion**: deleting a block appends `deleted` revisions for the target
  and each live descendant at every depth, atomically. Content and parent
  relationships are preserved on the deletion revisions; no rows are
  physically removed and no redundant deletion revisions are appended for
  already-deleted descendants.
- **Restoration**: restoring a deleted block restores only that block, back
  to its pre-deletion status. Descendants stay deleted until a later
  expansion/reconciliation decides otherwise, and reviving a child requires
  a live parent.

## Generated-block identities

Generated (extension-emitted) constituents carry a stable identity inside
the existing `serialized_content` envelope: a `key` field on standalone
records and per-member `key` fields inside collection envelopes, next to
the member event timestamps. There are no extra columns, tables, or
migrations for identities; the store stays content-type agnostic and the
controller owns the record codecs.

**Identity scope**: a generated constituent's identity is its document,
its immediate generating parent block (`parent_block_number`), and its
opaque non-empty key. Keys are unique across the parent's complete live
direct output — different content types and separate groups included —
so different parents (including at different nesting levels) may reuse
the same key. Matching additionally requires the same content type: a
key changing content type does not retain its old block number; a later
reconciliation retires the old block and allocates new output.

**Writes**: generated children are created and revised only through the
transaction-scoped host helpers, which take validated full
`content.Record` values rather than inner-content submissions:

- `Controller.CreateChildBlockTx` retains the supplied record as-is, so
  generated keys, complete memberships, member order, event timestamps,
  and inner values survive creation.
- `Controller.AppendGeneratedRevisionTx` appends a revision of a matched
  live generated block, preserving the immutable parent and content type
  and the key of a standalone reference, while collection membership (and
  the derived first-member anchor) may change freely with the block
  number staying stable.

Both reject, as invalid input and without writing: missing or malformed
keys, keys colliding with the parent's live direct output (deleted blocks
are not part of it, so a returning key gets a new block), content-type
changes, and standalone key changes. Ordinary public writes keep
submitting inner content only; producers never assign generated keys
through them. Since collisions are checked against the *live* output, a
key changing content type must retire the old block before the new
output is created within the same reconciliation transaction.

**Membership matching** (`controller.MatchGeneratedOutput`) is a pure
helper over previous latest blocks and already-formed desired records of
one immediate parent; it performs no writes and allocates no block
numbers. For each live same-parent block, the first member in persisted
order whose key survives in desired output of the same content type
nominates that desired record; a standalone keyed reference counts as a
one-member record. When several old blocks nominate the same desired
record, the lower existing block number wins and losers do not fall back
to another desired record. Deleted blocks are never candidates. The
matcher returns matches, unmatched desired records, and unmatched old
blocks; applying them (revisions, fresh block allocation, retirement of
unmatched output) is owned by the later snapshot reconciliation.

## Document mutation locking

All block mutations within a document — public block add/update as well as
internal host operations (child creation, cascading deletion, restoration,
and later snapshot reconciliation) — run inside a transaction that first
takes an exclusive row lock on the `documents` row:

```sql
SELECT uid FROM documents WHERE uid = $1 FOR UPDATE;
```

The lock is acquired before any block validation or write, so concurrent
operations on the same document are serialized. In particular, concurrent
child creation and parent deletion can never commit a live child beneath a
deleted parent. Transactions that only read blocks do not take the lock.
