package controller_test

// Bounded cascading expansion with the test-only Test/Ref expander
// (internal/testext): one lifecycle covering nested output with reference
// enrichment, identical resubmission with zero revisions, downstream-only
// payload changes, and missing-output subtree removal, plus the focused
// cycle/depth and sibling-reuse checks.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/infrapad/infrapad/server/internal/testext"
	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/model"
)

// TestCascadingExpansionLifecycle stages the complete tree before
// persisting: a reference is created directly with its final (enriched)
// record, identical trees create zero revisions, and only final
// differences are ever applied.
func TestCascadingExpansionLifecycle(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	doc, err := f.ctrl.CreateDocument(ctx, model.Document{Title: "cascading"})
	testutil.NoErr(t, err)
	root, err := f.ctrl.AddBlock(ctx, doc.Uid, model.Block{Type: testext.RefTypeName, Content: []byte(`{"name":"root"}`)})
	testutil.NoErr(t, err)

	submit := func(payload string) error {
		_, err := f.ctrl.SubmitBlockPayload(ctx, doc.Uid, root.BlockNumber, []byte(payload), "")
		return err
	}
	revisions := func() int { return len(f.store.blocks[doc.Uid]) }

	// Nested output with reference enrichment: the root is enriched with
	// its payload note, reference "a" with its own downstream note, and
	// "a" emits "a1".
	nested := `{"note":"n0","children":[{"name":"a","payload":{"note":"n1","children":[{"name":"a1"}]}}]}`
	testutil.NoErr(t, submit(nested))
	rootRev := expectBlockRevision(t, f, doc.Uid, root.BlockNumber, 2)
	if note := refNote(t, rootRev); note != "n0" {
		t.Fatalf("root not enriched: %q", note)
	}
	a := childByKey(t, f, doc.Uid, root.BlockNumber, "a")
	a1 := childByKey(t, f, doc.Uid, a.BlockNumber, "a1")
	// The reference is created once with its final record: no creation
	// followed by a separate enrichment revision.
	if a.RevisionNumber != 1 || a1.RevisionNumber != 1 {
		t.Fatalf("expected single revisions, got a=%d a1=%d", a.RevisionNumber, a1.RevisionNumber)
	}
	if note := refNote(t, a); note != "n1" {
		t.Fatalf("reference a not enriched: %q", note)
	}
	if note := refNote(t, a1); note != "" {
		t.Fatalf("reference a1 unexpectedly enriched: %q", note)
	}

	// An identical snapshot creates zero revisions throughout the tree.
	before := revisions()
	testutil.NoErr(t, submit(nested))
	if got := revisions(); got != before {
		t.Fatalf("identical tree appended %d revisions", got-before)
	}

	// A downstream-only change: the root's emitted reference content is
	// unchanged, only a's payload note differs. Exactly one revision, of
	// a's block; the root and a1 keep theirs.
	testutil.NoErr(t, submit(`{"note":"n0","children":[{"name":"a","payload":{"note":"n2","children":[{"name":"a1"}]}}]}`))
	if got := revisions() - before; got != 1 {
		t.Fatalf("expected exactly one revision, got %d", got)
	}
	a = childByKey(t, f, doc.Uid, root.BlockNumber, "a")
	if a.RevisionNumber != 2 || refNote(t, a) != "n2" {
		t.Fatalf("reference a not revised to the downstream note: rev %d", a.RevisionNumber)
	}
	expectBlockRevision(t, f, doc.Uid, root.BlockNumber, 2)
	expectBlockRevision(t, f, doc.Uid, a1.BlockNumber, 1)

	// Missing output removes the subtree: a retires with its descendant.
	testutil.NoErr(t, submit(`{"note":"n0","children":[]}`))
	expectStatus(t, f, doc.Uid, a.BlockNumber, model.DeletedBlock)
	expectStatus(t, f, doc.Uid, a1.BlockNumber, model.DeletedBlock)
	expectBlockRevision(t, f, doc.Uid, root.BlockNumber, 2)
}

// TestCascadingExpansionBounds covers the recursion bounds: an identical
// ancestor input tuple is a cycle, the same reference with a different
// payload and identical work in sibling branches are not, and the ninth
// expansion level is rejected before its expander runs.
func TestCascadingExpansionBounds(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	doc, err := f.ctrl.CreateDocument(ctx, model.Document{Title: "bounds"})
	testutil.NoErr(t, err)
	root, err := f.ctrl.AddBlock(ctx, doc.Uid, model.Block{Type: testext.RefTypeName, Content: []byte(`{"name":"root"}`)})
	testutil.NoErr(t, err)
	submit := func(payload string) error {
		_, err := f.ctrl.SubmitBlockPayload(ctx, doc.Uid, root.BlockNumber, []byte(payload), "")
		return err
	}
	revisions := len(f.store.blocks[doc.Uid])

	// Identical work in separate sibling branches is allowed: the cycle
	// check compares only the active ancestor path.
	testutil.NoErr(t, submit(`{"children":[{"name":"a","payload":{"note":"x"}},{"name":"b","payload":{"note":"x"}}]}`))
	revisions = len(f.store.blocks[doc.Uid])
	// The same reference content with a different payload is not a cycle:
	// "c" emits "c" with an empty payload, which terminates.
	testutil.NoErr(t, submit(`{"children":[{"name":"c","payload":{"children":[{"name":"c"}]}}]}`))
	revisions = len(f.store.blocks[doc.Uid])

	// An identical ancestor input tuple (content and payload) is a cycle.
	err = submit(`{"repeat":true}`)
	var submission *controller.SubmissionError
	if !errors.As(err, &submission) || submission.Reason != controller.ReasonExpansionCycle {
		t.Fatalf("expected %s, got %v", controller.ReasonExpansionCycle, err)
	}
	if want := []controller.ExpansionPathStep{{BlockNumber: root.BlockNumber}, {ReferenceKey: "repeat"}}; !reflect.DeepEqual(submission.ExpansionPath, want) {
		t.Fatalf("expected expansion path %v, got %v", want, submission.ExpansionPath)
	}
	if got := len(f.store.blocks[doc.Uid]); got != revisions {
		t.Fatalf("cycle rejection appended %d revisions", got-revisions)
	}

	// Eight levels (root included) are accepted; an attempted ninth is
	// rejected before its expander is invoked.
	chain := func(levels int) string {
		payload := `{}`
		for n := levels - 1; n >= 1; n-- {
			payload = fmt.Sprintf(`{"children":[{"name":"L%d","payload":%s}]}`, n, payload)
		}
		return payload
	}
	invoked := f.refType.Expansions()
	testutil.NoErr(t, submit(chain(maxExpansionLevels)))
	if got := f.refType.Expansions() - invoked; got != maxExpansionLevels {
		t.Fatalf("expected %d invocations for %d levels, got %d", maxExpansionLevels, maxExpansionLevels, got)
	}
	revisions = len(f.store.blocks[doc.Uid])

	invoked = f.refType.Expansions()
	err = submit(chain(maxExpansionLevels + 1))
	if !errors.As(err, &submission) || submission.Reason != controller.ReasonExpansionDepthExceeded {
		t.Fatalf("expected %s, got %v", controller.ReasonExpansionDepthExceeded, err)
	}
	// The root plus seven references expanded; the ninth level never ran.
	if got := f.refType.Expansions() - invoked; got != maxExpansionLevels {
		t.Fatalf("expander ran %d times, expected rejection after %d", got, maxExpansionLevels)
	}
	if got := len(f.store.blocks[doc.Uid]); got != revisions {
		t.Fatalf("depth rejection appended %d revisions", got-revisions)
	}
}

// maxExpansionLevels mirrors the controller's fixed bound (root included).
const maxExpansionLevels = 8

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// childByKey returns the live direct child of parent whose (single-member)
// record carries key, failing the test when it does not exist.
func childByKey(t *testing.T, f *fixture, documentUID model.DocumentUID, parent model.BlockNumber, key string) model.Block {
	t.Helper()
	blocks, err := f.ctrl.ListBlocks(context.Background(), documentUID)
	testutil.NoErr(t, err)
	for _, blk := range blocks {
		if blk.Status == model.DeletedBlock || blk.ParentBlockNumber == nil || *blk.ParentBlockNumber != parent {
			continue
		}
		if members := content.Members(blk.Record); len(members) == 1 && members[0].Key() == key {
			return blk
		}
	}
	t.Fatalf("no live child of %d with key %q", parent, key)
	return model.Block{}
}

// expectBlockRevision checks and returns the latest revision of a block.
func expectBlockRevision(t *testing.T, f *fixture, documentUID model.DocumentUID, number model.BlockNumber, revision model.RevisionNumber) model.Block {
	t.Helper()
	blk, err := f.ctrl.GetBlock(context.Background(), documentUID, number, 0)
	testutil.NoErr(t, err)
	if blk.RevisionNumber != revision {
		t.Fatalf("block %d: expected revision %d, got %d", number, revision, blk.RevisionNumber)
	}
	return blk
}

// refNote returns the note of a Test/Ref record.
func refNote(t *testing.T, blk model.Block) string {
	t.Helper()
	item, err := content.ItemView[testext.Ref](blk.Record)
	if err != nil {
		t.Fatalf("block %d: %v", blk.BlockNumber, err)
	}
	return item.ContentValue.Note
}
