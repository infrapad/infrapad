package controller_test

// Focused orchestration checks for payload submission beyond the API
// lifecycle (server/test/e2e/payload_submission_test.go): classification
// of invalid extension output and of (not yet supported) downstream
// expansion, both rejected before any write.

import (
	"context"
	"errors"
	"testing"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/model"
)

func TestSubmitBlockPayloadRejections(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	doc, err := f.ctrl.CreateDocument(ctx, model.Document{Title: "submission"})
	testutil.NoErr(t, err)
	root, err := f.ctrl.AddBlock(ctx, doc.Uid, model.Block{Type: refTypeName, Content: []byte(`{"name":"root"}`)})
	testutil.NoErr(t, err)

	refreshed, err := f.ctrl.SubmitBlockPayload(ctx, doc.Uid, root.BlockNumber, []byte(`{"children":[]}`), "")
	testutil.NoErr(t, err)
	if len(refreshed.Blocks) != 1 {
		t.Fatalf("expected only the root block, got %+v", refreshed.Blocks)
	}
	revisions := len(f.store.blocks[doc.Uid])

	for _, tc := range []struct {
		name    string
		payload string
		reason  controller.SubmissionReason
	}{
		{"invalid payload", `{"bogus":1}`, controller.ReasonInvalidPayload},
		{"invalid output", `{"invalid":true}`, controller.ReasonInvalidOutput},
		// TODO(cascading-extension-expansion): replace with downstream
		// expansion, cycle/depth rejection, and rollback checks.
		{"downstream expansion", `{"children":[{"name":"a","payload":{}}]}`, controller.ReasonCascadingUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.ctrl.SubmitBlockPayload(ctx, doc.Uid, root.BlockNumber, []byte(tc.payload), "")
			var submission *controller.SubmissionError
			if !errors.As(err, &submission) || submission.Reason != tc.reason {
				t.Fatalf("expected %s, got %v", tc.reason, err)
			}
			if got := submission.ExpansionPath; len(got) != 1 || got[0] != root.BlockNumber {
				t.Fatalf("expected expansion path [%d], got %v", root.BlockNumber, got)
			}
			if got := len(f.store.blocks[doc.Uid]); got != revisions {
				t.Fatalf("rejected submission appended %d revisions", got-revisions)
			}
		})
	}
}
