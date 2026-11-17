package controller_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
	"github.com/infrapad/infrapad/server/pkg/extensions/loki"
	"github.com/infrapad/infrapad/server/pkg/extensions/prometheus"
	"github.com/infrapad/infrapad/server/pkg/model"
)

// Both snapshot types use explicit content timestamps or the original
// server creation time, never query bounds or results. Exercise JSON and
// Markdown writes, revisions, history, and document ordering together.
func TestContentEventTimestamp(t *testing.T) {
	for _, typ := range []string{loki.LogsTypeName, prometheus.TimeSeriesChartTypeName} {
		t.Run(typ, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t)
			doc, err := f.ctrl.CreateDocument(ctx, model.Document{Title: "content event timestamps"})
			testutil.NoErr(t, err)
			explicit, err := time.Parse(time.RFC3339Nano, "2026-11-12T09:00:00.123456789Z")
			testutil.NoErr(t, err)
			inner := map[string]any{
				"timestamp": explicit,
				"query": map[string]any{"expression": "up", "start": "2026-11-13T10:00:00Z",
					"end": "2026-11-13T11:00:00Z"},
			}
			setResults := func(minute int) {
				seconds := 1794564000 + minute*60
				if typ == loki.LogsTypeName {
					inner["query"].(map[string]any)["limit"] = 10
					inner["query_results"] = map[string]any{"streams": []any{map[string]any{
						"labels": map[string]string{}, "entries": []any{map[string]any{
							"timestamp": fmt.Sprintf("%d000000000", seconds), "line": "x"}}}}}
				} else {
					inner["query"].(map[string]any)["step_seconds"] = 60
					inner["query_results"] = map[string]any{"series": []any{map[string]any{
						"labels": map[string]string{}, "samples": []any{[]any{seconds, nil}}}}}
				}
			}
			block := func() model.Block {
				data, err := json.Marshal(inner)
				testutil.NoErr(t, err)
				return model.Block{Type: typ, Content: data}
			}
			expect := func(b model.Block, revision model.RevisionNumber, timestamp time.Time) {
				t.Helper()
				if b.RevisionNumber != revision || !b.Record.Timestamp().Equal(timestamp) {
					t.Fatalf("want revision %d at %s, got revision %d at %s", revision, timestamp, b.RevisionNumber, b.Record.Timestamp())
				}
			}
			setResults(0)
			created, err := f.ctrl.AddBlock(ctx, doc.Uid, block())
			testutil.NoErr(t, err)
			expect(created, 1, explicit) // explicit times may be outside the query window
			update := func(echo *time.Time) model.Block {
				t.Helper()
				input := block()
				input.Timestamp = echo
				b, err := f.ctrl.UpdateBlock(ctx, doc.Uid, created.BlockNumber, input)
				testutil.NoErr(t, err)
				return b
			}
			setResults(10)
			firstRefresh := update(&explicit)
			expect(firstRefresh, 1, explicit) // refresh in place; unchanged envelope echo allowed
			forged := block()
			forged.Timestamp = &created.CreatedAt
			if _, err := f.ctrl.UpdateBlock(ctx, doc.Uid, created.BlockNumber, forged); err == nil {
				t.Fatal("accepted a changed host-envelope timestamp")
			}

			// Clearing restores the original creation time, not result/query time.
			inner["timestamp"] = nil
			cleared := update(nil)
			expect(cleared, 2, created.CreatedAt)
			historical, err := f.ctrl.GetBlock(ctx, doc.Uid, created.BlockNumber, 1)
			testutil.NoErr(t, err)
			expect(historical, 1, explicit)
			if !content.EqualRecords(historical.Record, firstRefresh.Record) {
				t.Fatal("clearing timestamp changed the earlier revision's snapshot")
			}
			// Pinning even the current effective time changes metadata and creates a revision.
			inner["timestamp"] = created.CreatedAt
			expect(update(nil), 3, created.CreatedAt)
			delete(inner, "timestamp")
			expect(update(nil), 4, created.CreatedAt)
			setResults(20)
			refreshed := update(nil)
			expect(refreshed, 4, created.CreatedAt)

			export := func() string {
				t.Helper()
				md, err := f.ctrl.ExportDocumentMarkdown(ctx, doc.Uid)
				testutil.NoErr(t, err)
				return md
			}
			push := func(md string) model.Block {
				t.Helper()
				_, err := f.ctrl.PushDocumentMarkdown(ctx, doc.Uid, md, "")
				testutil.NoErr(t, err)
				b, err := f.ctrl.GetBlock(ctx, doc.Uid, created.BlockNumber, 0)
				testutil.NoErr(t, err)
				return b
			}
			// Markdown timestamp edits must preserve hidden snapshot results.
			expect(push(strings.Replace(export(), "content:\n", "content:\n  timestamp: \""+explicit.Format(time.RFC3339Nano)+"\"\n", 1)), 5, explicit)
			expect(push(export()), 5, explicit)
			md := strings.Replace(export(), "  timestamp: \""+explicit.Format(time.RFC3339Nano)+"\"\n", "", 1)
			cleared = push(md)
			expect(cleared, 6, created.CreatedAt)
			if !content.EqualRecords(refreshed.Record, cleared.Record) {
				t.Fatal("Markdown timestamp edits changed snapshot content")
			}

			// JSON and Markdown creation without a timestamp use server creation time.
			unpinned, err := f.ctrl.AddBlock(ctx, doc.Uid, block())
			testutil.NoErr(t, err)
			expect(unpinned, 1, unpinned.CreatedAt)
			unchanged, err := f.ctrl.UpdateBlock(ctx, doc.Uid, unpinned.BlockNumber, block())
			testutil.NoErr(t, err)
			expect(unchanged, 1, unpinned.CreatedAt) // no first-refresh timestamp drift
			queryParameter := "limit: 10"
			if typ == prometheus.TimeSeriesChartTypeName {
				queryParameter = "step_seconds: 60"
			}
			push(export() + "\n::infrapad_block{block=new type=" + typ + "}\n```yaml infrapad\ncontent:\n  query:\n" +
				"    expression: up\n    start: \"2026-11-13T10:00:00Z\"\n    end: \"2026-11-13T11:00:00Z\"\n    " + queryParameter + "\n```\n")
			fromMarkdown, err := f.ctrl.GetBlock(ctx, doc.Uid, unpinned.BlockNumber+1, 0)
			testutil.NoErr(t, err)
			expect(fromMarkdown, 1, fromMarkdown.CreatedAt)

			_, err = f.ctrl.AddBlock(ctx, doc.Uid, model.Block{Type: core.MarkdownTypeName, Content: []byte(`{"text":"now"}`)})
			testutil.NoErr(t, err)
			// Reposition the first block after the others; API and Markdown must agree.
			later := time.Now().UTC().Add(time.Hour)
			inner["timestamp"] = later
			expect(update(nil), 7, later)
			blocks, err := f.ctrl.ListBlocks(ctx, doc.Uid)
			testutil.NoErr(t, err)
			if len(blocks) != 4 || blocks[3].BlockNumber != created.BlockNumber {
				t.Fatalf("expected explicitly placed block last, got %v", blocks)
			}
			exported := export()
			position := -1
			for _, b := range blocks {
				next := strings.Index(exported, fmt.Sprintf("::infrapad_block{block=%d ", b.BlockNumber))
				if next <= position {
					t.Fatal("API and Markdown block order differ")
				}
				position = next
			}
			// Invalid timestamps fail, rather than silently falling back.
			for _, invalid := range []string{"not-a-time", "0001-01-01T00:00:00Z"} {
				inner["timestamp"] = invalid
				if _, err := f.ctrl.UpdateBlock(ctx, doc.Uid, created.BlockNumber, block()); err == nil {
					t.Fatalf("accepted invalid timestamp %q", invalid)
				}
			}
		})
	}
}
