package grpc

import (
	"encoding/json"
	"reflect"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
)

func TestSubmissionReasonMapping(t *testing.T) {
	for _, tc := range []struct {
		reason controller.SubmissionReason
		code   codes.Code
	}{
		{controller.ReasonInvalidPayload, codes.InvalidArgument},
		{controller.ReasonDocumentNotFound, codes.NotFound},
		{controller.ReasonBlockNotFound, codes.NotFound},
		{controller.ReasonGeneratedBlock, codes.PermissionDenied},
		{controller.ReasonDeletedBlock, codes.FailedPrecondition},
		{controller.ReasonExpansionUnsupported, codes.FailedPrecondition},
		{controller.ReasonExpansionCycle, codes.FailedPrecondition},
		{controller.ReasonExpansionDepthExceeded, codes.FailedPrecondition},
		{controller.ReasonInvalidOutput, codes.Internal},
		{controller.ReasonExpansionFailed, codes.Internal},
		{"UNKNOWN_REASON", codes.Internal},
	} {
		t.Run(string(tc.reason), func(t *testing.T) {
			// The same validation error can describe producer input or an
			// extension's downstream payload. The reason, not the wrapped
			// validation stage, determines its status and field prefix.
			path := []controller.ExpansionPathStep{
				{BlockNumber: 1},
				{ReferenceKey: "a/b", BlockNumber: 3},
				{ReferenceKey: "4"},
			}
			err := rpcError("submit payload", &controller.SubmissionError{
				Reason: tc.reason, DocumentUID: "doc-1", ExpansionPath: path,
				Err: &content.ValidationError{
					Stage:  content.StagePayload,
					Fields: []content.FieldError{{Path: "messages", Message: "invalid messages"}},
				},
			})
			st := status.Convert(err)
			if st.Code() != tc.code {
				t.Fatalf("expected %s, got %s: %v", tc.code, st.Code(), err)
			}
			details := st.Details()
			if len(details) != 2 {
				t.Fatalf("expected ErrorInfo and BadRequest, got %v", details)
			}
			info, ok := details[0].(*errdetails.ErrorInfo)
			if !ok || info.GetReason() != string(tc.reason) {
				t.Fatalf("expected reason %s, got %v", tc.reason, details[0])
			}
			if got := info.GetMetadata()["expansion_path"]; got != "1/a%2Fb/4" {
				t.Fatalf("unexpected rendered path %q", got)
			}
			var steps []controller.ExpansionPathStep
			if err := json.Unmarshal([]byte(info.GetMetadata()["expansion_path_steps"]), &steps); err != nil {
				t.Fatalf("decode path steps: %v", err)
			}
			if !reflect.DeepEqual(steps, path) {
				t.Fatalf("expected structured path %v, got %v", path, steps)
			}
			field := "output.messages"
			if tc.reason == controller.ReasonInvalidPayload {
				field = "payload.messages"
			}
			bad, ok := details[1].(*errdetails.BadRequest)
			if !ok || len(bad.GetFieldViolations()) != 1 || bad.GetFieldViolations()[0].GetField() != field {
				t.Fatalf("expected field %s, got %v", field, details[1])
			}
		})
	}
}
