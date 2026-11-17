package integration

// Content-type revision policies against the real PostgreSQL store:
// in-place refreshes of the latest revision, a subsequent new revision,
// preserved earlier snapshots and attribution, and rejection of
// historical replacement.

import (
	"context"
	"errors"
	"testing"

	"github.com/infrapad/infrapad/server/internal/testext"
	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store"
	"github.com/infrapad/infrapad/server/pkg/store/postgres"
)

// TestRevisionPolicySnapshots follows the agreed snapshot semantics:
// revision 1 holds A/A1, a refresh replaces A1 with A2 in revision 1, a
// metadata change creates revision 2, and later refreshes only touch
// revision 2 while revision 1 keeps A/A2 with its author and creation
// time. Replacing a superseded revision fails in the store.
func TestRevisionPolicySnapshots(t *testing.T) {
	ctx := context.Background()

	db, err := postgres.OpenPG(testConnStr())
	testutil.NoErr(t, err)
	defer db.Close()
	registry, err := content.NewRegistry([]content.ContentType{testext.NewSnapshotType()})
	testutil.NoErr(t, err)
	pg := postgres.NewStore(db)
	ctrl := controller.New(pg, registry)

	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "revision policy"})
	testutil.NoErr(t, err)
	created, err := ctrl.AddBlock(ctx, doc.Uid, model.Block{Type: testext.SnapshotTypeName, AuthorID: "alice", Content: []byte(`{"name":"A","data":"A1"}`)})
	testutil.NoErr(t, err)
	number := created.BlockNumber
	update := func(contentJSON string) model.Block {
		t.Helper()
		blk, err := ctrl.UpdateBlock(ctx, doc.Uid, number, model.Block{Type: testext.SnapshotTypeName, AuthorID: "bob", Content: []byte(contentJSON)})
		testutil.NoErr(t, err)
		return blk
	}
	expect := func(revision model.RevisionNumber, name, data string) model.Block {
		t.Helper()
		blk, err := ctrl.GetBlock(ctx, doc.Uid, number, revision)
		testutil.NoErr(t, err)
		item, err := content.ItemView[testext.Snapshot](blk.Record)
		testutil.NoErr(t, err)
		if item.ContentValue.Name != name || item.ContentValue.Data != data {
			t.Fatalf("revision %d: unexpected content %+v", blk.RevisionNumber, item.ContentValue)
		}
		return blk
	}

	if blk := update(`{"name":"A","data":"A2"}`); blk.RevisionNumber != 1 {
		t.Fatalf("expected an in-place refresh of revision 1, got revision %d", blk.RevisionNumber)
	}
	rev1 := expect(1, "A", "A2")
	if rev1.AuthorID != "alice" || !rev1.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("in-place write changed attribution: %q at %v", rev1.AuthorID, rev1.CreatedAt)
	}
	if blk := update(`{"name":"B"}`); blk.RevisionNumber != 2 || blk.AuthorID != "bob" {
		t.Fatalf("expected revision 2 by bob, got revision %d by %q", blk.RevisionNumber, blk.AuthorID)
	}
	update(`{"name":"B","data":"B1"}`)
	expect(0, "B", "B1")
	expect(2, "B", "B1")
	expect(1, "A", "A2")
	history, err := ctrl.ListBlockHistory(ctx, doc.Uid, number)
	testutil.NoErr(t, err)
	if len(history) != 2 {
		t.Fatalf("expected 2 revisions, got %d", len(history))
	}

	// Historical replacement is rejected by the store guard.
	tx, err := pg.Begin(ctx)
	testutil.NoErr(t, err)
	defer tx.Rollback()
	_, err = tx.Blocks().ReplaceLatest(ctx, doc.Uid, number, 1, rev1.SerializedRecord)
	if !errors.Is(err, store.ErrNotLatestRevision) {
		t.Fatalf("expected ErrNotLatestRevision for a superseded revision, got %v", err)
	}
}
