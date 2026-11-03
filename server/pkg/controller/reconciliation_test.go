package controller_test

// Snapshot reconciliation lifecycle with the Slack/AI-agent slice: two
// thread primaries whose output interleaves, reconciled through the
// trusted host path in document-locked transactions.

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
	"github.com/infrapad/infrapad/server/pkg/extensions/slack"
	"github.com/infrapad/infrapad/server/pkg/model"
)

func TestSnapshotReconciliationLifecycle(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	ctrl := f.ctrl
	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "reconciliation"})
	testutil.NoErr(t, err)
	t1 := addThread(t, f, doc.Uid, "p1")
	t2 := addThread(t, f, doc.Uid, "p2")

	reconcile := func(thread model.Block, messages ...slack.SourceMessage) controller.ReconcileResult {
		t.Helper()
		result := expandThread(t, f, thread, messages...)
		tx, err := ctrl.BeginHostTx(ctx, doc.Uid)
		testutil.NoErr(t, err)
		defer tx.Rollback()
		res, err := ctrl.ReconcileExpansionTx(ctx, tx, doc.Uid, thread.BlockNumber, result, "host")
		testutil.NoErr(t, err)
		testutil.NoErr(t, tx.Commit())
		return res
	}
	revisions := func() int { return len(f.store.blocks[doc.Uid]) }

	m1 := sourceMessage("1", 0, 0, "Alice", false)
	m2 := sourceMessage("2", 1, 0, "Bob", false)
	m3 := sourceMessage("3", 2, 0, "investigation-agent", true)
	m4 := sourceMessage("4", 3, 0, "Alice", false)
	m4b := sourceMessage("4b", 3, 40, "Carol", false)
	m5 := sourceMessage("5", 4, 0, "Bob", false)

	// Initial output: the agent message splits the human conversation.
	res := reconcile(t1, m1, m2, m3, m4, m5)
	expectChildren(t, f, doc.Uid, t1.BlockNumber, map[model.BlockNumber][]string{
		3: {"1", "2"}, 4: {"3"}, 5: {"4", "5"},
	})
	if res.Output[slack.MessageKey("2")] != 3 || res.Output[slack.MessageKey("3")] != 4 || res.Output[slack.MessageKey("5")] != 5 {
		t.Fatalf("unexpected output mapping: %v", res.Output)
	}

	// An identical snapshot creates no revisions anywhere.
	before := revisions()
	if res := reconcile(t1, m1, m2, m3, m4, m5); len(res.Revisions) != 0 || revisions() != before {
		t.Fatalf("identical snapshot appended revisions: %+v", res.Revisions)
	}

	// A late event of another primary splits the existing conversation;
	// the fragment holding the earliest member keeps the block number.
	x := sourceMessage("x", 3, 30, "Dave", false)
	reconcile(t2, x)
	expectChildren(t, f, doc.Uid, t2.BlockNumber, map[model.BlockNumber][]string{6: {"x"}})
	expectChildren(t, f, doc.Uid, t1.BlockNumber, map[model.BlockNumber][]string{
		3: {"1", "2"}, 4: {"3"}, 5: {"4"}, 7: {"5"},
	})
	expectRevision(t, f, doc.Uid, 3, 1)
	expectRevision(t, f, doc.Uid, 5, 2)

	// Removing the separator keeps the surviving groups distinct.
	before = revisions()
	reconcile(t2)
	if got := revisions() - before; got != 1 {
		t.Fatalf("expected only the separator deletion, got %d revisions", got)
	}
	expectChildren(t, f, doc.Uid, t1.BlockNumber, map[model.BlockNumber][]string{
		3: {"1", "2"}, 4: {"3"}, 5: {"4"}, 7: {"5"},
	})
	expectStatus(t, f, doc.Uid, 6, model.DeletedBlock)

	// A member inserted between preserved compatible groups attaches to
	// the preceding group.
	reconcile(t1, m1, m2, m3, m4, m4b, m5)
	expectChildren(t, f, doc.Uid, t1.BlockNumber, map[model.BlockNumber][]string{
		3: {"1", "2"}, 4: {"3"}, 5: {"4", "4b"}, 7: {"5"},
	})
	expectRevision(t, f, doc.Uid, 7, 1)

	// A nested descendant of the agent block, interleaved between 4 and 4b.
	tx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	testutil.NoErr(t, err)
	nested, err := ctrl.CreateChildBlockTx(ctx, tx, doc.Uid, 4,
		content.NewKeyedItem(f.markdownType, "note", messageTime(3, 20), core.Markdown{Text: "note"}), "host")
	testutil.NoErr(t, err)
	testutil.NoErr(t, tx.Commit())

	// Removing the anchor and the agent message: the conversation keeps
	// its number, the agent block and its descendant are deleted, and the
	// retired descendant no longer splits the following group.
	reconcile(t1, m2, m4, m4b, m5)
	expectChildren(t, f, doc.Uid, t1.BlockNumber, map[model.BlockNumber][]string{
		3: {"2"}, 5: {"4", "4b"}, 7: {"5"},
	})
	expectStatus(t, f, doc.Uid, 4, model.DeletedBlock)
	expectStatus(t, f, doc.Uid, nested.BlockNumber, model.DeletedBlock)

	// Removing all output, then returning keys: no resurrection.
	reconcile(t1)
	expectChildren(t, f, doc.Uid, t1.BlockNumber, map[model.BlockNumber][]string{})
	res = reconcile(t1, m1, m2, m3, m4, m5)
	expectChildren(t, f, doc.Uid, t1.BlockNumber, map[model.BlockNumber][]string{
		9: {"1", "2"}, 10: {"3"}, 11: {"4", "5"},
	})
	for _, number := range []model.BlockNumber{3, 4, 5, 7} {
		expectStatus(t, f, doc.Uid, number, model.DeletedBlock)
	}
}

// TestReconciliationEqualTimePlanning covers deterministic planning of
// equal timestamps across primaries: members order by key within a group,
// owners order by primary block number, and new blocks are allocated in
// planner order.
func TestReconciliationEqualTimePlanning(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	ctrl := f.ctrl
	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "equal time"})
	testutil.NoErr(t, err)
	t1 := addThread(t, f, doc.Uid, "p1")
	t2 := addThread(t, f, doc.Uid, "p2")

	tx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	testutil.NoErr(t, err)
	defer tx.Rollback()
	_, err = ctrl.ReconcileExpansionTx(ctx, tx, doc.Uid, t1.BlockNumber, expandThread(t, f, t1,
		sourceMessage("b", 0, 0, "Bob", false),
		sourceMessage("a", 0, 0, "Alice", false),
		sourceMessage("d", 1, 0, "Dave", false),
	), "host")
	testutil.NoErr(t, err)
	// Same transaction: the second reconciliation sees the first one's
	// output. Thread 2 sorts after thread 1 at equal time.
	_, err = ctrl.ReconcileExpansionTx(ctx, tx, doc.Uid, t2.BlockNumber, expandThread(t, f, t2,
		sourceMessage("c", 0, 0, "Carol", false),
		sourceMessage("e", 1, 0, "Erin", false),
	), "host")
	testutil.NoErr(t, err)
	testutil.NoErr(t, tx.Commit())

	expectChildren(t, f, doc.Uid, t1.BlockNumber, map[model.BlockNumber][]string{3: {"a", "b"}, 5: {"d"}})
	expectChildren(t, f, doc.Uid, t2.BlockNumber, map[model.BlockNumber][]string{4: {"c"}, 6: {"e"}})
}

// addThread adds a Slack thread root primary.
func addThread(t *testing.T, f *fixture, documentUID model.DocumentUID, id string) model.Block {
	t.Helper()
	thread, err := f.ctrl.AddBlock(context.Background(), documentUID, model.Block{
		Type:    slack.ThreadTypeName,
		Content: []byte(fmt.Sprintf(`{"url":"https://slack.example/archives/C1/%s"}`, id)),
	})
	testutil.NoErr(t, err)
	return thread
}

// expandThread runs the Slack thread expansion and host validation for a
// snapshot of messages, as the payload-submission caller would.
func expandThread(t *testing.T, f *fixture, thread model.Block, messages ...slack.SourceMessage) content.ExpansionResult {
	t.Helper()
	payload, err := json.Marshal(slack.Snapshot{Messages: append([]slack.SourceMessage{}, messages...)})
	testutil.NoErr(t, err)
	testutil.NoErr(t, f.registry.ValidatePayload(slack.ThreadTypeName, payload))
	result, err := f.threadType.Expand(context.Background(), thread.Record, payload)
	testutil.NoErr(t, err)
	testutil.NoErr(t, f.registry.ValidateExpansion(thread.Record, result))
	return result
}

func sourceMessage(id string, minute, second int, author string, agent bool) slack.SourceMessage {
	return slack.SourceMessage{ID: id, Timestamp: messageTime(minute, second), Author: author, Text: "message " + id, Agent: agent}
}

func messageTime(minute, second int) time.Time {
	return time.Date(2026, 10, 25, 10, minute, second, 0, time.UTC)
}

// expectChildren checks the live direct children of parent: block number
// to Slack message IDs in persisted member order.
func expectChildren(t *testing.T, f *fixture, documentUID model.DocumentUID, parent model.BlockNumber, want map[model.BlockNumber][]string) {
	t.Helper()
	blocks, err := f.ctrl.ListBlocks(context.Background(), documentUID)
	testutil.NoErr(t, err)
	got := map[model.BlockNumber][]string{}
	for _, blk := range blocks {
		if blk.Status == model.DeletedBlock || blk.ParentBlockNumber == nil || *blk.ParentBlockNumber != parent {
			continue
		}
		var ids []string
		for _, member := range content.Members(blk.Record) {
			ids = append(ids, member.Key()[len("slack-message:"):])
		}
		got[blk.BlockNumber] = ids
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("children of %d: expected %v, got %v", parent, want, got)
	}
}

func expectRevision(t *testing.T, f *fixture, documentUID model.DocumentUID, number model.BlockNumber, revision model.RevisionNumber) {
	t.Helper()
	blk, err := f.ctrl.GetBlock(context.Background(), documentUID, number, 0)
	testutil.NoErr(t, err)
	if blk.RevisionNumber != revision {
		t.Fatalf("block %d: expected revision %d, got %d", number, revision, blk.RevisionNumber)
	}
}

func expectStatus(t *testing.T, f *fixture, documentUID model.DocumentUID, number model.BlockNumber, status model.BlockStatus) {
	t.Helper()
	blk, err := f.ctrl.GetBlock(context.Background(), documentUID, number, 0)
	testutil.NoErr(t, err)
	if blk.Status != status {
		t.Fatalf("block %d: expected status %s, got %s", number, status, blk.Status)
	}
}
