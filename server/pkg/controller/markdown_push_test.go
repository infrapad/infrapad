package controller_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/infrapad/infrapad/server/internal/testext"
	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/model"
)

// TestPushDocumentMarkdownBaselines checks edit detection against the
// referenced revisions: untouched stale and generated blocks are skipped,
// while edited stale blocks conflict and edited generated blocks are
// forbidden, rejecting the whole push before any write.
func TestPushDocumentMarkdownBaselines(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	doc, err := f.ctrl.CreateDocument(ctx, model.Document{Title: "push", Blocks: []model.Block{
		{Type: "Core/Markdown", Content: []byte(`{"text":"one"}`)},
		{Type: "Core/Markdown", Content: []byte(`{"text":"two"}`)},
	}})
	testutil.NoErr(t, err)
	root, err := f.ctrl.AddBlock(ctx, doc.Uid, model.Block{Type: testext.RefTypeName, Content: []byte(`{"name":"root"}`)})
	testutil.NoErr(t, err)
	_, err = f.ctrl.SubmitBlockPayload(ctx, doc.Uid, root.BlockNumber, []byte(`{"children":[{"name":"child"}]}`), "")
	testutil.NoErr(t, err)
	child := childByKey(t, f, doc.Uid, root.BlockNumber, "child")

	exported, err := f.ctrl.ExportDocumentMarkdown(ctx, doc.Uid)
	testutil.NoErr(t, err)
	// Block 2 changes concurrently: the exported Markdown is now stale for it.
	_, err = f.ctrl.UpdateBlock(ctx, doc.Uid, 2, model.Block{Type: "Core/Markdown", Content: []byte(`{"text":"two (server)"}`)})
	testutil.NoErr(t, err)

	push := func(md string) (controller.MarkdownPushResult, error) {
		return f.ctrl.PushDocumentMarkdown(ctx, doc.Uid, md, "")
	}
	reason := func(err error) controller.MarkdownPushReason {
		var pushErr *controller.MarkdownPushError
		if !errors.As(err, &pushErr) {
			t.Fatalf("expected a MarkdownPushError, got %v", err)
		}
		return pushErr.Reason
	}

	// An edited stale block conflicts; the edit of block 1 is not applied.
	editedBoth := strings.Replace(strings.Replace(exported, "\none\n", "\none (local)\n", 1), "\ntwo\n", "\ntwo (local)\n", 1)
	if _, err := push(editedBoth); reason(err) != controller.PushStaleRevision {
		t.Fatalf("expected a stale revision conflict, got %v", err)
	}
	// An edited generated block is forbidden, regardless of capabilities.
	editedChild := strings.Replace(exported, "\nchild\n", "\nchild (local)\n", 1)
	if _, err := push(editedChild); reason(err) != controller.PushGeneratedBlock {
		t.Fatalf("expected a generated-block rejection, got %v", err)
	}
	if history, err := f.ctrl.ListBlockHistory(ctx, doc.Uid, 1); err != nil || len(history) != 1 {
		t.Fatalf("rejected pushes must not write: %v %v", history, err)
	}

	// Untouched stale (block 2) and generated blocks are skipped; only the
	// genuinely edited block is revised.
	result, err := push(strings.Replace(exported, "\none\n", "\none (local)\n", 1))
	testutil.NoErr(t, err)
	if len(result.Changes) != 1 || result.Changes[0] != (controller.MarkdownChange{BlockNumber: 1, Action: controller.MarkdownBlockUpdated}) {
		t.Fatalf("unexpected changes: %+v", result.Changes)
	}
	for _, want := range []string{"rev=2 type=Core/Markdown", "one (local)\n", "two (server)\n", fmt.Sprintf("block=%d rev=1", child.BlockNumber)} {
		if !strings.Contains(result.Markdown, want) {
			t.Fatalf("expected %q in refreshed Markdown:\n%s", want, result.Markdown)
		}
	}

	// Pushing the refreshed Markdown back is a no-op.
	again, err := push(result.Markdown)
	testutil.NoErr(t, err)
	if len(again.Changes) != 0 || again.Markdown != result.Markdown {
		t.Fatalf("expected a no-op push, got %+v", again.Changes)
	}
}
