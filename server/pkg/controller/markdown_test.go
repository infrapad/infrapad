package controller_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/infrapad/infrapad/server/internal/testext"
	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/model"
)

// TestExportDocumentMarkdownLiveBlocks checks that export renders only
// latest live blocks (generated children included) and never block status.
func TestExportDocumentMarkdownLiveBlocks(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	doc, err := f.ctrl.CreateDocument(ctx, model.Document{Title: "export"})
	testutil.NoErr(t, err)
	root, err := f.ctrl.AddBlock(ctx, doc.Uid, model.Block{Type: testext.RefTypeName, Content: []byte(`{"name":"root"}`)})
	testutil.NoErr(t, err)
	_, err = f.ctrl.SubmitBlockPayload(ctx, doc.Uid, root.BlockNumber, []byte(`{"children":[{"name":"a"}]}`), "")
	testutil.NoErr(t, err)
	child := childByKey(t, f, doc.Uid, root.BlockNumber, "a")
	childDirective := fmt.Sprintf("::infrapad_block{block=%d rev=1 type=Test/Ref parent=%d ", child.BlockNumber, root.BlockNumber)

	md, err := f.ctrl.ExportDocumentMarkdown(ctx, doc.Uid)
	testutil.NoErr(t, err)
	if !strings.Contains(md, childDirective) || !strings.Contains(md, "\na\n") {
		t.Fatalf("expected the generated child in export:\n%s", md)
	}

	// Removing the child from the snapshot deletes it; refreshed and
	// loaded documents and block lists omit it before Markdown rendering.
	refreshed, err := f.ctrl.SubmitBlockPayload(ctx, doc.Uid, root.BlockNumber, []byte(`{"children":[]}`), "")
	testutil.NoErr(t, err)
	loaded, err := f.ctrl.GetDocument(ctx, doc.Uid)
	testutil.NoErr(t, err)
	listed, err := f.ctrl.ListBlocks(ctx, doc.Uid)
	testutil.NoErr(t, err)
	for _, blocks := range [][]model.Block{refreshed.Blocks, loaded.Blocks, listed} {
		if len(blocks) != 1 || blocks[0].BlockNumber != root.BlockNumber {
			t.Fatalf("expected only the live root block, got %v", blocks)
		}
	}
	md, err = f.ctrl.ExportDocumentMarkdown(ctx, doc.Uid)
	testutil.NoErr(t, err)
	rootDirective := fmt.Sprintf("::infrapad_block{block=%d rev=1 type=Test/Ref timestamp=", root.BlockNumber)
	if !strings.Contains(md, rootDirective) || strings.Contains(md, fmt.Sprintf("block=%d ", child.BlockNumber)) {
		t.Fatalf("expected only the live root block:\n%s", md)
	}
	if strings.Contains(md, "status=") || strings.Contains(md, "deleted") {
		t.Fatalf("block status must not be exported:\n%s", md)
	}

	if _, err := f.ctrl.ExportDocumentMarkdown(ctx, "missing"); !controller.IsNotFound(err) {
		t.Fatalf("expected not found for a missing document, got %v", err)
	}
}
