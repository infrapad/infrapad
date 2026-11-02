package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store"
)

// Controller provides the business-logic layer on top of the store.
// It orchestrates operations that span documents and blocks while
// keeping the store focused on persistence only.
//
// The controller owns record validation, serialization, and
// deserialization through the injected content registry. Public API
// submissions carry only the inner content value; the controller builds
// the host record around it, initializing the record timestamp from
// block creation time and preserving it across revisions.
type Controller struct {
	store    store.Store
	registry *content.Registry
}

// New creates a Controller backed by the given store and content registry.
func New(s store.Store, registry *content.Registry) *Controller {
	return &Controller{store: s, registry: registry}
}

// IsInvalidInput reports whether err was caused by invalid submitted
// content or metadata (unknown content type, a schema/record validation
// failure, or invalid provenance input) rather than an unexpected server
// failure. Transports use it to map such errors to explicit
// invalid-argument responses.
func IsInvalidInput(err error) bool {
	var validation *content.ValidationError
	if errors.As(err, &validation) {
		return true
	}
	var invalid *InvalidInputError
	if errors.As(err, &invalid) {
		return true
	}
	return errors.Is(err, content.ErrUnknownContentType)
}

// InvalidInputError marks a rejection caused by invalid submitted input
// (e.g. illegal provenance changes) as opposed to an unexpected server
// failure. Transports map it to invalid-argument responses through
// IsInvalidInput.
type InvalidInputError struct {
	Err error
}

func (e *InvalidInputError) Error() string { return e.Err.Error() }
func (e *InvalidInputError) Unwrap() error { return e.Err }

func invalidInput(format string, args ...any) error {
	return &InvalidInputError{Err: fmt.Errorf(format, args...)}
}

// ---------------------------------------------------------------------------
// Record handling
// ---------------------------------------------------------------------------

// buildRecord validates the submitted inner content JSON against the
// registered content type and wraps it in a new host record stamped with
// ts (block creation time for ordinary blocks).
func (c *Controller) buildRecord(blockType string, inner json.RawMessage, ts time.Time) (content.Record, error) {
	value, err := c.registry.DecodeContent(blockType, inner)
	if err != nil {
		return nil, err
	}
	ct, err := c.registry.Lookup(blockType)
	if err != nil {
		return nil, err
	}
	record := content.NewItem(ct, ts, value)
	if err := c.registry.ValidateRecord(record); err != nil {
		return nil, err
	}
	return record, nil
}

// updatedRecord validates the submitted inner content JSON and replaces
// the inner value of existing while preserving its type association,
// timestamp, and key.
func (c *Controller) updatedRecord(existing content.Record, blockType string, inner json.RawMessage) (content.Record, error) {
	value, err := c.registry.DecodeContent(blockType, inner)
	if err != nil {
		return nil, err
	}
	updated, err := content.WithContent(existing, value)
	if err != nil {
		return nil, err
	}
	if err := c.registry.ValidateRecord(updated); err != nil {
		return nil, err
	}
	return updated, nil
}

// serializeRecord stores the full host record envelope on blk.
func serializeRecord(blk *model.Block) error {
	if blk.Record == nil {
		return fmt.Errorf("block %d: no record to serialize", blk.BlockNumber)
	}
	data, err := content.EncodeRecord(blk.Record)
	if err != nil {
		return fmt.Errorf("block %d: encode record: %w", blk.BlockNumber, err)
	}
	blk.SerializedRecord = model.SerializedRecord{
		Type: blk.Record.ContentType().Name(),
		Data: data,
	}
	return nil
}

// deserializeRecord restores the host record of blk from its serialized
// envelope, validating the stored content against the registered schema.
func (c *Controller) deserializeRecord(blk *model.Block) error {
	record, err := c.registry.DecodeRecord(blk.Type, blk.SerializedRecord.Data)
	if err != nil {
		return fmt.Errorf("block %d: decode record: %w", blk.BlockNumber, err)
	}
	blk.Record = record
	return nil
}

// prepareNew validates blk.Content and builds a new record from it,
// initializing the record timestamp from block creation time.
func (c *Controller) prepareNew(blk *model.Block, ts time.Time) error {
	record, err := c.buildRecord(blk.Type, blk.Content, ts)
	if err != nil {
		return err
	}
	blk.Record = record
	return serializeRecord(blk)
}

// prepareUpdate validates blk.Content against the existing record,
// preserving the existing record timestamp (and key).
func (c *Controller) prepareUpdate(blk *model.Block, existing content.Record) error {
	if blk.Type != existing.ContentType().Name() {
		return &content.ValidationError{
			ContentType: blk.Type,
			Stage:       content.StageRecord,
			Err:         fmt.Errorf("block type %q cannot be changed to %q", existing.ContentType().Name(), blk.Type),
		}
	}
	record, err := c.updatedRecord(existing, blk.Type, blk.Content)
	if err != nil {
		return err
	}
	blk.Record = record
	return serializeRecord(blk)
}

// ---------------------------------------------------------------------------
// Document operations
// ---------------------------------------------------------------------------

// CreateDocument creates a new document. If doc.Blocks is non-empty the blocks are
// inserted as well, each getting BlockNumber assigned sequentially starting at 1.
func (c *Controller) CreateDocument(ctx context.Context, doc model.Document) (model.Document, error) {
	for i := range doc.Blocks {
		if err := rejectPublicParent(&doc.Blocks[i]); err != nil {
			return model.Document{}, fmt.Errorf("initial block %d: %w", i+1, err)
		}
	}

	tx, err := c.store.Begin(ctx)
	if err != nil {
		return model.Document{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	created, err := tx.Documents().Create(ctx, doc)
	if err != nil {
		return model.Document{}, fmt.Errorf("create document: %w", err)
	}

	now := time.Now().UTC()
	for i, blk := range doc.Blocks {
		blk.BlockNumber = model.BlockNumber(i + 1)
		blk.RevisionNumber = 1
		if err := c.prepareNew(&blk, now); err != nil {
			return model.Document{}, fmt.Errorf("initial block %d: %w", i+1, err)
		}
		b, err := tx.Blocks().Create(ctx, created.Uid, blk)
		if err != nil {
			return model.Document{}, fmt.Errorf("create initial block %d: %w", i+1, err)
		}
		b.Record = blk.Record
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
	for i := range blocks {
		if err := c.deserializeRecord(&blocks[i]); err != nil {
			return model.Document{}, err
		}
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
// automatically by the store (next sequential number for the doc). The
// submitted blk.Content is validated against the registered content type
// and the record timestamp is initialized from block creation time.
func (c *Controller) AddBlock(ctx context.Context, documentUID model.DocumentUID, blk model.Block) (model.Block, error) {
	if err := rejectPublicParent(&blk); err != nil {
		return model.Block{}, err
	}

	tx, err := c.store.Begin(ctx)
	if err != nil {
		return model.Block{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Verify the document exists and lock it against concurrent mutations.
	if err := tx.Documents().Lock(ctx, documentUID); err != nil {
		return model.Block{}, fmt.Errorf("lock document for add block: %w", err)
	}

	if err := c.prepareNew(&blk, time.Now().UTC()); err != nil {
		return model.Block{}, err
	}

	created, err := tx.Blocks().Create(ctx, documentUID, blk)
	if err != nil {
		return model.Block{}, fmt.Errorf("create block: %w", err)
	}
	created.Record = blk.Record

	if err := tx.Commit(); err != nil {
		return model.Block{}, fmt.Errorf("commit tx: %w", err)
	}
	return created, nil
}

// UpdateBlock creates a new revision for an existing block. The new revision
// keeps the same BlockNumber but gets an incremented RevisionNumber. The
// submitted blk.Content is validated against the registered content type;
// the existing record timestamp is preserved.
func (c *Controller) UpdateBlock(ctx context.Context, documentUID model.DocumentUID, blockNumber model.BlockNumber, blk model.Block) (model.Block, error) {
	tx, err := c.store.Begin(ctx)
	if err != nil {
		return model.Block{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Serialize block mutations within the document.
	if err := tx.Documents().Lock(ctx, documentUID); err != nil {
		return model.Block{}, fmt.Errorf("lock document for update block: %w", err)
	}

	// Make sure the block exists before we create a new revision.
	latest, err := tx.Blocks().Get(ctx, documentUID, blockNumber, 0)
	if err != nil {
		return model.Block{}, fmt.Errorf("get latest block for update: %w", err)
	}
	if err := c.deserializeRecord(&latest); err != nil {
		return model.Block{}, err
	}

	if err := checkParentEcho(blk.ParentBlockNumber, latest.ParentBlockNumber); err != nil {
		return model.Block{}, err
	}
	// Preserve the stored provenance on the new revision.
	blk.ParentBlockNumber = latest.ParentBlockNumber

	if err := c.prepareUpdate(&blk, latest.Record); err != nil {
		return model.Block{}, err
	}

	blk.BlockNumber = blockNumber
	created, err := tx.Blocks().Create(ctx, documentUID, blk)
	if err != nil {
		return model.Block{}, fmt.Errorf("create block revision: %w", err)
	}
	created.Record = blk.Record

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
	if err := c.deserializeRecord(&blk); err != nil {
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
	for i := range blocks {
		if err := c.deserializeRecord(&blocks[i]); err != nil {
			return nil, err
		}
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
	for i := range revisions {
		if err := c.deserializeRecord(&revisions[i]); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return revisions, nil
}

// ---------------------------------------------------------------------------
// Provenance boundary checks and revision helpers
// ---------------------------------------------------------------------------
// The host-side provenance path lives in provenance.go; these helpers
// enforce the same contract on the public write boundary and provide the
// shared revision-append primitive.

// rejectPublicParent rejects any provenance supplied through public block
// creation input: provenance is assigned only by the host when it creates
// a generated child.
func rejectPublicParent(blk *model.Block) error {
	if blk.ParentBlockNumber == nil {
		return nil
	}
	return invalidInput("block parent %d: provenance cannot be assigned through public input", *blk.ParentBlockNumber)
}

// checkParentEcho accepts an omitted or exactly echoed parent block number
// on public updates. Omission preserves provenance; changing or clearing
// it is rejected.
func checkParentEcho(submitted, stored *model.BlockNumber) error {
	if submitted == nil || stored != nil && *submitted == *stored {
		return nil
	}
	if stored == nil {
		return invalidInput("block has no parent: provenance cannot be assigned through public input")
	}
	return invalidInput("block parent %d: provenance cannot be changed to %d", *stored, *submitted)
}

// appendRevision appends a new revision of blk with the given status and
// author, preserving the block type, serialized record envelope, and
// immutable parent relationship.
func appendRevision(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, blk model.Block, status model.BlockStatus, author model.AuthorID) (model.Block, error) {
	blk.Status = status
	blk.AuthorID = author
	rev, err := tx.Blocks().Create(ctx, documentUID, blk)
	if err != nil {
		return model.Block{}, fmt.Errorf("append revision for block %d: %w", blk.BlockNumber, err)
	}
	return rev, nil
}
