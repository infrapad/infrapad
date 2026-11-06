package integration

// Bounded cascading expansion against the real PostgreSQL store: a nested
// tree commits atomically, a downstream failure leaves the complete
// document and revision history unchanged, and rolling back after a
// successful expansion discards every write. The controller fake store's
// rollback is a no-op, so only this proves database rollback.

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/infrapad/infrapad/server/internal/testext"
	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store/postgres"
)

func TestCascadingExpansionTransactions(t *testing.T) {
	ctx := context.Background()

	db, err := postgres.OpenPG(testConnStr())
	testutil.NoErr(t, err)
	defer db.Close()

	refType := testext.NewRefType()
	registry, err := content.NewRegistry([]content.ContentType{refType})
	testutil.NoErr(t, err)
	ctrl := controller.New(postgres.NewStore(db), registry)

	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "cascading expansion"})
	testutil.NoErr(t, err)
	root, err := ctrl.AddBlock(ctx, doc.Uid, model.Block{Type: testext.RefTypeName, Content: []byte(`{"name":"root"}`)})
	testutil.NoErr(t, err)

	// documentSnapshot captures the complete document and revision
	// history, so unchanged documents compare equal.
	documentSnapshot := func() ([]model.Block, map[model.BlockNumber]int) {
		t.Helper()
		blocks, err := ctrl.ListBlocks(ctx, doc.Uid)
		testutil.NoErr(t, err)
		counts := map[model.BlockNumber]int{}
		for _, blk := range blocks {
			history, err := ctrl.ListBlockHistory(ctx, doc.Uid, blk.BlockNumber)
			testutil.NoErr(t, err)
			counts[blk.BlockNumber] = len(history)
		}
		return blocks, counts
	}
	submit := func(payload string) error {
		_, err := ctrl.SubmitBlockPayload(ctx, doc.Uid, root.BlockNumber, []byte(payload), "host")
		return err
	}

	// A nested tree commits atomically: the reference is created with its
	// final (enriched) record, and its child beneath it.
	testutil.NoErr(t, submit(`{"children":[{"name":"a","payload":{"note":"n1","children":[{"name":"a1"}]}}]}`))
	blocks, _ := documentSnapshot()
	var a, a1 *model.Block
	for n := range blocks {
		blk := blocks[n]
		if keyed, ok := blk.Record.(content.KeyedRecord); ok {
			switch keyed.Key() {
			case "a":
				a = &blocks[n]
			case "a1":
				a1 = &blocks[n]
			}
		}
	}
	if a == nil || a1 == nil || *a1.ParentBlockNumber != a.BlockNumber || *a.ParentBlockNumber != root.BlockNumber {
		t.Fatalf("expected a nested tree, got %+v", blocks)
	}
	if a.RevisionNumber != 1 {
		t.Fatalf("expected reference a created once with its final record, got revision %d", a.RevisionNumber)
	}

	// A downstream failure (invalid downstream payload) leaves the
	// complete document and revision history unchanged.
	beforeBlocks, beforeCounts := documentSnapshot()
	err = submit(`{"children":[{"name":"a","payload":{"bogus":1}}]}`)
	var submission *controller.SubmissionError
	if !errors.As(err, &submission) || submission.Reason != controller.ReasonInvalidOutput {
		t.Fatalf("expected %s, got %v", controller.ReasonInvalidOutput, err)
	}
	afterBlocks, afterCounts := documentSnapshot()
	if !reflect.DeepEqual(beforeBlocks, afterBlocks) || !reflect.DeepEqual(beforeCounts, afterCounts) {
		t.Fatalf("rejected submission changed the document: %v -> %v", beforeBlocks, afterBlocks)
	}

	// A successful expansion rolled back discards every write, proving
	// the whole tree shares one database transaction.
	rootLatest, err := ctrl.GetBlock(ctx, doc.Uid, root.BlockNumber, 0)
	testutil.NoErr(t, err)
	tx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	testutil.NoErr(t, err)
	testutil.NoErr(t, ctrl.ExpandTreeTx(ctx, tx, doc.Uid, rootLatest,
		[]byte(`{"children":[{"name":"a","payload":{"note":"n2","children":[{"name":"a1"},{"name":"a2"}]}}]}`), "host"))
	testutil.NoErr(t, tx.Rollback())
	afterBlocks, afterCounts = documentSnapshot()
	if !reflect.DeepEqual(beforeBlocks, afterBlocks) || !reflect.DeepEqual(beforeCounts, afterCounts) {
		t.Fatalf("rollback changed the document: %v -> %v", beforeBlocks, afterBlocks)
	}
}
