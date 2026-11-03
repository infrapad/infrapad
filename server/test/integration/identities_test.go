package integration

// Generated-block identity persistence against the real PostgreSQL store:
// grouped constituents and a keyed standalone reference survive internal
// host writes and reads/history, and a matched revision retains the block
// number while history keeps the previous membership.

import (
	"context"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/extensions/aiagent"
	"github.com/infrapad/infrapad/server/pkg/extensions/slack"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store/postgres"
)

func identityAt(minute int) time.Time {
	return time.Date(2026, 10, 25, 10, minute, 0, 0, time.UTC)
}

// TestGeneratedIdentityPersistenceRoundTrip covers the focused identity
// scenario: create grouped and standalone generated output through the
// host path, verify keys, memberships, timestamps, inner values, and
// provenance through reads, then match and append a revision removing the
// collection's first member while the block number stays stable and
// history retains the previous membership.
func TestGeneratedIdentityPersistenceRoundTrip(t *testing.T) {
	ctx := context.Background()

	db, err := postgres.OpenPG(testConnStr())
	testutil.NoErr(t, err)
	defer db.Close()

	agentType := aiagent.NewMessageType()
	conversationType := slack.NewConversationType()
	threadType := slack.NewThreadType(conversationType, agentType)
	registry, err := content.NewRegistry([]content.ContentType{agentType, threadType, conversationType})
	testutil.NoErr(t, err)
	ctrl := controller.New(postgres.NewStore(db), registry)

	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "generated identities"})
	testutil.NoErr(t, err)
	thread, err := ctrl.AddBlock(ctx, doc.Uid, model.Block{
		Type:    slack.ThreadTypeName,
		Content: []byte(`{"url":"https://slack.example/archives/C1/p1"}`),
	})
	testutil.NoErr(t, err)

	// Desired direct output of the thread: a conversation of three
	// messages and one keyed standalone agent reference.
	message := func(id, author string, minute int) content.KeyedRecord {
		return content.NewKeyedItem(conversationType, slack.MessageKey(id), identityAt(minute), slack.Message{Author: author, Text: "message " + id})
	}
	conversation, err := conversationType.NewCollection([]content.KeyedRecord{
		message("1", "Alice", 0), message("2", "Bob", 1), message("4", "Alice", 3),
	})
	testutil.NoErr(t, err)
	agentRecord := content.NewKeyedItem(agentType, slack.MessageKey("3"), identityAt(2), aiagent.Message{
		Agent:  "investigation-agent",
		Text:   "Node unhealthy.",
		Extras: map[string]any{"slack_message_id": "3"},
	})

	tx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	testutil.NoErr(t, err)
	convBlock, err := ctrl.CreateChildBlockTx(ctx, tx, doc.Uid, thread.BlockNumber, conversation, "host")
	testutil.NoErr(t, err)
	agentBlock, err := ctrl.CreateChildBlockTx(ctx, tx, doc.Uid, thread.BlockNumber, agentRecord, "host")
	testutil.NoErr(t, err)
	testutil.NoErr(t, tx.Commit())

	// Keys, complete memberships, timestamps, inner values, and provenance
	// survive the round-trip through document reads.
	gotDoc, err := ctrl.GetDocument(ctx, doc.Uid)
	testutil.NoErr(t, err)
	blocks := map[model.BlockNumber]model.Block{}
	for _, blk := range gotDoc.Blocks {
		blocks[blk.BlockNumber] = blk
	}
	gotConversation, ok := blocks[convBlock.BlockNumber]
	if !ok {
		t.Fatalf("conversation block %d missing", convBlock.BlockNumber)
	}
	if gotConversation.ParentBlockNumber == nil || *gotConversation.ParentBlockNumber != thread.BlockNumber {
		t.Fatalf("conversation provenance lost: %v", gotConversation.ParentBlockNumber)
	}
	view, err := content.CollectionView[slack.Message](gotConversation.Record)
	testutil.NoErr(t, err)
	if len(view.Items) != 3 {
		t.Fatalf("expected 3 members, got %d", len(view.Items))
	}
	for n, want := range []struct {
		key, author string
		minute      int
	}{
		{slack.MessageKey("1"), "Alice", 0},
		{slack.MessageKey("2"), "Bob", 1},
		{slack.MessageKey("4"), "Alice", 3},
	} {
		item := view.Items[n]
		if item.Key() != want.key || item.ContentValue.Author != want.author ||
			item.ContentValue.Text != "message "+want.key[len("slack-message:"):] ||
			!item.Timestamp().Equal(identityAt(want.minute)) {
			t.Fatalf("member %d not preserved: %#v", n, item)
		}
	}
	gotAgent, ok := blocks[agentBlock.BlockNumber]
	if !ok {
		t.Fatalf("agent block %d missing", agentBlock.BlockNumber)
	}
	if gotAgent.ParentBlockNumber == nil || *gotAgent.ParentBlockNumber != thread.BlockNumber {
		t.Fatalf("agent provenance lost: %v", gotAgent.ParentBlockNumber)
	}
	agentView, err := content.KeyedItemView[aiagent.Message](gotAgent.Record)
	testutil.NoErr(t, err)
	if agentView.Key() != slack.MessageKey("3") || agentView.ContentValue.Agent != "investigation-agent" ||
		agentView.ContentValue.Extras["slack_message_id"] != "3" || !agentView.Timestamp().Equal(identityAt(2)) {
		t.Fatalf("agent record not preserved: %#v", agentView)
	}

	// Matching nominates the conversation block through its earliest
	// surviving member after the first member is dropped from the desired
	// output.
	revised, err := conversationType.NewCollection([]content.KeyedRecord{message("2", "Bob", 1), message("4", "Alice", 3)})
	testutil.NoErr(t, err)
	// The caller scopes the input to this parent's latest direct children.
	children := []model.Block{gotConversation, gotAgent}
	matches := controller.MatchGeneratedOutput(children, []content.Record{revised})
	if len(matches.Matches) != 1 || matches.Matches[0].BlockNumber != convBlock.BlockNumber ||
		len(matches.UnmatchedRecords) != 0 || len(matches.UnmatchedBlocks) != 1 {
		t.Fatalf("unexpected match result: %+v", matches)
	}
	if matches.UnmatchedBlocks[0].BlockNumber != agentBlock.BlockNumber {
		t.Fatalf("expected the agent block unmatched, got %d", matches.UnmatchedBlocks[0].BlockNumber)
	}

	// The revision keeps the block number, moves the anchor, and history
	// retains the previous membership.
	tx, err = ctrl.BeginHostTx(ctx, doc.Uid)
	testutil.NoErr(t, err)
	revision, err := ctrl.AppendGeneratedRevisionTx(ctx, tx, doc.Uid, convBlock.BlockNumber, revised, "host")
	testutil.NoErr(t, err)
	testutil.NoErr(t, tx.Commit())
	if revision.BlockNumber != convBlock.BlockNumber || revision.RevisionNumber != 2 {
		t.Fatalf("unexpected revision %d/%d", revision.BlockNumber, revision.RevisionNumber)
	}
	latest, err := ctrl.GetBlock(ctx, doc.Uid, convBlock.BlockNumber, 0)
	testutil.NoErr(t, err)
	latestView, err := content.CollectionView[slack.Message](latest.Record)
	testutil.NoErr(t, err)
	if len(latestView.Items) != 2 || latestView.Items[0].Key() != slack.MessageKey("2") ||
		!latestView.Timestamp().Equal(identityAt(1)) {
		t.Fatalf("unexpected revised membership: %#v", latestView.Items)
	}
	history, err := ctrl.ListBlockHistory(ctx, doc.Uid, convBlock.BlockNumber)
	testutil.NoErr(t, err)
	if len(history) != 2 {
		t.Fatalf("expected 2 revisions, got %d", len(history))
	}
	firstView, err := content.CollectionView[slack.Message](history[0].Record)
	testutil.NoErr(t, err)
	if len(firstView.Items) != 3 || firstView.Items[0].Key() != slack.MessageKey("1") {
		t.Fatalf("history lost the previous membership: %#v", firstView.Items)
	}

	// Illegal identity changes are rejected as invalid input and roll back
	// without new revisions.
	revisionsBefore := len(history)
	failTx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	testutil.NoErr(t, err)
	if _, err := ctrl.AppendGeneratedRevisionTx(ctx, failTx, doc.Uid, agentBlock.BlockNumber,
		content.NewKeyedItem(agentType, slack.MessageKey("other"), identityAt(2), aiagent.Message{Agent: "a", Text: "b"}), "host"); err == nil || !controller.IsInvalidInput(err) {
		t.Fatalf("expected invalid input for a reference key change, got: %v", err)
	}
	failTx.Rollback()
	history, err = ctrl.ListBlockHistory(ctx, doc.Uid, convBlock.BlockNumber)
	testutil.NoErr(t, err)
	if len(history) != revisionsBefore {
		t.Fatalf("rejected revision changed history: %d != %d", len(history), revisionsBefore)
	}
}
