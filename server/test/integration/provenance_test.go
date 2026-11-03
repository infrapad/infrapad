package integration

// Provenance integration tests against the real PostgreSQL store: the
// nullable parent column round-trip and the document mutation lock
// guaranteeing no committed live child beneath a deleted parent.

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store/postgres"
)

func markdownBlock(text string) model.Block {
	return model.Block{
		Type:    core.MarkdownTypeName,
		Content: []byte(fmt.Sprintf(`{"text":%q}`, text)),
	}
}

// keyedMarkdownRecord builds a keyed Core/Markdown record standing in for
// a generated record carried through the internal host path.
func keyedMarkdownRecord(markdown *core.MarkdownType, key, text string) content.Record {
	return content.NewKeyedItem(markdown, key, time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC), core.Markdown{Text: text})
}

// markdownRegistry builds a registry holding one Core/Markdown instance
// that is also returned to the caller for record construction.
func markdownRegistry(t *testing.T) (*content.Registry, *core.MarkdownType) {
	t.Helper()
	markdown := core.NewMarkdownType()
	registry, err := content.NewRegistry([]content.ContentType{markdown})
	testutil.NoErr(t, err)
	return registry, markdown
}

// TestProvenancePersistenceRoundTrip checks the nullable parent column
// round-trip through the transaction-scoped host path: creation, reads,
// cascading deletion, and parent-only restoration.
func TestProvenancePersistenceRoundTrip(t *testing.T) {
	ctx := context.Background()

	db, err := postgres.OpenPG(testConnStr())
	testutil.NoErr(t, err)
	defer db.Close()

	registry, markdown := markdownRegistry(t)
	ctrl := controller.New(postgres.NewStore(db), registry)

	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "provenance round-trip"})
	testutil.NoErr(t, err)
	root, err := ctrl.AddBlock(ctx, doc.Uid, markdownBlock("root"))
	testutil.NoErr(t, err)

	tx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	testutil.NoErr(t, err)
	child, err := ctrl.CreateChildBlockTx(ctx, tx, doc.Uid, root.BlockNumber, keyedMarkdownRecord(markdown, "md:child", "child"), "host")
	testutil.NoErr(t, err)
	grandchild, err := ctrl.CreateChildBlockTx(ctx, tx, doc.Uid, child.BlockNumber, keyedMarkdownRecord(markdown, "md:grandchild", "grandchild"), "host")
	testutil.NoErr(t, err)
	testutil.NoErr(t, tx.Commit())

	// Parents come back through the individual reads and the block list.
	gotChild, err := ctrl.GetBlock(ctx, doc.Uid, child.BlockNumber, 0)
	testutil.NoErr(t, err)
	if gotChild.ParentBlockNumber == nil || *gotChild.ParentBlockNumber != root.BlockNumber {
		t.Fatalf("child parent not persisted: %v", gotChild.ParentBlockNumber)
	}
	blocks, err := ctrl.ListBlocks(ctx, doc.Uid)
	testutil.NoErr(t, err)
	parents := map[model.BlockNumber]model.BlockNumber{}
	for _, blk := range blocks {
		if blk.ParentBlockNumber != nil {
			parents[blk.BlockNumber] = *blk.ParentBlockNumber
		}
	}
	if parents[child.BlockNumber] != root.BlockNumber || parents[grandchild.BlockNumber] != child.BlockNumber {
		t.Fatalf("unexpected persisted parents: %v", parents)
	}
	if _, ok := parents[root.BlockNumber]; ok {
		t.Fatal("root unexpectedly carries a parent")
	}

	// Cascading deletion and parent-only restoration.
	delTx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	testutil.NoErr(t, err)
	deleted, err := ctrl.DeleteBlockTreeTx(ctx, delTx, doc.Uid, root.BlockNumber, "host")
	testutil.NoErr(t, err)
	testutil.NoErr(t, delTx.Commit())
	if len(deleted) != 3 {
		t.Fatalf("expected 3 deletion revisions, got %d", len(deleted))
	}

	restTx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	testutil.NoErr(t, err)
	_, err = ctrl.RestoreBlockTx(ctx, restTx, doc.Uid, root.BlockNumber, "host")
	testutil.NoErr(t, err)
	testutil.NoErr(t, restTx.Commit())

	for _, number := range []model.BlockNumber{child.BlockNumber, grandchild.BlockNumber} {
		blk, err := ctrl.GetBlock(ctx, doc.Uid, number, 0)
		testutil.NoErr(t, err)
		if blk.Status != model.DeletedBlock {
			t.Fatalf("block %d resurrected with the parent: %q", number, blk.Status)
		}
	}
}

// TestProvenanceConcurrency checks the document mutation lock: with
// concurrent child creation and parent deletion on the same document, no
// committed live child ever ends up beneath a deleted parent.
func TestProvenanceConcurrency(t *testing.T) {
	ctx := context.Background()

	db, err := postgres.OpenPG(testConnStr())
	testutil.NoErr(t, err)
	defer db.Close()

	registry, markdown := markdownRegistry(t)
	ctrl := controller.New(postgres.NewStore(db), registry)

	for i := 0; i < 10; i++ {
		doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "provenance concurrency"})
		testutil.NoErr(t, err)
		root, err := ctrl.AddBlock(ctx, doc.Uid, markdownBlock("root"))
		testutil.NoErr(t, err)

		deleteRoot := func() error {
			tx, err := ctrl.BeginHostTx(ctx, doc.Uid)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if _, err := ctrl.DeleteBlockTreeTx(ctx, tx, doc.Uid, root.BlockNumber, "host"); err != nil {
				return err
			}
			return tx.Commit()
		}
		childRecord := keyedMarkdownRecord(markdown, fmt.Sprintf("md:child-%d", i), "child")
		createChild := func() error {
			tx, err := ctrl.BeginHostTx(ctx, doc.Uid)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if _, err := ctrl.CreateChildBlockTx(ctx, tx, doc.Uid, root.BlockNumber, childRecord, "host"); err != nil {
				return err
			}
			return tx.Commit()
		}

		var wg sync.WaitGroup
		results := make([]error, 2)
		// Start gate: park both goroutines so they are released together
		// and actually race for the document lock, instead of relying on
		// scheduler timing to overlap them.
		start := make(chan struct{})
		for j, op := range []func() error{deleteRoot, createChild} {
			wg.Add(1)
			go func(j int, op func() error) {
				defer wg.Done()
				<-start
				results[j] = op()
			}(j, op)
		}
		close(start)
		wg.Wait()
		if err := results[0]; err != nil {
			t.Fatalf("iteration %d: delete root: %v", i, err)
		}
		// Child creation either commits before the deletion (and is then
		// cascaded away) or is rejected because the parent is already
		// deleted; both outcomes are valid.
		if err := results[1]; err != nil && !controller.IsInvalidInput(err) {
			t.Fatalf("iteration %d: create child: %v", i, err)
		}

		// Invariant: no committed live block has a deleted parent.
		blocks, err := ctrl.ListBlocks(ctx, doc.Uid)
		testutil.NoErr(t, err)
		latest := map[model.BlockNumber]model.Block{}
		for _, blk := range blocks {
			latest[blk.BlockNumber] = blk
		}
		for _, blk := range latest {
			if blk.Status == model.DeletedBlock || blk.ParentBlockNumber == nil {
				continue
			}
			parent, ok := latest[*blk.ParentBlockNumber]
			if !ok || parent.Status == model.DeletedBlock {
				t.Fatalf("iteration %d: live block %d beneath deleted parent %d",
					i, blk.BlockNumber, *blk.ParentBlockNumber)
			}
		}
		if child, ok := latest[root.BlockNumber+1]; ok && child.Status != model.DeletedBlock {
			t.Fatalf("iteration %d: live child %d survived parent deletion", i, child.BlockNumber)
		}
	}
}

// TestProvenanceLockSerialization deterministically verifies that the
// document mutation lock serializes host operations: while one transaction
// holds the lock with an uncommitted root deletion, a concurrent child
// creation stays blocked; once the deletion commits, the creation proceeds
// and is rejected because the parent is deleted.
func TestProvenanceLockSerialization(t *testing.T) {
	ctx := context.Background()

	db, err := postgres.OpenPG(testConnStr())
	testutil.NoErr(t, err)
	defer db.Close()

	registry, markdown := markdownRegistry(t)
	ctrl := controller.New(postgres.NewStore(db), registry)

	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "provenance lock serialization"})
	testutil.NoErr(t, err)
	root, err := ctrl.AddBlock(ctx, doc.Uid, markdownBlock("root"))
	testutil.NoErr(t, err)

	// Hold the document lock with an uncommitted deletion of the root.
	delTx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	testutil.NoErr(t, err)
	_, err = ctrl.DeleteBlockTreeTx(ctx, delTx, doc.Uid, root.BlockNumber, "host")
	testutil.NoErr(t, err)

	lateRecord := keyedMarkdownRecord(markdown, "md:late", "late")
	done := make(chan error, 1)
	go func() {
		tx, err := ctrl.BeginHostTx(ctx, doc.Uid)
		if err != nil {
			done <- err
			return
		}
		defer tx.Rollback()
		if _, err := ctrl.CreateChildBlockTx(ctx, tx, doc.Uid, root.BlockNumber, lateRecord, "host"); err != nil {
			done <- err
			return
		}
		done <- tx.Commit()
	}()

	// While the lock is held, the child creation must stay blocked on the
	// document row lock.
	select {
	case err := <-done:
		t.Fatalf("child creation completed while the document lock was held: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	testutil.NoErr(t, delTx.Commit())

	// After the release the creation proceeds and observes the deleted
	// parent.
	err = <-done
	if err == nil || !controller.IsInvalidInput(err) {
		t.Fatalf("expected invalid input for child beneath deleted parent after lock release, got: %v", err)
	}
}
