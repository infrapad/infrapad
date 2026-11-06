package controller_test

// Focused orchestration checks for payload submission beyond the lifecycle
// tests (expansion_test.go, server/test/e2e/payload_submission_test.go):
// classification of invalid producer input and invalid extension output,
// rejected before any write.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/infrapad/infrapad/server/internal/testext"
	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/model"
)

func TestSubmitBlockPayloadRejections(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	doc, err := f.ctrl.CreateDocument(ctx, model.Document{Title: "submission"})
	testutil.NoErr(t, err)
	root, err := f.ctrl.AddBlock(ctx, doc.Uid, model.Block{Type: testext.RefTypeName, Content: []byte(`{"name":"root"}`)})
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
		// A malformed downstream payload is invalid extension output, not
		// invalid producer input, and carries the reference's path context.
		{"invalid downstream payload", `{"children":[{"name":"a","payload":{"bogus":1}}]}`, controller.ReasonInvalidOutput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.ctrl.SubmitBlockPayload(ctx, doc.Uid, root.BlockNumber, []byte(tc.payload), "")
			var submission *controller.SubmissionError
			if !errors.As(err, &submission) || submission.Reason != tc.reason {
				t.Fatalf("expected %s, got %v", tc.reason, err)
			}
			if got := len(f.store.blocks[doc.Uid]); got != revisions {
				t.Fatalf("rejected submission appended %d revisions", got-revisions)
			}
		})
	}
}

// TestSubmitBlockPayloadDownstreamPath checks that reference keys locate
// a downstream failure regardless of cached state. Known block numbers
// supplement keys, and slashes within keys remain unambiguous.
func TestSubmitBlockPayloadDownstreamPath(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	doc, err := f.ctrl.CreateDocument(ctx, model.Document{Title: "downstream path"})
	testutil.NoErr(t, err)
	root, err := f.ctrl.AddBlock(ctx, doc.Uid, model.Block{Type: testext.RefTypeName, Content: []byte(`{"name":"root"}`)})
	testutil.NoErr(t, err)

	for _, cached := range []bool{false, true} {
		var childNumber model.BlockNumber
		if cached {
			_, err := f.ctrl.SubmitBlockPayload(ctx, doc.Uid, root.BlockNumber,
				[]byte(`{"children":[{"name":"a"}]}`), "")
			testutil.NoErr(t, err)
			childNumber = childByKey(t, f, doc.Uid, root.BlockNumber, "a").BlockNumber
		}
		before := len(f.store.blocks[doc.Uid])
		_, err := f.ctrl.SubmitBlockPayload(ctx, doc.Uid, root.BlockNumber,
			[]byte(`{"children":[{"name":"a","payload":{"children":[{"name":"b/c","payload":{"bogus":1}}]}}]}`), "")
		var submission *controller.SubmissionError
		if !errors.As(err, &submission) || submission.Reason != controller.ReasonInvalidOutput {
			t.Fatalf("expected %s, got %v", controller.ReasonInvalidOutput, err)
		}
		want := []controller.ExpansionPathStep{
			{BlockNumber: root.BlockNumber},
			{ReferenceKey: "a", BlockNumber: childNumber},
			{ReferenceKey: "b/c"},
		}
		if !reflect.DeepEqual(submission.ExpansionPath, want) {
			t.Fatalf("cached=%t: expected expansion path %v, got %v", cached, want, submission.ExpansionPath)
		}
		if path := submission.Path(); path != fmt.Sprintf("%d/a/b%%2Fc", root.BlockNumber) {
			t.Fatalf("cached=%t: unexpected rendered path %q", cached, path)
		}
		if got := len(f.store.blocks[doc.Uid]); got != before {
			t.Fatalf("rejected submission appended %d revisions", got-before)
		}
	}
}
