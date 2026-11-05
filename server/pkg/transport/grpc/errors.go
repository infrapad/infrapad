package grpc

import (
	"errors"
	"fmt"
	"strconv"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
)

// errorDomain is the google.rpc.ErrorInfo domain of InfraPad errors.
const errorDomain = "infrapad.io"

// rpcError maps operation failures to gRPC statuses with standard error
// details: payload-submission rejections follow their classification
// (submissionError); forbidden edits of extension-managed blocks become
// permission-denied; invalid submitted content becomes invalid-argument
// with google.rpc.BadRequest field violations; missing documents/blocks
// become not-found; anything else is an unexpected server error. The HTTP
// gateway derives its status codes from the same mapping and exposes the
// details in the error body.
func rpcError(op string, err error) error {
	var submission *controller.SubmissionError
	if errors.As(err, &submission) {
		return submissionError(op, submission)
	}
	if controller.IsForbiddenEdit(err) {
		return status.Errorf(codes.PermissionDenied, "%s: %v", op, err)
	}
	if controller.IsInvalidInput(err) {
		var details []protoadapt.MessageV1
		var validation *content.ValidationError
		if errors.As(err, &validation) {
			details = append(details, badRequest(validation, "block.record"))
		}
		return withDetails(status.New(codes.InvalidArgument, fmt.Sprintf("%s: %v", op, err)), details...)
	}
	if controller.IsNotFound(err) {
		return status.Errorf(codes.NotFound, "%s: %v", op, err)
	}
	return status.Errorf(codes.Internal, "%s: %v", op, err)
}

// submissionCodes maps rejection reasons to gRPC codes. Unmapped reasons
// are treated as internal failures.
var submissionCodes = map[controller.SubmissionReason]codes.Code{
	controller.ReasonInvalidPayload:       codes.InvalidArgument,
	controller.ReasonDocumentNotFound:     codes.NotFound,
	controller.ReasonBlockNotFound:        codes.NotFound,
	controller.ReasonGeneratedBlock:       codes.PermissionDenied,
	controller.ReasonDeletedBlock:         codes.FailedPrecondition,
	controller.ReasonExpansionUnsupported: codes.FailedPrecondition,
	controller.ReasonCascadingUnsupported: codes.FailedPrecondition,
	controller.ReasonInvalidOutput:        codes.Internal,
	controller.ReasonExpansionFailed:      codes.Internal,
}

// submissionError builds the status of a rejected payload submission:
// google.rpc.ErrorInfo with the stable reason and available target
// context, plus google.rpc.BadRequest field violations from a wrapped
// validation error.
func submissionError(op string, e *controller.SubmissionError) error {
	code, ok := submissionCodes[e.Reason]
	if !ok {
		code = codes.Internal
	}
	metadata := map[string]string{
		"document":     "documents/" + string(e.DocumentUID),
		"block_number": strconv.Itoa(int(e.BlockNumber)),
	}
	if e.ContentType != "" {
		metadata["content_type"] = e.ContentType
	}
	if len(e.ExpansionPath) > 0 {
		metadata["expansion_path"] = e.Path()
	}
	details := []protoadapt.MessageV1{}
	if validation := e.Validation(); validation != nil {
		metadata["validation_stage"] = string(validation.Stage)
		if validation.ContentType != "" {
			metadata["validated_content_type"] = validation.ContentType
		}
		if len(validation.Location) > 0 {
			metadata["location"] = validation.Location.String()
		}
		prefix := "output"
		if e.Reason == controller.ReasonInvalidPayload {
			prefix = "payload"
		}
		details = append(details, badRequest(validation, prefix))
	}
	details = append([]protoadapt.MessageV1{&errdetails.ErrorInfo{
		Reason:   string(e.Reason),
		Domain:   errorDomain,
		Metadata: metadata,
	}}, details...)
	return withDetails(status.New(code, fmt.Sprintf("%s: %v", op, e)), details...)
}

// badRequest converts structured validation fields to field violations
// rooted at prefix (e.g. "payload.messages.0.ts"). Failures without field
// paths (host record checks) become a single violation of the prefix.
func badRequest(validation *content.ValidationError, prefix string) *errdetails.BadRequest {
	out := &errdetails.BadRequest{}
	for _, field := range validation.Fields {
		path := prefix
		if field.Path != "" {
			path += "." + field.Path
		}
		out.FieldViolations = append(out.FieldViolations, &errdetails.BadRequest_FieldViolation{
			Field:       path,
			Description: field.Message,
		})
	}
	if len(out.FieldViolations) == 0 {
		out.FieldViolations = append(out.FieldViolations, &errdetails.BadRequest_FieldViolation{
			Field:       prefix,
			Description: validation.Error(),
		})
	}
	return out
}

func withDetails(st *status.Status, details ...protoadapt.MessageV1) error {
	if len(details) == 0 {
		return st.Err()
	}
	withDetails, err := st.WithDetails(details...)
	if err != nil {
		return st.Err()
	}
	return withDetails.Err()
}
