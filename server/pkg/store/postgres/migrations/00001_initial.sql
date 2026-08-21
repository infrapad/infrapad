-- +goose Up

CREATE TABLE documents (
    uid                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status              TEXT NOT NULL DEFAULT 'active',
    title               TEXT NOT NULL,
    namespace           TEXT NOT NULL DEFAULT 'default',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE blocks (
    document_uid        UUID NOT NULL REFERENCES documents(uid),
    block_number        INT NOT NULL,
    revision_number     INT NOT NULL,
    author_id           TEXT NOT NULL DEFAULT '',
    type                TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'published',
    serialized_content  JSONB NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (document_uid, block_number, revision_number)
);

-- +goose Down

DROP TABLE blocks;
DROP TABLE documents;
