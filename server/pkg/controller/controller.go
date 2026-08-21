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

// CreateDocument creates a new document. If doc.Blocks is non-empty the blocks are
// inserted as well, each getting BlockNumber assigned sequentially starting at 1.
func (c *Controller) CreateDocument(ctx context.Context, doc model.Document) (model.Document, error) {
	tx, err := c.store.Begin(ctx)
	if err != nil {
		return model.Document{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	created, err := tx.Documents().Create(ctx, doc)
	if err != nil {
		return model.Document{}, fmt.Errorf("create document: %w", err)
	}

	for i, blk := range doc.Blocks {
		blk.BlockNumber = model.BlockNumber(i + 1)
		blk.RevisionNumber = 1
		b, err := tx.Blocks().Create(ctx, created.Uid, blk)
		if err != nil {
			return model.Document{}, fmt.Errorf("create initial block %d: %w", i+1, err)
		}
		created.Blocks = append(created.Blocks, b)
	}

	if err := tx.Commit(); err != nil {
		return model.Document{}, fmt.Errorf("commit tx: %w", err)
	}
	return created, nil
}

// GetDocument returns a document together with the latest revision of each of its
// blocks.
func (c *Controller) GetDocument(ctx context.Context, uid model.DocumentUID) (model.Document, error) {
	tx, err := c.store.BeginReadOnly(ctx)
	if err != nil {
		return model.Document{}, fmt.Errorf("begin read-only tx: %w", err)
	}
	defer tx.Rollback()

	doc, err := tx.Documents().Get(ctx, uid)
	if err != nil {
		return model.Document{}, fmt.Errorf("get document: %w", err)
	}

	blocks, err := tx.Blocks().ListLatest(ctx, uid)
	if err != nil {
		return model.Document{}, fmt.Errorf("list latest blocks: %w", err)
	}
	doc.Blocks = blocks

	if err := tx.Commit(); err != nil {
		return model.Document{}, fmt.Errorf("commit tx: %w", err)
	}
	return doc, nil
}

// ListDocuments returns all documents (without blocks).
func (c *Controller) ListDocuments(ctx context.Context) ([]model.Document, error) {
	tx, err := c.store.BeginReadOnly(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin read-only tx: %w", err)
	}
	defer tx.Rollback()

	documents, err := tx.Documents().List(ctx)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return documents, nil
}

// ---------------------------------------------------------------------------
// Block operations
// ---------------------------------------------------------------------------

// AddBlock appends a new block to the document. The BlockNumber is assigned
// automatically by the store (next sequential number for the doc).
func (c *Controller) AddBlock(ctx context.Context, documentUID model.DocumentUID, blk model.Block) (model.Block, error) {
	tx, err := c.store.Begin(ctx)
	if err != nil {
		return model.Block{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Verify the document exists.
	if _, err := tx.Documents().Get(ctx, documentUID); err != nil {
		return model.Block{}, fmt.Errorf("get document for add block: %w", err)
	}

	created, err := tx.Blocks().Create(ctx, documentUID, blk)
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
func (c *Controller) UpdateBlock(ctx context.Context, documentUID model.DocumentUID, blockNumber model.BlockNumber, blk model.Block) (model.Block, error) {
	tx, err := c.store.Begin(ctx)
	if err != nil {
		return model.Block{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Make sure the block exists before we create a new revision.
	_, err = tx.Blocks().Get(ctx, documentUID, blockNumber, 0)
	if err != nil {
		return model.Block{}, fmt.Errorf("get latest block for update: %w", err)
	}

	blk.BlockNumber = blockNumber
	created, err := tx.Blocks().Create(ctx, documentUID, blk)
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
func (c *Controller) GetBlock(ctx context.Context, documentUID model.DocumentUID, blockNumber model.BlockNumber, revisionNumber model.RevisionNumber) (model.Block, error) {
	tx, err := c.store.BeginReadOnly(ctx)
	if err != nil {
		return model.Block{}, fmt.Errorf("begin read-only tx: %w", err)
	}
	defer tx.Rollback()

	blk, err := tx.Blocks().Get(ctx, documentUID, blockNumber, revisionNumber)
	if err != nil {
		return model.Block{}, err
	}

	if err := tx.Commit(); err != nil {
		return model.Block{}, fmt.Errorf("commit tx: %w", err)
	}
	return blk, nil
}

// ListBlocks returns the latest revision of every block in the document.
func (c *Controller) ListBlocks(ctx context.Context, documentUID model.DocumentUID) ([]model.Block, error) {
	tx, err := c.store.BeginReadOnly(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin read-only tx: %w", err)
	}
	defer tx.Rollback()

	blocks, err := tx.Blocks().ListLatest(ctx, documentUID)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return blocks, nil
}

// ListBlockHistory returns all revisions of a given block.
func (c *Controller) ListBlockHistory(ctx context.Context, documentUID model.DocumentUID, blockNumber model.BlockNumber) ([]model.Block, error) {
	tx, err := c.store.BeginReadOnly(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin read-only tx: %w", err)
	}
	defer tx.Rollback()

	revisions, err := tx.Blocks().ListRevisions(ctx, documentUID, blockNumber)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return revisions, nil
}
