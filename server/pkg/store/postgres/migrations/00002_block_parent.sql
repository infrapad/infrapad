-- +goose Up
-- Block provenance: the immediate generating block in the same document.
-- NULL means a root block. Existing blocks remain roots.
-- No FK constraint: same-document references and parent existence are
-- enforced by the controller under the document mutation lock.
ALTER TABLE blocks ADD COLUMN parent_block_number INT;

-- +goose Down
ALTER TABLE blocks DROP COLUMN parent_block_number;
