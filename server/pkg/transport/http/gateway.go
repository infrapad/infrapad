package http

import (
	"context"
	"fmt"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
)

// RegisterGatewayHandlers registers the generated gRPC-gateway HTTP handlers
// on mux using a shared loopback grpc.ClientConn. The conn lifetime is owned
// by the caller (Close), not the registration context.
func RegisterGatewayHandlers(ctx context.Context, mux *runtime.ServeMux, conn *grpc.ClientConn) error {
	if err := pb.RegisterInfrapadServiceHandler(ctx, mux, conn); err != nil {
		return fmt.Errorf("register infrapad gateway: %w", err)
	}
	return nil
}
