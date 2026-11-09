package store

import (
	"context"
	"errors"

	"github.com/infrapad/infrapad/server/pkg/model"
)

// ErrNotFound is wrapped by store errors for missing documents or blocks
// (Documents.Get/Lock, Blocks.Get), so callers can distinguish them from
// unexpected storage failures.
var ErrNotFound = errors.New("not found")

// Store provides transactional access to all data. Use
// [Store.Begin] to start a read-write transaction or
// [Store.BeginReadOnly] for a read-only transaction, then access
// store data through the returned [Tx].
type Store interface {
	// Begin starts a read-write transaction. On SQLite this issues
	// BEGIN IMMEDIATE so writers are serialized and read-lock-upgrade
	// deadlocks cannot occur.
	Begin(ctx context.Context) (Tx, error)

	// BeginReadOnly starts a read-only transaction. It uses a
	// deferred lock so it never contends with other readers or
	// writers. Use this for queries that do not mutate state.
	BeginReadOnly(ctx context.Context) (Tx, error)
}

type Tx interface {
	Documents() DocumentsCollection
	Blocks() BlocksCollection
	Commit() error
	Rollback() error
}

type DocumentsCollection interface {
	Create(ctx context.Context, doc model.Document) (model.Document, error)
	Get(ctx context.Context, uid model.DocumentUID) (model.Document, error)
	List(ctx context.Context) ([]model.Document, error)
	// Lock takes an exclusive lock on the document row for the rest of
	// the transaction, serializing all block mutations within the
	// document. It also verifies the document exists and fails when it
	// does not. Implementations without row locking (e.g. in-memory
	// fakes) may just verify existence.
	Lock(ctx context.Context, uid model.DocumentUID) error
}

type BlocksCollection interface {
	Create(ctx context.Context, documentUid model.DocumentUID, block model.Block) (model.Block, error)
	// Get the block by id and revision number. If revisionNumber == 0, get
	// the latest, including a deletion revision (needed for restoration
	// and lifecycle checks). Unlike ListLatest, this is a revision lookup.
	Get(ctx context.Context,
		documentUid model.DocumentUID,
		blockNumber model.BlockNumber,
		revisionNumber model.RevisionNumber) (model.Block, error)
	// List all revisions in the document, including deletion revisions.
	List(ctx context.Context, documentUid model.DocumentUID) ([]model.Block, error)
	// List all revisions for a given block, including deletion revisions.
	ListRevisions(ctx context.Context, documentUid model.DocumentUID, blockNumber model.BlockNumber) ([]model.Block, error)
	// ListLatest returns the latest revision of each live block in the
	// document. Select the latest revision before excluding deleted blocks,
	// so an older live revision of a deleted block is never returned.
	ListLatest(ctx context.Context, documentUid model.DocumentUID) ([]model.Block, error)
}
