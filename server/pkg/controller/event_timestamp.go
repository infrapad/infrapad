package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store"
)

// finalizeBlockCreation applies the actual stored creation time as the
// fallback for content timestamps. The store assigns CreatedAt on insert;
// the provisional event time must not drift from it on the next refresh.
// Replace the new record inside the creation transaction without adding
// a revision. Explicitly timestamped and ordinary types need no write.
func finalizeBlockCreation(ctx context.Context, tx store.Tx, uid model.DocumentUID, created model.Block) (model.Block, error) {
	record, err := content.ApplyEventTimestamp(created.Record, created.CreatedAt)
	if err != nil {
		return model.Block{}, err
	}
	if record.Timestamp().Equal(created.Record.Timestamp()) {
		return created, nil
	}
	created.Record = record
	if err := serializeRecord(&created); err != nil {
		return model.Block{}, err
	}
	if _, err := tx.Blocks().ReplaceLatest(ctx, uid, created.BlockNumber, created.RevisionNumber, created.SerializedRecord); err != nil {
		return model.Block{}, fmt.Errorf("finalize block %d creation timestamp: %w", created.BlockNumber, err)
	}
	return created, nil
}

// originalBlockCreationTime supplies the stable fallback for types that
// opt into explicit content timestamps. A revision's CreatedAt is not the
// block's creation time; revision 1 remains authoritative even when the
// block was originally created with an explicit event timestamp.
func originalBlockCreationTime(ctx context.Context, tx store.Tx, uid model.DocumentUID, latest model.Block) (time.Time, error) {
	if _, ok := latest.Record.ContentType().(content.EventTimestamper); !ok {
		return time.Time{}, nil // unused by other content types
	}
	if latest.RevisionNumber == 1 {
		return latest.CreatedAt, nil
	}
	first, err := tx.Blocks().Get(ctx, uid, latest.BlockNumber, 1)
	if err != nil {
		return time.Time{}, fmt.Errorf("get block %d creation time: %w", latest.BlockNumber, err)
	}
	return first.CreatedAt, nil
}
