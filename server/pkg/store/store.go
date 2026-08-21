package store

import (
	"context"

	"github.com/infrapad/infrapad/server/pkg/model"
)

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
}

type BlocksCollection interface {
	Create(ctx context.Context, documentUid model.DocumentUID, block model.Block) (model.Block, error)
	// Get the block by id and revision number. If revisionNumber == 0, get the latest.
	Get(ctx context.Context,
		documentUid model.DocumentUID,
		blockNumber model.BlockNumber,
		revisionNumber model.RevisionNumber) (model.Block, error)
	List(ctx context.Context, documentUid model.DocumentUID) ([]model.Block, error)
	// List all revisionions for a given block
	ListRevisions(ctx context.Context, documentUid model.DocumentUID, blockNumber model.BlockNumber) ([]model.Block, error)
	// List only the latest blocks inside the document.
	ListLatest(ctx context.Context, documentUid model.DocumentUID) ([]model.Block, error)
}
