# Database Design

## Overview

The database uses PostgreSQL with a **single-table inheritance (STI)** approach
for blocks. All block types and their versions are stored in one `blocks` table
with type-specific data in a JSONB `serialized_content` column.

## Schema

### docs

Top-level entity representing an incident, observation, or analysis.

```sql
CREATE TABLE docs (
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
    doc_uid           UUID NOT NULL REFERENCES docs(uid),
    block_number      INT NOT NULL,
    revision_number   INT NOT NULL,
    author_id         TEXT NOT NULL DEFAULT '',
    type              TEXT NOT NULL,
    status            TEXT NOT NULL DEFAULT 'published',
    serialized_content JSONB NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (doc_uid, block_number, revision_number)
);
```

Both `block_number` and `revision_number` are scoped auto-increments
starting from 1 for each doc (and each block respectively). They are
assigned via subqueries at insert time (see [Inserting](#inserting) below).

## Indexes

The composite PK `(doc_uid, block_number, revision_number)` covers
the two primary access patterns with no additional indexes needed:

- **Fetch all blocks for a doc** — prefix scan on `doc_uid`
- **Fetch history of a block** — prefix scan on `(doc_uid, block_number)`

All blocks of the same doc are physically adjacent in the B-tree index,
making doc fetches very efficient.

## Versioning

Versioning is per-block, append-only. Each edit creates a new row with
an incremented `revision_number`. No rows are ever updated or deleted
during normal operation.

The important invariant: the current version of the block always has
the highest revision number. This allows for efficient queries for current
blocks.

### Fetching Current State of a Doc

```sql
SELECT DISTINCT ON (block_number) *
FROM blocks
WHERE doc_uid = $1
ORDER BY block_number, revision_number DESC;
```

### Inserting a New Block {#inserting}

Creating a new block uses a subquery to find the next available `block_number`
for the doc.

```sql
INSERT INTO blocks (doc_uid, block_number, revision_number, author_id, type, status, serialized_content)
VALUES (
    $1,
    (SELECT COALESCE(MAX(block_number), 0) + 1
       FROM blocks
      WHERE doc_uid = $1),
    1,
    $2, $3, $4, $5
);
```

### Inserting a New Revision of an Existing Block

```sql
INSERT INTO blocks (doc_uid, block_number, revision_number, author_id, type, status, serialized_content)
VALUES (
    $1,
    $2,
    (SELECT MAX(revision_number) + 1
       FROM blocks
      WHERE doc_uid = $1 AND block_number = $2),
    $3, $4, $5, $6
);
```

The PK constraint on `(doc_uid, block_number, revision_number)` prevents
duplicate revisions in case of concurrent edits — the application can retry
on conflict.
