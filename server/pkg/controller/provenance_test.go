package controller_test

// Provenance contract tests: nested host-owned parent relationships,
// public/internal write boundaries, cascading deletion, and restoration.

import (
	"context"
	"testing"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
	"github.com/infrapad/infrapad/server/pkg/model"
)

// TestProvenanceLifecycle covers the focused provenance scenario: a root,
// child, and grandchild created through the internal host path, provenance
// visible through reads and history, atomic cascading deletion with
// preserved content and parents, and parent-only restoration.
func TestProvenanceLifecycle(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	ctrl := f.ctrl
	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "incident"})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	root, err := ctrl.AddBlock(ctx, doc.Uid, markdownBlock("root"))
	if err != nil {
		t.Fatalf("add root block: %v", err)
	}
	if root.ParentBlockNumber != nil {
		t.Fatalf("public block unexpectedly carries provenance: %d", *root.ParentBlockNumber)
	}

	childTx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("begin child tx: %v", err)
	}
	defer childTx.Rollback()
	childRecord := content.NewKeyedItem(f.markdownType, "md:child", at(0), core.Markdown{Text: "child"})
	child, err := ctrl.CreateChildBlockTx(ctx, childTx, doc.Uid, root.BlockNumber, childRecord, "host")
	if err != nil {
		t.Fatalf("create child block: %v", err)
	}
	if err := childTx.Commit(); err != nil {
		t.Fatalf("commit child tx: %v", err)
	}

	grandchildTx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("begin grandchild tx: %v", err)
	}
	defer grandchildTx.Rollback()
	grandchildRecord := content.NewKeyedItem(f.markdownType, "md:grandchild", at(0), core.Markdown{Text: "grandchild"})
	grandchild, err := ctrl.CreateChildBlockTx(ctx, grandchildTx, doc.Uid, child.BlockNumber, grandchildRecord, "host")
	if err != nil {
		t.Fatalf("create grandchild block: %v", err)
	}
	if err := grandchildTx.Commit(); err != nil {
		t.Fatalf("commit grandchild tx: %v", err)
	}
	if parentOf(t, child) != root.BlockNumber || parentOf(t, grandchild) != child.BlockNumber {
		t.Fatalf("unexpected provenance: child=%d grandchild=%d", child.BlockNumber, grandchild.BlockNumber)
	}

	// Provenance is returned through document reads and block lists.
	gotDoc, err := ctrl.GetDocument(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	if len(gotDoc.Blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(gotDoc.Blocks))
	}
	for _, blk := range gotDoc.Blocks {
		switch blk.BlockNumber {
		case root.BlockNumber:
			if blk.ParentBlockNumber != nil {
				t.Fatalf("root carries provenance: %d", *blk.ParentBlockNumber)
			}
		case child.BlockNumber:
			if parentOf(t, blk) != root.BlockNumber {
				t.Fatalf("child provenance lost: %d", parentOf(t, blk))
			}
		case grandchild.BlockNumber:
			if parentOf(t, blk) != child.BlockNumber {
				t.Fatalf("grandchild provenance lost: %d", parentOf(t, blk))
			}
		}
	}

	// A public update echoing the stored parent is accepted and preserves
	// provenance on the new revision, visible through revision history.
	updated := markdownBlock("child edited")
	updated.ParentBlockNumber = &root.BlockNumber
	if _, err := ctrl.UpdateBlock(ctx, doc.Uid, child.BlockNumber, updated); err != nil {
		t.Fatalf("update child with parent echo: %v", err)
	}
	history, err := ctrl.ListBlockHistory(ctx, doc.Uid, child.BlockNumber)
	if err != nil {
		t.Fatalf("list child history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 child revisions, got %d", len(history))
	}
	for _, rev := range history {
		if parentOf(t, rev) != root.BlockNumber {
			t.Fatalf("revision %d lost provenance: %d", rev.RevisionNumber, parentOf(t, rev))
		}
	}

	// Deleting the root atomically appends deletion revisions for the whole
	// tree, preserving content and parent relationships.
	deleteTx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("begin delete tx: %v", err)
	}
	deleted, err := ctrl.DeleteBlockTreeTx(ctx, deleteTx, doc.Uid, root.BlockNumber, "host")
	if err != nil {
		t.Fatalf("delete block tree: %v", err)
	}
	if err := deleteTx.Commit(); err != nil {
		t.Fatalf("commit delete tx: %v", err)
	}
	if len(deleted) != 3 {
		t.Fatalf("expected 3 deletion revisions, got %d", len(deleted))
	}
	for _, rev := range deleted {
		if rev.Status != model.DeletedBlock {
			t.Fatalf("deletion revision %d has status %q", rev.BlockNumber, rev.Status)
		}
		switch rev.BlockNumber {
		case child.BlockNumber:
			if parentOf(t, rev) != root.BlockNumber {
				t.Fatalf("deletion revision lost provenance: %d", parentOf(t, rev))
			}
		case grandchild.BlockNumber:
			if parentOf(t, rev) != child.BlockNumber {
				t.Fatalf("deletion revision lost provenance: %d", parentOf(t, rev))
			}
		}
	}

	// Content is preserved on deletion revisions and readable through the
	// API paths.
	deletedChild, err := ctrl.GetBlock(ctx, doc.Uid, child.BlockNumber, 0)
	if err != nil {
		t.Fatalf("get deleted child: %v", err)
	}
	if deletedChild.Status != model.DeletedBlock {
		t.Fatalf("child not deleted: %q", deletedChild.Status)
	}
	view, err := recordContentView(deletedChild)
	if err != nil || view != "child edited" {
		t.Fatalf("deletion revision content not preserved: %q (%v)", view, err)
	}

	// Restoring the root restores only the root; descendants stay deleted.
	restoreTx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("begin restore tx: %v", err)
	}
	restored, err := ctrl.RestoreBlockTx(ctx, restoreTx, doc.Uid, root.BlockNumber, "host")
	if err != nil {
		t.Fatalf("restore root: %v", err)
	}
	if err := restoreTx.Commit(); err != nil {
		t.Fatalf("commit restore tx: %v", err)
	}
	if restored.Status != model.PublishedBlock {
		t.Fatalf("restored root status: %q", restored.Status)
	}
	for _, number := range []model.BlockNumber{child.BlockNumber, grandchild.BlockNumber} {
		blk, err := ctrl.GetBlock(ctx, doc.Uid, number, 0)
		if err != nil {
			t.Fatalf("get block %d: %v", number, err)
		}
		if blk.Status != model.DeletedBlock {
			t.Fatalf("block %d resurrected with the parent: %q", number, blk.Status)
		}
	}

	// Reviving a child requires a live parent: the grandchild cannot be
	// restored while its parent stays deleted.
	reviveTx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("begin revive tx: %v", err)
	}
	_, err = ctrl.RestoreBlockTx(ctx, reviveTx, doc.Uid, grandchild.BlockNumber, "host")
	if err == nil || !controller.IsInvalidInput(err) {
		t.Fatalf("expected invalid input restoring beneath a deleted parent, got: %v", err)
	}
	reviveTx.Rollback()
}

// TestProvenanceRejection covers the public/internal write boundaries and
// ancestry validation, including rollback of rejected operations.
func TestProvenanceRejection(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	ctrl := f.ctrl
	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "incident"})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	root, err := ctrl.AddBlock(ctx, doc.Uid, markdownBlock("root"))
	if err != nil {
		t.Fatalf("add root block: %v", err)
	}
	blocks, err := ctrl.ListBlocks(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("list blocks: %v", err)
	}
	blocksBefore := len(blocks)

	// Public creation rejects any supplied provenance, including through
	// initial document blocks and explicit zero.
	withParent := markdownBlock("nope")
	withParent.ParentBlockNumber = &root.BlockNumber
	if _, err := ctrl.AddBlock(ctx, doc.Uid, withParent); err == nil || !controller.IsInvalidInput(err) {
		t.Fatalf("expected invalid input for public provenance assignment, got: %v", err)
	}
	zeroParent := markdownBlock("nope")
	zero := model.BlockNumber(0)
	zeroParent.ParentBlockNumber = &zero
	if _, err := ctrl.AddBlock(ctx, doc.Uid, zeroParent); err == nil || !controller.IsInvalidInput(err) {
		t.Fatalf("expected invalid input for zero parent, got: %v", err)
	}
	initial := withParent
	if _, err := ctrl.CreateDocument(ctx, model.Document{Title: "x", Blocks: []model.Block{initial}}); err == nil || !controller.IsInvalidInput(err) {
		t.Fatalf("expected invalid input for initial-block provenance, got: %v", err)
	}

	// Public updates cannot change or assign provenance.
	changed := markdownBlock("edited")
	wrong := root.BlockNumber + 100
	changed.ParentBlockNumber = &wrong
	if _, err := ctrl.UpdateBlock(ctx, doc.Uid, root.BlockNumber, changed); err == nil || !controller.IsInvalidInput(err) {
		t.Fatalf("expected invalid input for provenance change, got: %v", err)
	}
	if _, err := ctrl.UpdateBlock(ctx, doc.Uid, root.BlockNumber, withParent); err == nil || !controller.IsInvalidInput(err) {
		t.Fatalf("expected invalid input for provenance assignment on update, got: %v", err)
	}

	// Invalid ancestry is rejected: missing parent and a non-positive
	// parent number.
	tx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("begin host tx: %v", err)
	}
	orphanRecord := content.NewKeyedItem(f.markdownType, "md:orphan", at(0), core.Markdown{Text: "orphan"})
	if _, err := ctrl.CreateChildBlockTx(ctx, tx, doc.Uid, model.BlockNumber(999), orphanRecord, "host"); err == nil || !controller.IsInvalidInput(err) {
		t.Fatalf("expected invalid input for missing parent, got: %v", err)
	}
	zeroRecord := content.NewKeyedItem(f.markdownType, "md:zero", at(0), core.Markdown{Text: "zero"})
	if _, err := ctrl.CreateChildBlockTx(ctx, tx, doc.Uid, 0, zeroRecord, "host"); err == nil || !controller.IsInvalidInput(err) {
		t.Fatalf("expected invalid input for zero parent, got: %v", err)
	}
	tx.Rollback()

	// Rejected operations committed nothing.
	blocks, err = ctrl.ListBlocks(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("list blocks: %v", err)
	}
	if len(blocks) != blocksBefore {
		t.Fatalf("rejected operations changed the document: %d blocks", len(blocks))
	}

	// A child cannot be created beneath a deleted parent; the failing
	// transaction is rolled back without changes.
	childTx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("begin child tx: %v", err)
	}
	defer childTx.Rollback()
	childRecord := content.NewKeyedItem(f.markdownType, "md:child", at(0), core.Markdown{Text: "child"})
	child, err := ctrl.CreateChildBlockTx(ctx, childTx, doc.Uid, root.BlockNumber, childRecord, "host")
	if err != nil {
		t.Fatalf("create child block: %v", err)
	}
	if err := childTx.Commit(); err != nil {
		t.Fatalf("commit child tx: %v", err)
	}

	deleteTx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("begin delete tx: %v", err)
	}
	defer deleteTx.Rollback()
	if _, err := ctrl.DeleteBlockTreeTx(ctx, deleteTx, doc.Uid, root.BlockNumber, "host"); err != nil {
		t.Fatalf("delete block tree: %v", err)
	}
	if err := deleteTx.Commit(); err != nil {
		t.Fatalf("commit delete tx: %v", err)
	}
	failTx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("begin host tx: %v", err)
	}
	lateRecord := content.NewKeyedItem(f.markdownType, "md:late", at(0), core.Markdown{Text: "late"})
	if _, err := ctrl.CreateChildBlockTx(ctx, failTx, doc.Uid, root.BlockNumber, lateRecord, "host"); err == nil || !controller.IsInvalidInput(err) {
		t.Fatalf("expected invalid input for child beneath deleted parent, got: %v", err)
	}
	failTx.Rollback()
	latest, err := ctrl.GetBlock(ctx, doc.Uid, child.BlockNumber, 0)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if latest.Status != model.DeletedBlock {
		t.Fatalf("child not deleted by cascade: %q", latest.Status)
	}
	// The document still holds exactly the two blocks; the rejected host
	// transaction added nothing (root and child carry one creation and one
	// deletion revision each).
	blocks, err = ctrl.ListBlocks(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("list blocks: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks after rejections, got %d", len(blocks))
	}
	if got := len(f.store.blocks[doc.Uid]); got != 4 {
		t.Fatalf("expected 4 stored revisions, got %d", got)
	}
}

func markdownBlock(text string) model.Block {
	return model.Block{
		Type:    core.MarkdownTypeName,
		Content: []byte(`{"text":"` + text + `"}`),
	}
}

func parentOf(t *testing.T, blk model.Block) model.BlockNumber {
	t.Helper()
	if blk.ParentBlockNumber == nil {
		t.Fatalf("block %d: expected a parent, got a root", blk.BlockNumber)
	}
	return *blk.ParentBlockNumber
}

func recordContentView(blk model.Block) (string, error) {
	view, err := content.ItemView[core.Markdown](blk.Record)
	if err != nil {
		return "", err
	}
	return view.ContentValue.Text, nil
}
