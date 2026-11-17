package controller

// Record persistence: changed records either create a revision or replace
// the latest revision in place. No-ops are handled before persistence.
//
// Direct updates and generated-record helpers use storeRecord; Markdown
// and reconciliation plans call shouldCreateRevision before any writes,
// then persist through applyRecordWrite. All paths run after validation
// and ownership checks inside the document-locked transaction.

import (
	"context"
	"fmt"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store"
)

// storeRecord persists record over latest. For policy-enabled types,
// unchanged records with no status change return latest without a write
// or policy call. Types without a policy retain their path-specific
// no-op behavior. Callers validate the record and identity first.
func storeRecord(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, latest model.Block, record content.Record, status model.BlockStatus, author model.AuthorID) (model.Block, error) {
	if _, ok := latest.Record.ContentType().(content.RevisionPolicy); ok &&
		status == latest.Status && latest.Status != model.DeletedBlock &&
		content.EqualRecords(latest.Record, record) {
		return latest, nil
	}
	newRevision, err := shouldCreateRevision(latest, record, status)
	if err != nil {
		return model.Block{}, err
	}
	return applyRecordWrite(ctx, tx, documentUID, latest, record, status, author, newRevision)
}

// shouldCreateRevision reports whether a validated record change creates
// a new revision instead of replacing the latest revision in place.
// Callers exclude no-ops first. Host-owned status/lifecycle and structural
// changes always create revisions; only inner-content changes are
// delegated to the content type's policy. It performs no writes.
func shouldCreateRevision(latest model.Block, record content.Record, status model.BlockStatus) (bool, error) {
	policy, ok := latest.Record.ContentType().(content.RevisionPolicy)
	if !ok {
		return true, nil
	}
	if status != latest.Status || latest.Status == model.DeletedBlock {
		return true, nil
	}
	if !content.EqualRecordStructure(latest.Record, record) {
		return true, nil
	}
	newRevision, err := policy.ShouldCreateRevision(latest.Record, record)
	if err != nil {
		return false, fmt.Errorf("block %d: %s revision policy: %w", latest.BlockNumber, latest.Type, err)
	}
	return newRevision, nil
}

// applyRecordWrite persists a changed record over latest and returns the
// resulting block (record set). If newRevision is true, it appends a
// revision with status and author, preserving type and parent. Otherwise
// it replaces the latest revision in place, preserving all revision
// metadata. It performs no validation or no-op checks.
func applyRecordWrite(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, latest model.Block, record content.Record, status model.BlockStatus, author model.AuthorID, newRevision bool) (model.Block, error) {
	blk := latest
	blk.Record = record
	if err := serializeRecord(&blk); err != nil {
		return model.Block{}, err
	}
	var persisted model.Block
	var err error
	if newRevision {
		persisted, err = appendRevision(ctx, tx, documentUID, blk, status, author)
	} else {
		persisted, err = tx.Blocks().ReplaceLatest(ctx, documentUID, latest.BlockNumber, latest.RevisionNumber, blk.SerializedRecord)
		if err != nil {
			return model.Block{}, fmt.Errorf("replace latest revision of block %d: %w", latest.BlockNumber, err)
		}
	}
	if err != nil {
		return model.Block{}, err
	}
	persisted.Record = record
	return persisted, nil
}
