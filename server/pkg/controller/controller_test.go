package controller_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
	"github.com/infrapad/infrapad/server/pkg/extensions/prometheus"
	"github.com/infrapad/infrapad/server/pkg/model"
)

// TestRecordPersistenceRoundTrip checks that a submitted inner content value
// is persisted as a full host record envelope and restored losslessly.
func TestRecordPersistenceRoundTrip(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	ctrl := f.ctrl
	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "incident"})
	testutil.NoErr(t, err)

	blk, err := ctrl.AddBlock(ctx, doc.Uid, model.Block{
		Type:    prometheus.AlertsMatcherTypeName,
		Content: json.RawMessage(`{"labels_matchers":[{"alertname":["CrashLoopBackOff"]}],"since":"2026-10-25T10:00:00Z"}`),
	})
	testutil.NoErr(t, err)
	if _, ok := blk.Record.(content.ItemRecord); !ok {
		t.Fatal("created block carries no item record")
	}
	// The persisted envelope is the full host record, not the flat inner content.
	var envelope map[string]any
	testutil.NoErr(t, json.Unmarshal(blk.SerializedRecord.Data, &envelope))
	if envelope["timestamp"] == nil || envelope["content"] == nil {
		t.Fatalf("serialized record is not a host record envelope: %v", envelope)
	}
	if blk.SerializedRecord.Type != prometheus.AlertsMatcherTypeName || blk.Type != prometheus.AlertsMatcherTypeName {
		t.Fatalf("unexpected type association: %q / %q", blk.SerializedRecord.Type, blk.Type)
	}

	fetched, err := ctrl.GetBlock(ctx, doc.Uid, blk.BlockNumber, 0)
	testutil.NoErr(t, err)
	view, err := content.ItemView[prometheus.AlertsMatcher](fetched.Record)
	testutil.NoErr(t, err)
	if len(view.ContentValue.LabelsMatchers) != 1 ||
		view.ContentValue.LabelsMatchers[0]["alertname"][0] != "CrashLoopBackOff" {
		t.Fatalf("unexpected restored content: %#v", view.ContentValue)
	}
	if !view.ContentValue.Since.Equal(time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected since: %v", view.ContentValue.Since)
	}
}

// TestTimestampPreservationAcrossRevisions checks that an ordinary block's
// record timestamp is initialized from block creation time and preserved
// on updates.
func TestTimestampPreservationAcrossRevisions(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	ctrl := f.ctrl
	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "incident"})
	testutil.NoErr(t, err)
	before := time.Now().UTC()
	blk, err := ctrl.AddBlock(ctx, doc.Uid, model.Block{
		Type:    core.MarkdownTypeName,
		Content: json.RawMessage(`{"text":"first"}`),
	})
	testutil.NoErr(t, err)
	created := blk.Record.Timestamp()
	if created.Before(before.Add(-time.Minute)) || created.After(time.Now().UTC().Add(time.Minute)) {
		t.Fatalf("record timestamp not initialized from creation time: %v", created)
	}

	updated, err := ctrl.UpdateBlock(ctx, doc.Uid, blk.BlockNumber, model.Block{
		Type:    core.MarkdownTypeName,
		Content: json.RawMessage(`{"text":"second"}`),
	})
	testutil.NoErr(t, err)
	if !updated.Record.Timestamp().Equal(created) {
		t.Fatalf("record timestamp not preserved: %v != %v", updated.Record.Timestamp(), created)
	}
	view, err := content.ItemView[core.Markdown](updated.Record)
	testutil.NoErr(t, err)
	if view.ContentValue.Text != "second" {
		t.Fatalf("unexpected updated content: %#v", view.ContentValue)
	}
}

// TestStrictInputRejection checks that legacy type names, normalized-away
// field spellings, unknown fields, and missing required fields are rejected
// as invalid input without changing the document.
func TestStrictInputRejection(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	ctrl := f.ctrl
	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "incident"})
	testutil.NoErr(t, err)

	cases := []struct {
		name     string
		typeName string
		content  string
	}{
		{"legacy markdown type", "markdown", `{"text":"x"}`},
		{"legacy alerts_matcher type", "alerts_matcher", `{"labels_matchers":[],"since":"2026-10-25T10:00:00Z"}`},
		{"unregistered type", "Some/Unknown", `{"text":"x"}`},
		{"uppercase alert fields", prometheus.AlertsMatcherTypeName, `{"LabelsMatchers":[{"alertname":["x"]}],"Since":"2026-10-25T10:00:00Z"}`},
		{"missing required since", prometheus.AlertsMatcherTypeName, `{"labels_matchers":[{"alertname":["x"]}]}`},
		{"unknown field", core.MarkdownTypeName, `{"text":"x","bogus":1}`},
		{"missing required text", core.MarkdownTypeName, `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ctrl.AddBlock(ctx, doc.Uid, model.Block{
				Type:    tc.typeName,
				Content: json.RawMessage(tc.content),
			})
			if err == nil {
				t.Fatal("expected rejection")
			}
			if !controller.IsInvalidInput(err) {
				t.Fatalf("expected invalid input, got: %v", err)
			}
			blocks, err := ctrl.ListBlocks(ctx, doc.Uid)
			testutil.NoErr(t, err)
			if len(blocks) != 0 {
				t.Fatalf("rejected submission changed the document: %d blocks", len(blocks))
			}
		})
	}
}
