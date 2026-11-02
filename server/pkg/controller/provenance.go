package controller

// Provenance: the internal host path for generated content.
//
// The helpers in this file are the server-side counterpart to the public
// block API. They have no public API surface by design. Their caller is
// the host orchestration behind payload submission: it dispatches to the
// registered Expander, validates the resulting records, and then persists
// the desired output (adding, revising, deleting, and recursively expanding
// generated blocks) through these helpers in one atomic operation.
//
// Note the content types themselves never call these: an Expander only
// computes the desired output records; the host persists the result.
//
// That orchestration must run inside one transaction that holds the
// document mutation lock, which is what BeginHostTx provides; the *Tx
// helpers take the already-open store.Tx so callers can compose several of
// them without nested transactions.

import (
	"context"
	"fmt"
	"time"

	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store"
)

// BeginHostTx starts a read-write transaction and acquires the document
// mutation lock. Host-side generated-content management (child creation,
// cascading deletion, restoration, snapshot reconciliation) must run inside
// such a transaction: the lock serializes concurrent block mutations within
// the document, preventing e.g. a live child from being committed beneath a
// concurrently deleted parent. The returned Tx is passed to the
// transaction-scoped host helpers below; the caller owns Commit/Rollback.
func (c *Controller) BeginHostTx(ctx context.Context, documentUID model.DocumentUID) (store.Tx, error) {
	tx, err := c.store.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin host tx: %w", err)
	}
	if err := tx.Documents().Lock(ctx, documentUID); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("lock document: %w", err)
	}
	return tx, nil
}

// CreateChildBlockTx creates a generated child block under
// parentBlockNumber in the same document and transaction. It is the only
// path that assigns provenance; public block creation always rejects it.
// The parent must already exist and be live (non-deleted) in the same
// document, and the ancestry must be well-formed (no self-reference, no
// cycles, no missing intermediate parents); otherwise the operation fails
// as invalid input and the transaction is left untouched for rollback.
// Callers create nested trees parent-first in one transaction.
func (c *Controller) CreateChildBlockTx(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, parentBlockNumber model.BlockNumber, blk model.Block) (model.Block, error) {
	if parentBlockNumber <= 0 {
		return model.Block{}, invalidInput("child block parent %d: must be positive", parentBlockNumber)
	}
	if blk.BlockNumber != 0 {
		return model.Block{}, invalidInput("child block %d: block numbers are assigned by the host", blk.BlockNumber)
	}

	latest, err := c.latestBlocks(ctx, tx, documentUID)
	if err != nil {
		return model.Block{}, err
	}
	if err := validateParentAncestry(latest, parentBlockNumber); err != nil {
		return model.Block{}, err
	}

	parent := model.BlockNumber(parentBlockNumber)
	blk.ParentBlockNumber = &parent
	if err := c.prepareNew(&blk, time.Now().UTC()); err != nil {
		return model.Block{}, err
	}
	created, err := tx.Blocks().Create(ctx, documentUID, blk)
	if err != nil {
		return model.Block{}, fmt.Errorf("create child block: %w", err)
	}
	created.Record = blk.Record
	return created, nil
}

// DeleteBlockTreeTx appends a `deleted` revision for the target block and
// each of its live descendants at every depth, atomically within the given
// transaction. Deletion is append-only: content and immutable parent
// relationships are preserved on the deletion revisions and earlier
// revisions remain available through history. No redundant deletion
// revisions are appended for descendants that are already deleted. It
// returns all appended deletion revisions.
func (c *Controller) DeleteBlockTreeTx(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, blockNumber model.BlockNumber, author model.AuthorID) ([]model.Block, error) {
	latest, err := c.latestBlocks(ctx, tx, documentUID)
	if err != nil {
		return nil, err
	}
	target, ok := latest[blockNumber]
	if !ok {
		return nil, fmt.Errorf("delete block %d: %w", blockNumber, errNotFound)
	}
	if target.Status == model.DeletedBlock {
		return nil, invalidInput("block %d: already deleted", blockNumber)
	}

	targets := append([]model.Block{target}, liveDescendants(latest, blockNumber)...)
	deleted := make([]model.Block, 0, len(targets))
	for _, blk := range targets {
		rev, err := appendRevision(ctx, tx, documentUID, blk, model.DeletedBlock, author)
		if err != nil {
			return nil, err
		}
		deleted = append(deleted, rev)
	}
	return deleted, nil
}

// RestoreBlockTx restores a deleted block by appending a new revision with
// its previous (pre-deletion) status. Only the block itself is restored:
// its descendants are not resurrected and remain deleted until a later
// expansion/reconciliation decides otherwise. Reviving a child requires a
// live parent: restoring beneath a deleted parent is rejected as invalid
// input.
func (c *Controller) RestoreBlockTx(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, blockNumber model.BlockNumber, author model.AuthorID) (model.Block, error) {
	latest, err := tx.Blocks().Get(ctx, documentUID, blockNumber, 0)
	if err != nil {
		return model.Block{}, fmt.Errorf("get latest block for restore: %w", err)
	}
	if latest.Status != model.DeletedBlock {
		return model.Block{}, invalidInput("block %d: not deleted", blockNumber)
	}

	if parent := latest.ParentBlockNumber; parent != nil {
		parentLatest, err := tx.Blocks().Get(ctx, documentUID, *parent, 0)
		if err != nil {
			return model.Block{}, invalidInput("block %d: parent block %d not found", blockNumber, *parent)
		}
		if parentLatest.Status == model.DeletedBlock {
			return model.Block{}, invalidInput("block %d: parent block %d is deleted", blockNumber, *parent)
		}
	}

	// Restore to the status the block had before it was deleted.
	var status model.BlockStatus = model.PublishedBlock
	revisions, err := tx.Blocks().ListRevisions(ctx, documentUID, blockNumber)
	if err != nil {
		return model.Block{}, fmt.Errorf("list revisions for restore: %w", err)
	}
	for i := len(revisions) - 1; i >= 0; i-- {
		if revisions[i].Status != model.DeletedBlock {
			status = revisions[i].Status
			break
		}
	}

	return appendRevision(ctx, tx, documentUID, latest, status, author)
}

// ---------------------------------------------------------------------------
// Host-path helpers
// ---------------------------------------------------------------------------

// latestBlocks loads the latest revision of every block in the document,
// keyed by block number.
func (c *Controller) latestBlocks(ctx context.Context, tx store.Tx, documentUID model.DocumentUID) (map[model.BlockNumber]model.Block, error) {
	blocks, err := tx.Blocks().ListLatest(ctx, documentUID)
	if err != nil {
		return nil, fmt.Errorf("list latest blocks: %w", err)
	}
	latest := make(map[model.BlockNumber]model.Block, len(blocks))
	for _, blk := range blocks {
		latest[blk.BlockNumber] = blk
	}
	return latest, nil
}

// validateParentAncestry checks that parentBlockNumber refers to a live
// block in the same document with a well-formed ancestry chain: no missing
// parents and no cycles. Parent-first creation plus immutable provenance
// prevents valid operations from introducing cycles; this explicit check
// guards against malformed relationships.
func validateParentAncestry(latest map[model.BlockNumber]model.Block, parentBlockNumber model.BlockNumber) error {
	parent, ok := latest[parentBlockNumber]
	if !ok {
		return invalidInput("parent block %d: not found in document", parentBlockNumber)
	}
	if parent.Status == model.DeletedBlock {
		return invalidInput("parent block %d: is deleted", parentBlockNumber)
	}

	seen := map[model.BlockNumber]bool{parentBlockNumber: true}
	for {
		p := parent.ParentBlockNumber
		if p == nil {
			return nil
		}
		if seen[*p] {
			return invalidInput("parent block %d: ancestry cycle at block %d", parentBlockNumber, *p)
		}
		seen[*p] = true
		parent, ok = latest[*p]
		if !ok {
			return invalidInput("parent block %d: ancestor block %d not found in document", parentBlockNumber, *p)
		}
	}
}

// liveDescendants returns the live (non-deleted) descendants of blockNumber
// at every depth, in breadth-first order. Already deleted descendants are
// skipped.
func liveDescendants(latest map[model.BlockNumber]model.Block, blockNumber model.BlockNumber) []model.Block {
	children := map[model.BlockNumber][]model.Block{}
	for _, blk := range latest {
		if blk.ParentBlockNumber != nil {
			children[*blk.ParentBlockNumber] = append(children[*blk.ParentBlockNumber], blk)
		}
	}

	var out []model.Block
	queue := []model.Block{latest[blockNumber]}
	for len(queue) > 0 {
		blk := queue[0]
		queue = queue[1:]
		for _, child := range children[blk.BlockNumber] {
			if child.Status == model.DeletedBlock {
				continue
			}
			out = append(out, child)
			queue = append(queue, child)
		}
	}
	return out
}

// errNotFound marks a missing block/document in host operations. It is
// deliberately not mapped to invalid input: not-found is not a property of
// the submitted data.
type notFoundError struct{}

func (notFoundError) Error() string { return "not found" }

var errNotFound = notFoundError{}
