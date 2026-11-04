package controller_test

// Chronological ordering: latest-block views sorted by event time then
// block number, and the member-ordering helper sorting by event time then
// key, using the Slack/agent types of the shared fixture.

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/aiagent"
	"github.com/infrapad/infrapad/server/pkg/extensions/slack"
	"github.com/infrapad/infrapad/server/pkg/model"
)

// TestChronologicalBlockOrdering checks that GetDocument and ListBlocks
// order blocks by event time rather than block number, resolve equal times
// by block number, and use a collection's first-member time.
func TestChronologicalBlockOrdering(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	doc, err := f.ctrl.CreateDocument(ctx, model.Document{Title: "ordering"})
	testutil.NoErr(t, err)

	// Block 1: ordinary block, event time = creation time (now).
	thread, err := f.ctrl.AddBlock(ctx, doc.Uid, model.Block{
		Type:    slack.ThreadTypeName,
		Content: []byte(`{"url":"https://slack.example/archives/C1/p1788873542000000"}`),
	})
	testutil.NoErr(t, err)

	// Blocks 2-4: generated children with past, extension-supplied times.
	baseTime := time.Now().UTC().Add(-time.Hour)
	past := func(minute int) time.Time {
		return baseTime.Add(time.Duration(minute) * time.Minute)
	}
	tx, err := f.ctrl.BeginHostTx(ctx, doc.Uid)
	testutil.NoErr(t, err)
	for _, record := range []content.Record{
		// The collection uses c's timestamp, even though a occurred earlier.
		conversationAt(t, f, member{"c", past(5)}, member{"a", past(1)}),
		agentAt(f, "early", past(2)),
		agentAt(f, "tie", past(5)),
	} {
		_, err := f.ctrl.CreateChildBlockTx(ctx, tx, doc.Uid, thread.BlockNumber, record, "host")
		testutil.NoErr(t, err)
	}
	testutil.NoErr(t, tx.Commit())

	// Block 5: ordinary block created last.
	_, err = f.ctrl.AddBlock(ctx, doc.Uid, markdownBlock("writeup"))
	testutil.NoErr(t, err)

	want := []model.BlockNumber{3, 2, 4, 1, 5}
	listed, err := f.ctrl.ListBlocks(ctx, doc.Uid)
	testutil.NoErr(t, err)
	if got := blockNumbers(listed); !slices.Equal(got, want) {
		t.Fatalf("ListBlocks order: got %v, want %v", got, want)
	}
	fetched, err := f.ctrl.GetDocument(ctx, doc.Uid)
	testutil.NoErr(t, err)
	if got := blockNumbers(fetched.Blocks); !slices.Equal(got, want) {
		t.Fatalf("GetDocument order: got %v, want %v", got, want)
	}
}

// TestMemberOrdering checks that equal-time members order by key and that
// the source collection keeps its persisted member order.
func TestMemberOrdering(t *testing.T) {
	f := newFixture(t)
	source := conversationAt(t, f, member{"c", at(0)}, member{"b", at(0)}, member{"a", at(5)})

	if got := keysOf(content.OrderMembers(source)); !slices.Equal(got, []string{"b", "c", "a"}) {
		t.Fatalf("ordered members: got %v", got)
	}
	if got := memberKeys(t, source); !slices.Equal(got, []string{"c", "b", "a"}) {
		t.Fatalf("source collection was reordered: %v", got)
	}
}

type member struct {
	key string
	ts  time.Time
}

// conversationAt builds a conversation collection keeping the given
// member order and event times.
func conversationAt(t *testing.T, f *fixture, members ...member) content.Record {
	t.Helper()
	items := make([]content.KeyedRecord, len(members))
	for n, m := range members {
		items[n] = content.NewKeyedItem(f.conversationType, m.key, m.ts, slack.Message{Author: "author", Text: m.key})
	}
	collection, err := f.conversationType.NewCollection(items)
	testutil.NoErr(t, err)
	return collection
}

func agentAt(f *fixture, key string, ts time.Time) content.Record {
	return content.NewKeyedItem(f.agentType, key, ts, aiagent.Message{Agent: "agent", Text: key})
}

func blockNumbers(blocks []model.Block) []model.BlockNumber {
	numbers := make([]model.BlockNumber, len(blocks))
	for n, blk := range blocks {
		numbers[n] = blk.BlockNumber
	}
	return numbers
}

func keysOf(records []content.KeyedRecord) []string {
	keys := make([]string, len(records))
	for n, record := range records {
		keys[n] = record.Key()
	}
	return keys
}
