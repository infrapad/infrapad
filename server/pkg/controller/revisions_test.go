package controller_test

import (
	"context"
	"strings"
	"testing"

	"github.com/infrapad/infrapad/server/internal/testext"
	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
	"github.com/infrapad/infrapad/server/pkg/model"
)

// TestRevisionPolicyWritePaths checks the content-type revision policy on
// every record write path with the test-only Test/Snapshot type (name is
// revision-worthy, data is refreshed in place): direct updates, Markdown
// edits, payload-primary updates, and matched generated output; covering
// in-place writes, new revisions, no-ops, host overrides (status), policy
// errors without writes, and unchanged behavior of types without a policy.
func TestRevisionPolicyWritePaths(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	doc, err := f.ctrl.CreateDocument(ctx, model.Document{Title: "policy"})
	testutil.NoErr(t, err)
	snap, err := f.ctrl.AddBlock(ctx, doc.Uid, model.Block{Type: testext.SnapshotTypeName, AuthorID: "alice", Content: []byte(`{"name":"A","data":"A1"}`)})
	testutil.NoErr(t, err)
	number := snap.BlockNumber
	update := func(contentJSON string, status model.BlockStatus) (model.Block, error) {
		return f.ctrl.UpdateBlock(ctx, doc.Uid, number, model.Block{Type: testext.SnapshotTypeName, AuthorID: "bob", Status: status, Content: []byte(contentJSON)})
	}
	expectSnapshot := func(blk model.Block, name, data string) {
		t.Helper()
		item, err := content.ItemView[testext.Snapshot](blk.Record)
		testutil.NoErr(t, err)
		if item.ContentValue.Name != name || item.ContentValue.Data != data {
			t.Fatalf("block %d revision %d: unexpected content %+v", blk.BlockNumber, blk.RevisionNumber, item.ContentValue)
		}
	}

	// Direct updates: data-only changes replace revision 1 in place,
	// keeping its author; identical content writes nothing.
	got, err := update(`{"name":"A","data":"A2"}`, "")
	testutil.NoErr(t, err)
	if got.RevisionNumber != 1 || got.AuthorID != "alice" {
		t.Fatalf("expected an in-place write of revision 1 by alice, got revision %d by %q", got.RevisionNumber, got.AuthorID)
	}
	expectSnapshot(expectBlockRevision(t, f, doc.Uid, number, 1), "A", "A2")
	_, err = update(`{"name":"A","data":"A2"}`, "")
	testutil.NoErr(t, err)
	expectBlockRevision(t, f, doc.Uid, number, 1)
	// A status change is host-owned: it creates a revision even when the
	// policy would update in place.
	_, err = update(`{"name":"A","data":"A3"}`, model.ProgressingBlock)
	testutil.NoErr(t, err)
	expectSnapshot(expectBlockRevision(t, f, doc.Uid, number, 2), "A", "A3")
	// A revision-worthy change creates a revision; policy errors write nothing.
	_, err = update(`{"name":"B","data":"B1"}`, model.ProgressingBlock)
	testutil.NoErr(t, err)
	expectBlockRevision(t, f, doc.Uid, number, 3)
	if _, err := update(`{"name":"`+testext.SnapshotPolicyError+`"}`, model.ProgressingBlock); err == nil {
		t.Fatal("expected the policy error to reject the update")
	}
	expectSnapshot(expectBlockRevision(t, f, doc.Uid, number, 3), "B", "B1")
	// Earlier revisions keep their last data.
	rev1, err := f.ctrl.GetBlock(ctx, doc.Uid, number, 1)
	testutil.NoErr(t, err)
	expectSnapshot(rev1, "A", "A2")

	// Markdown: a name edit creates a revision preserving hidden data; a
	// policy error rejects the push.
	exported, err := f.ctrl.ExportDocumentMarkdown(ctx, doc.Uid)
	testutil.NoErr(t, err)
	_, err = f.ctrl.PushDocumentMarkdown(ctx, doc.Uid, strings.Replace(exported, "\nB\n", "\nC\n", 1), "")
	testutil.NoErr(t, err)
	expectSnapshot(expectBlockRevision(t, f, doc.Uid, number, 4), "C", "B1")
	exported, err = f.ctrl.ExportDocumentMarkdown(ctx, doc.Uid)
	testutil.NoErr(t, err)
	if _, err := f.ctrl.PushDocumentMarkdown(ctx, doc.Uid, strings.Replace(exported, "\nC\n", "\n"+testext.SnapshotPolicyError+"\n", 1), ""); err == nil {
		t.Fatal("expected the policy error to reject the push")
	}
	expectBlockRevision(t, f, doc.Uid, number, 4)

	// Payloads: primary data and matched generated output refresh in place.
	submit := func(payload string) error {
		_, err := f.ctrl.SubmitBlockPayload(ctx, doc.Uid, number, []byte(payload), "host")
		return err
	}
	testutil.NoErr(t, submit(`{"data":"C1","children":[{"key":"k","name":"x","data":"x1"}]}`))
	expectSnapshot(expectBlockRevision(t, f, doc.Uid, number, 4), "C", "C1")
	child := childByKey(t, f, doc.Uid, number, "k")
	testutil.NoErr(t, submit(`{"data":"C2","children":[{"key":"k","name":"x","data":"x2"}]}`))
	expectSnapshot(expectBlockRevision(t, f, doc.Uid, number, 4), "C", "C2")
	expectSnapshot(expectBlockRevision(t, f, doc.Uid, child.BlockNumber, 1), "x", "x2")
	// A revision-worthy generated change creates a revision.
	testutil.NoErr(t, submit(`{"children":[{"key":"k","name":"y","data":"y1"}]}`))
	expectSnapshot(expectBlockRevision(t, f, doc.Uid, child.BlockNumber, 2), "y", "y1")
	// A policy error on generated output rejects the whole submission
	// before any write, including the primary's refresh.
	if err := submit(`{"data":"C3","children":[{"key":"k","name":"` + testext.SnapshotPolicyError + `"}]}`); err == nil {
		t.Fatal("expected the policy error to reject the submission")
	}
	expectSnapshot(expectBlockRevision(t, f, doc.Uid, number, 4), "C", "C2")
	expectSnapshot(expectBlockRevision(t, f, doc.Uid, child.BlockNumber, 2), "y", "y1")

	// Types without a policy keep appending revisions, also for identical
	// direct updates.
	md, err := f.ctrl.AddBlock(ctx, doc.Uid, model.Block{Type: core.MarkdownTypeName, Content: []byte(`{"text":"same"}`)})
	testutil.NoErr(t, err)
	_, err = f.ctrl.UpdateBlock(ctx, doc.Uid, md.BlockNumber, model.Block{Type: core.MarkdownTypeName, Content: []byte(`{"text":"same"}`)})
	testutil.NoErr(t, err)
	expectBlockRevision(t, f, doc.Uid, md.BlockNumber, 2)
}
