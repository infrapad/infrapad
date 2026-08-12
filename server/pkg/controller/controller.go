package controller

import (
	"context"
	"fmt"

	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store"
)

// Controller provides the business-logic layer on top of the store.
// It orchestrates operations that span documents and blocks while
// keeping the store focused on persistence only.
type Controller struct {
	store store.Store
}

// New creates a Controller backed by the given store.
func New(s store.Store) *Controller {
	return &Controller{store: s}
}

// ---------------------------------------------------------------------------
// Document operations
// ---------------------------------------------------------------------------

// CreateDoc creates a new document. If doc.Blocks is non-empty the blocks are
// inserted as well, each getting BlockNumber assigned sequentially starting at 1.
func (c *Controller) CreateDoc(ctx context.Context, doc model.Doc) (model.Doc, error) {
	tx, err := c.store.Begin(ctx)
	if err != nil {
		return model.Doc{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	created, err := tx.Docs().Create(ctx, doc)
	if err != nil {
		return model.Doc{}, fmt.Errorf("create doc: %w", err)
	}

	for i, blk := range doc.Blocks {
		blk.BlockNumber = model.BlockNumber(i + 1)
		blk.RevisionNumber = 1
		b, err := tx.Blocks().Create(ctx, created.Uid, blk)
		if err != nil {
			return model.Doc{}, fmt.Errorf("create initial block %d: %w", i+1, err)
		}
		created.Blocks = append(created.Blocks, b)
	}

	if err := tx.Commit(); err != nil {
		return model.Doc{}, fmt.Errorf("commit tx: %w", err)
	}
	return created, nil
}

// GetDoc returns a document together with the latest revision of each of its
// blocks.
func (c *Controller) GetDoc(ctx context.Context, uid model.DocUID) (model.Doc, error) {
	tx, err := c.store.BeginReadOnly(ctx)
	if err != nil {
		return model.Doc{}, fmt.Errorf("begin read-only tx: %w", err)
	}
	defer tx.Rollback()

	doc, err := tx.Docs().Get(ctx, uid)
	if err != nil {
		return model.Doc{}, fmt.Errorf("get doc: %w", err)
	}

	blocks, err := tx.Blocks().ListLatest(ctx, uid)
	if err != nil {
		return model.Doc{}, fmt.Errorf("list latest blocks: %w", err)
	}
	doc.Blocks = blocks

	if err := tx.Commit(); err != nil {
		return model.Doc{}, fmt.Errorf("commit tx: %w", err)
	}
	return doc, nil
}

// ListDocs returns all documents (without blocks).
func (c *Controller) ListDocs(ctx context.Context) ([]model.Doc, error) {
	tx, err := c.store.BeginReadOnly(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin read-only tx: %w", err)
	}
	defer tx.Rollback()

	docs, err := tx.Docs().List(ctx)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return docs, nil
}

// ---------------------------------------------------------------------------
// Block operations
// ---------------------------------------------------------------------------

// AddBlock appends a new block to the document. The BlockNumber is assigned
// automatically by the store (next sequential number for the doc).
func (c *Controller) AddBlock(ctx context.Context, docUID model.DocUID, blk model.Block) (model.Block, error) {
	tx, err := c.store.Begin(ctx)
	if err != nil {
		return model.Block{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Verify the document exists.
	if _, err := tx.Docs().Get(ctx, docUID); err != nil {
		return model.Block{}, fmt.Errorf("get doc for add block: %w", err)
	}

	created, err := tx.Blocks().Create(ctx, docUID, blk)
	if err != nil {
		return model.Block{}, fmt.Errorf("create block: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return model.Block{}, fmt.Errorf("commit tx: %w", err)
	}
	return created, nil
}

// UpdateBlock creates a new revision for an existing block. The new revision
// keeps the same BlockNumber but gets an incremented RevisionNumber.
func (c *Controller) UpdateBlock(ctx context.Context, docUID model.DocUID, blockNumber model.BlockNumber, blk model.Block) (model.Block, error) {
	tx, err := c.store.Begin(ctx)
	if err != nil {
		return model.Block{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Make sure the block exists before we create a new revision.
	_, err = tx.Blocks().Get(ctx, docUID, blockNumber, 0)
	if err != nil {
		return model.Block{}, fmt.Errorf("get latest block for update: %w", err)
	}

	blk.BlockNumber = blockNumber
	created, err := tx.Blocks().Create(ctx, docUID, blk)
	if err != nil {
		return model.Block{}, fmt.Errorf("create block revision: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return model.Block{}, fmt.Errorf("commit tx: %w", err)
	}
	return created, nil
}

// GetBlock returns a specific revision of a block. If revisionNumber is 0 the
// latest revision is returned.
func (c *Controller) GetBlock(ctx context.Context, docUID model.DocUID, blockNumber model.BlockNumber, revisionNumber model.RevisionNumber) (model.Block, error) {
	tx, err := c.store.BeginReadOnly(ctx)
	if err != nil {
		return model.Block{}, fmt.Errorf("begin read-only tx: %w", err)
	}
	defer tx.Rollback()

	blk, err := tx.Blocks().Get(ctx, docUID, blockNumber, revisionNumber)
	if err != nil {
		return model.Block{}, err
	}

	if err := tx.Commit(); err != nil {
		return model.Block{}, fmt.Errorf("commit tx: %w", err)
	}
	return blk, nil
}

// ListBlocks returns the latest revision of every block in the document.
func (c *Controller) ListBlocks(ctx context.Context, docUID model.DocUID) ([]model.Block, error) {
	tx, err := c.store.BeginReadOnly(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin read-only tx: %w", err)
	}
	defer tx.Rollback()

	blocks, err := tx.Blocks().ListLatest(ctx, docUID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return blocks, nil
}

// ListBlockHistory returns all revisions of a given block.
func (c *Controller) ListBlockHistory(ctx context.Context, docUID model.DocUID, blockNumber model.BlockNumber) ([]model.Block, error) {
	tx, err := c.store.BeginReadOnly(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin read-only tx: %w", err)
	}
	defer tx.Rollback()

	revisions, err := tx.Blocks().ListRevisions(ctx, docUID, blockNumber)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return revisions, nil
}
