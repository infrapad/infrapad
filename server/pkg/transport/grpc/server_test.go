package grpc

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/infrapad/infrapad/server/pkg/controller"
)

func TestForbiddenEditMapping(t *testing.T) {
	err := rpcError("update block", fmt.Errorf("wrapped: %w", &controller.ForbiddenEditError{
		DocumentUID:       "doc-1",
		BlockNumber:       2,
		ParentBlockNumber: 1,
	}))
	if code := status.Code(err); code != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied, got %s: %v", code, err)
	}
	if code := runtime.HTTPStatusFromCode(status.Code(err)); code != http.StatusForbidden {
		t.Fatalf("expected HTTP 403, got %d", code)
	}
}
