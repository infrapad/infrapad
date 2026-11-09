package integration

// Markdown push atomicity against the real PostgreSQL store: a push whose
// edits and creations are already written fails while rendering the
// refreshed document, and the whole push (revisions and new blocks) rolls
// back. The controller fake store's rollback is a no-op.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store/postgres"
)

func TestMarkdownPushRollback(t *testing.T) {
	ctx := context.Background()
	db, err := postgres.OpenPG(testConnStr())
	testutil.NoErr(t, err)
	defer db.Close()

	registry, err := content.NewRegistry(core.ContentTypes(), []content.ContentType{newFragileType()})
	testutil.NoErr(t, err)
	ctrl := controller.New(postgres.NewStore(db), registry)

	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "markdown push rollback", Blocks: []model.Block{
		{Type: core.MarkdownTypeName, Content: []byte(`{"text":"notes"}`)},
		{Type: fragileTypeName, Content: []byte(`{"text":"fine"}`)},
	}})
	testutil.NoErr(t, err)
	exported, err := ctrl.ExportDocumentMarkdown(ctx, doc.Uid)
	testutil.NoErr(t, err)

	// Edit both blocks and add one; the fragile block's edit is valid but
	// cannot be rendered afterwards.
	edited := strings.Replace(exported, "\nnotes\n", "\nnotes (edited)\n", 1)
	edited = strings.Replace(edited, "\nfine\n", "\nboom\n", 1)
	edited += "::infrapad_block{block=new type=Core/Markdown}\nadded\n"
	_, err = ctrl.PushDocumentMarkdown(ctx, doc.Uid, edited, "")
	var pushErr *controller.MarkdownPushError
	if !errors.As(err, &pushErr) || pushErr.Reason != controller.PushRenderFailed {
		t.Fatalf("expected a render failure, got %v", err)
	}

	blocks, err := ctrl.ListBlocks(ctx, doc.Uid)
	testutil.NoErr(t, err)
	if len(blocks) != 2 {
		t.Fatalf("expected the new block to roll back, got %d blocks", len(blocks))
	}
	for _, blk := range blocks {
		history, err := ctrl.ListBlockHistory(ctx, doc.Uid, blk.BlockNumber)
		testutil.NoErr(t, err)
		if len(history) != 1 {
			t.Fatalf("block %d: expected the revision to roll back, got %d revisions", blk.BlockNumber, len(history))
		}
	}
	after, err := ctrl.ExportDocumentMarkdown(ctx, doc.Uid)
	testutil.NoErr(t, err)
	if after != exported {
		t.Fatalf("document changed after a failed push:\n%s", after)
	}

	// Without the unrenderable edit, the same push commits atomically.
	result, err := ctrl.PushDocumentMarkdown(ctx, doc.Uid, strings.Replace(edited, "\nboom\n", "\nfine\n", 1), "")
	testutil.NoErr(t, err)
	if got := fmt.Sprint(result.Changes); got != "[{1 updated} {3 added}]" {
		t.Fatalf("unexpected changes: %s", got)
	}
}

// Test/Fragile is an editable test type whose rendering fails for the
// text "boom".
const fragileTypeName = "Test/Fragile"

type fragile struct {
	Text string `json:"text"`
}

type fragileType struct {
	content.TypedMarkdownRenderer[content.Item[fragile]]
}

func newFragileType() *fragileType {
	return &fragileType{TypedMarkdownRenderer: content.ItemMarkdown(func(item content.Item[fragile]) (string, error) {
		if item.ContentValue.Text == "boom" {
			return "", errors.New("cannot render boom")
		}
		return item.ContentValue.Text, nil
	})}
}

func (*fragileType) Name() string { return fragileTypeName }

func (*fragileType) ContentSchema() content.Schema {
	return content.Schema{Source: []byte(`#Fragile: {text?: string}`), Definition: "#Fragile"}
}

func (*fragileType) DecodeJSON(data json.RawMessage) (content.Content, error) {
	return content.DecodeContent[fragile](data)
}

func (*fragileType) UpdateMarkdown(existing content.Record, body string) (content.Record, error) {
	return content.WithContent(existing, fragile{Text: body})
}
