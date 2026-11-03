package controller_test

// Generated-block identity contract tests: the pure membership matcher and
// the record-preserving internal write helpers, using the Slack/agent types
// of the shared fixture. Full persistence round-trips live in the integration tests.

import (
	"context"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/extensions/aiagent"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
	"github.com/infrapad/infrapad/server/pkg/extensions/slack"
	"github.com/infrapad/infrapad/server/pkg/model"
)

var identityTS = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

// TestMembershipMatcher covers the focused matching contracts: anchor
// removal, splits, collision resolution with no fallback, type isolation,
// deleted-block exclusion, and standalone one-member records.
func TestMembershipMatcher(t *testing.T) {
	f := newFixture(t)
	conversation := func(keys ...string) content.Record {
		return conversationRecord(t, f.conversationType, keys...)
	}
	agent := func(keys ...string) content.Record {
		return content.NewKeyedItem(f.agentType, keys[0], identityTS, aiagent.Message{Agent: "agent", Text: "message " + keys[0]})
	}

	cases := []struct {
		name string
		// old blocks of the matched parent (number -> keys)
		old              map[model.BlockNumber][]string
		deleted          map[model.BlockNumber]bool
		oldType          func(keys ...string) content.Record
		desired          []content.Record
		matches          []model.BlockNumber
		unmatchedBlocks  []model.BlockNumber
		unmatchedRecords int
	}{
		{
			name:    "anchor removal retains the block",
			old:     map[model.BlockNumber][]string{7: {"a", "b", "c"}},
			oldType: conversation,
			desired: []content.Record{conversation("b", "c")},
			matches: []model.BlockNumber{7},
		},
		{
			name:    "split follows the earliest surviving member",
			old:     map[model.BlockNumber][]string{7: {"a", "b", "c"}},
			oldType: conversation,
			desired: []content.Record{conversation("a"), conversation("b", "c")},
			matches: []model.BlockNumber{7}, unmatchedRecords: 1,
		},
		{
			name:    "collision resolved by lower block number without fallback",
			old:     map[model.BlockNumber][]string{7: {"a", "b"}, 9: {"c", "d"}},
			oldType: conversation,
			desired: []content.Record{conversation("b", "c", "d")},
			matches: []model.BlockNumber{7}, unmatchedBlocks: []model.BlockNumber{9},
		},
		{
			name:            "type isolation",
			old:             map[model.BlockNumber][]string{7: {"a"}},
			oldType:         agent,
			desired:         []content.Record{conversation("a")},
			unmatchedBlocks: []model.BlockNumber{7}, unmatchedRecords: 1,
		},
		{
			name:             "deleted blocks are not candidates",
			old:              map[model.BlockNumber][]string{7: {"a"}},
			deleted:          map[model.BlockNumber]bool{7: true},
			oldType:          conversation,
			desired:          []content.Record{conversation("a")},
			unmatchedRecords: 1,
		},
		{
			name:    "standalone reference is a one-member record",
			old:     map[model.BlockNumber][]string{5: {"m"}},
			oldType: agent,
			desired: []content.Record{agent("m"), conversation("z")},
			matches: []model.BlockNumber{5}, unmatchedRecords: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var blocks []model.Block
			for number, keys := range tc.old {
				var status model.BlockStatus = model.PublishedBlock
				if tc.deleted[number] {
					status = model.DeletedBlock
				}
				blocks = append(blocks, generatedBlock(number, 1, status, tc.oldType(keys...)))
			}

			result := controller.MatchGeneratedOutput(blocks, tc.desired)
			if len(result.Matches) != len(tc.matches) {
				t.Fatalf("expected %d matches, got %d (%+v)", len(tc.matches), len(result.Matches), result.Matches)
			}
			for n, want := range tc.matches {
				if result.Matches[n].BlockNumber != want {
					t.Fatalf("match %d: expected block %d, got %d", n, want, result.Matches[n].BlockNumber)
				}
			}
			if len(result.UnmatchedBlocks) != len(tc.unmatchedBlocks) {
				t.Fatalf("expected %d unmatched blocks, got %d", len(tc.unmatchedBlocks), len(result.UnmatchedBlocks))
			}
			for n, want := range tc.unmatchedBlocks {
				if result.UnmatchedBlocks[n].BlockNumber != want {
					t.Fatalf("unmatched block %d: expected %d, got %d", n, want, result.UnmatchedBlocks[n].BlockNumber)
				}
			}
			if len(result.UnmatchedRecords) != tc.unmatchedRecords {
				t.Fatalf("expected %d unmatched records, got %d", tc.unmatchedRecords, len(result.UnmatchedRecords))
			}
		})
	}
}

// TestGeneratedWriteRejection covers the write boundaries: keyed records
// required, duplicate identities within a parent's live output (including
// across content types), illegal identity changes on revisions, and
// rollback leaving stored revisions unchanged.
func TestGeneratedWriteRejection(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	ctrl := f.ctrl
	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "identities"})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	thread, err := ctrl.AddBlock(ctx, doc.Uid, model.Block{
		Type:    slack.ThreadTypeName,
		Content: []byte(`{"url":"https://slack.example/archives/C1/p1"}`),
	})
	if err != nil {
		t.Fatalf("add thread block: %v", err)
	}

	// Generated children require keyed records.
	unkeyed := content.NewItem(f.conversationType, identityTS, slack.Message{Author: "a", Text: "b"})
	tx, err := ctrl.BeginHostTx(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("begin host tx: %v", err)
	}
	if _, err := ctrl.CreateChildBlockTx(ctx, tx, doc.Uid, thread.BlockNumber, unkeyed, "host"); err == nil || !controller.IsInvalidInput(err) {
		t.Fatalf("expected invalid input for an unkeyed record, got: %v", err)
	}
	tx.Rollback()

	// Valid creation of a conversation and a keyed standalone reference.
	tx, err = ctrl.BeginHostTx(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("begin host tx: %v", err)
	}
	defer tx.Rollback()
	convRecord := conversationRecord(t, f.conversationType, "m1", "m2")
	convBlock, err := ctrl.CreateChildBlockTx(ctx, tx, doc.Uid, thread.BlockNumber, convRecord, "host")
	if err != nil {
		t.Fatalf("create conversation child: %v", err)
	}
	agent := content.NewKeyedItem(f.agentType, "m3", identityTS, aiagent.Message{Agent: "agent", Text: "message m3"})
	if _, err := ctrl.CreateChildBlockTx(ctx, tx, doc.Uid, thread.BlockNumber, agent, "host"); err != nil {
		t.Fatalf("create agent child: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit host tx: %v", err)
	}
	revisionsBefore := len(f.store.blocks[doc.Uid])

	// Duplicate keys are rejected within the parent's live output, both
	// for the same content type and across types.
	for name, record := range map[string]content.Record{
		"same type":  conversationRecord(t, f.conversationType, "m1", "m4"),
		"other type": content.NewKeyedItem(f.agentType, "m1", identityTS, aiagent.Message{Agent: "agent", Text: "message m1"}),
	} {
		tx, err := ctrl.BeginHostTx(ctx, doc.Uid)
		if err != nil {
			t.Fatalf("begin host tx (%s): %v", name, err)
		}
		if _, err := ctrl.CreateChildBlockTx(ctx, tx, doc.Uid, thread.BlockNumber, record, "host"); err == nil || !controller.IsInvalidInput(err) {
			t.Fatalf("%s: expected invalid input for a duplicate key, got: %v", name, err)
		}
		tx.Rollback()
	}

	// Revisions keep the content type and standalone keys.
	for name, record := range map[string]content.Record{
		"type change":          content.NewKeyedItem(f.markdownType, "m2", identityTS, core.Markdown{Text: "edited"}),
		"reference key change": content.NewKeyedItem(f.agentType, "m9", identityTS, aiagent.Message{Agent: "agent", Text: "message m9"}),
		"sibling collision":    conversationRecord(t, f.conversationType, "m2", "m3"),
	} {
		number := convBlock.BlockNumber
		if name == "reference key change" {
			number = convBlock.BlockNumber + 1
		}
		tx, err := ctrl.BeginHostTx(ctx, doc.Uid)
		if err != nil {
			t.Fatalf("begin host tx (%s): %v", name, err)
		}
		if _, err := ctrl.AppendGeneratedRevisionTx(ctx, tx, doc.Uid, number, record, "host"); err == nil || !controller.IsInvalidInput(err) {
			t.Fatalf("%s: expected invalid input, got: %v", name, err)
		}
		tx.Rollback()
	}

	// Rejected operations committed nothing.
	if got := len(f.store.blocks[doc.Uid]); got != revisionsBefore {
		t.Fatalf("rejected operations changed stored revisions: %d != %d", got, revisionsBefore)
	}

	// A collection revision may change membership (and the derived anchor)
	// while the block number stays stable.
	revised := conversationRecord(t, f.conversationType, "m2")
	tx, err = ctrl.BeginHostTx(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("begin host tx: %v", err)
	}
	revision, err := ctrl.AppendGeneratedRevisionTx(ctx, tx, doc.Uid, convBlock.BlockNumber, revised, "host")
	if err != nil {
		t.Fatalf("append generated revision: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit host tx: %v", err)
	}
	if revision.BlockNumber != convBlock.BlockNumber || revision.RevisionNumber != 2 {
		t.Fatalf("unexpected revision %d/%d", revision.BlockNumber, revision.RevisionNumber)
	}
	if keys := memberKeys(t, revision.Record); len(keys) != 1 || keys[0] != "m2" {
		t.Fatalf("unexpected revised membership: %v", keys)
	}
}

// conversationRecord builds a collection record from message keys,
// timestamps increasing with position.
func conversationRecord(t *testing.T, conversationType *slack.ConversationType, keys ...string) content.Record {
	t.Helper()
	items := make([]content.KeyedRecord, len(keys))
	for n, key := range keys {
		items[n] = content.NewKeyedItem(conversationType, key, at(n), slack.Message{Author: "author-" + key, Text: "message " + key})
	}
	collection, err := conversationType.NewCollection(items)
	if err != nil {
		t.Fatalf("build conversation: %v", err)
	}
	return collection
}

// generatedBlock wraps a record into a latest-revision model block.
func generatedBlock(number, parent model.BlockNumber, status model.BlockStatus, record content.Record) model.Block {
	return model.Block{
		BlockNumber:       number,
		ParentBlockNumber: &parent,
		Status:            status,
		Type:              record.ContentType().Name(),
		Record:            record,
	}
}

func memberKeys(t *testing.T, record content.Record) []string {
	t.Helper()
	var keys []string
	for _, member := range content.Members(record) {
		keys = append(keys, member.Key())
	}
	return keys
}

// at returns a fixed timestamp offset by minutes, for ordering-relevant
// record timestamps.
func at(minute int) time.Time {
	return time.Date(2026, 10, 25, 10, minute, 0, 0, time.UTC)
}
