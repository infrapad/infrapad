package integration

// Snapshot reconciliation against the real PostgreSQL store: a
// cross-primary regrouping commits atomically, and rolling back after a
// successful reconciliation leaves every revision unchanged.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/extensions/aiagent"
	"github.com/infrapad/infrapad/server/pkg/extensions/slack"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store/postgres"
)

func TestSnapshotReconciliationTransactions(t *testing.T) {
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

	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "snapshot reconciliation"})
	testutil.NoErr(t, err)
	addThread := func(ts string) model.Block {
		blk, err := ctrl.AddBlock(ctx, doc.Uid, model.Block{Type: slack.ThreadTypeName, Content: []byte(`{"url":"https://slack.example/archives/C1/p` + strings.ReplaceAll(ts, ".", "") + `"}`)})
		testutil.NoErr(t, err)
		return blk
	}
	t1 := addThread("1788873542.000000")
	t2 := addThread("1788873542.000100")

	// reconcile expands, validates, and reconciles one thread snapshot in
	// its own host transaction, returning it for the caller to finish.
	reconcile := func(thread model.Block, messages ...slack.SourceMessage) (controller.ReconcileResult, func() error, func() error) {
		t.Helper()
		payload, err := json.Marshal(slack.Snapshot{Messages: append([]slack.SourceMessage{}, messages...)})
		testutil.NoErr(t, err)
		testutil.NoErr(t, registry.ValidatePayload(slack.ThreadTypeName, payload))
		result, err := threadType.Expand(ctx, thread.Record, payload)
		testutil.NoErr(t, err)
		testutil.NoErr(t, registry.ValidateExpansion(thread.Record, result))
		tx, err := ctrl.BeginHostTx(ctx, doc.Uid)
		testutil.NoErr(t, err)
		res, err := ctrl.ReconcileExpansionTx(ctx, tx, doc.Uid, thread.BlockNumber, result, "host")
		if err != nil {
			tx.Rollback()
			t.Fatalf("reconcile: %v", err)
		}
		return res, tx.Commit, tx.Rollback
	}
	// slackBase aligns Slack timestamps with the identityAt wall clock.
	slackBase := identityAt(0).Unix()
	message := func(id string, minute, second int) slack.SourceMessage {
		return slack.SourceMessage{
			Ts:       slackMessageTS(slackBase, minute, second, 0),
			User:     "U" + id,
			Text:     "message " + id,
			UserName: "author-" + id,
		}
	}
	// revisionCounts returns the number of revisions per block.
	revisionCounts := func() map[model.BlockNumber]int {
		blocks, err := ctrl.ListBlocks(ctx, doc.Uid)
		testutil.NoErr(t, err)
		counts := map[model.BlockNumber]int{}
		for _, blk := range blocks {
			history, err := ctrl.ListBlockHistory(ctx, doc.Uid, blk.BlockNumber)
			testutil.NoErr(t, err)
			counts[blk.BlockNumber] = len(history)
		}
		return counts
	}

	res, commit, _ := reconcile(t1, message("1", 0, 0), message("2", 1, 0), message("3", 2, 0))
	testutil.NoErr(t, commit())
	conversation := res.Output[slack.MessageKey(slackMessageTS(slackBase, 0, 0, 0))]

	// Thread 2's late event splits thread 1's conversation: both
	// primaries' changes commit together.
	res, commit, _ = reconcile(t2, message("x", 1, 30))
	testutil.NoErr(t, commit())
	if len(res.Revisions) != 3 { // revision of the conversation + two new blocks
		t.Fatalf("expected 3 revisions, got %d", len(res.Revisions))
	}
	latest, err := ctrl.GetBlock(ctx, doc.Uid, conversation, 0)
	testutil.NoErr(t, err)
	if keys := content.Members(latest.Record); len(keys) != 2 || keys[1].Key() != slack.MessageKey(slackMessageTS(slackBase, 1, 0, 0)) || latest.RevisionNumber != 2 {
		t.Fatalf("conversation not split atomically: revision %d, %d members", latest.RevisionNumber, len(keys))
	}

	// A successful reconciliation rolled back leaves all revisions
	// unchanged.
	before := revisionCounts()
	res, _, rollback := reconcile(t2)
	if len(res.Revisions) == 0 {
		t.Fatal("expected the removal to produce revisions before rollback")
	}
	testutil.NoErr(t, rollback())
	after := revisionCounts()
	if len(after) != len(before) {
		t.Fatalf("rollback changed blocks: %v -> %v", before, after)
	}
	for number, count := range before {
		if after[number] != count {
			t.Fatalf("rollback changed revisions of block %d: %d -> %d", number, count, after[number])
		}
	}
}

// slackMessageTS formats a realistic Slack message timestamp aligned with
// the identityAt wall clock of the fixture.
func slackMessageTS(base int64, minute, second, micro int) string {
	return fmt.Sprintf("%d.%06d", base+int64(minute)*60+int64(second), micro)
}
