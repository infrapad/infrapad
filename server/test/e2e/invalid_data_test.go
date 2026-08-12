package e2e

// e2e capturing making requests with invalid data. It's currently known to not pass.

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
)

// requireCode fails the test if the error does not carry the expected gRPC
// status code.  It also fails when err is nil but a non-OK code is expected.
func requireCode(t *testing.T, err error, want codes.Code, msgAndArgs ...interface{}) {
	t.Helper()
	got := codes.OK
	if err != nil {
		got = status.Code(err)
	}
	if got != want {
		label := ""
		if len(msgAndArgs) > 0 {
			label = msgAndArgs[0].(string) + ": "
		}
		t.Errorf("%sexpected code %s, got %s (err: %v)", label, want, got, err)
	}
}

// helperCreateDoc creates a valid doc and returns its UID, failing the test
// on error.
func helperCreateDoc(t *testing.T, client pb.InfrapadServiceClient) string {
	t.Helper()
	resp, err := client.CreateDoc(context.Background(), &pb.CreateDocRequest{
		Title: "helper doc for invalid-data tests",
	})
	if err != nil {
		t.Fatalf("helperCreateDoc: %v", err)
	}
	return resp.GetDoc().GetName()
}

// validContent returns a simple valid content struct for testing.
func validContent(t *testing.T) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(map[string]any{"text": "valid block"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	return s
}

// helperAddBlock adds a valid markdown block and returns it.
func helperAddBlock(t *testing.T, client pb.InfrapadServiceClient, docName string) *pb.Block {
	t.Helper()
	resp, err := client.AddBlock(context.Background(), &pb.AddBlockRequest{
		Parent: docName,
		Block: &pb.Block{
			Type:    "markdown",
			Content: validContent(t),
		},
	})
	if err != nil {
		t.Fatalf("helperAddBlock: %v", err)
	}
	return resp.GetBlock()
}

func TestInvalidData(t *testing.T) {
	ctx := context.Background()

	conn, err := grpc.NewClient(grpcAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	defer conn.Close()

	client := pb.NewInfrapadServiceClient(conn)

	// Create a valid doc + block for use in later sub-tests.
	validDocName := helperCreateDoc(t, client)
	validBlock := helperAddBlock(t, client, validDocName)

	// ===================================================================
	// CreateDoc
	// ===================================================================

	t.Run("CreateDoc/empty_title", func(t *testing.T) {
		// title is REQUIRED — empty string should be rejected.
		_, err := client.CreateDoc(ctx, &pb.CreateDocRequest{Title: ""})
		requireCode(t, err, codes.InvalidArgument, "empty title")
	})

	// ===================================================================
	// GetDoc
	// ===================================================================

	t.Run("GetDoc/empty_name", func(t *testing.T) {
		// name is REQUIRED.
		_, err := client.GetDoc(ctx, &pb.GetDocRequest{Name: ""})
		requireCode(t, err, codes.InvalidArgument, "empty name")
	})

	t.Run("GetDoc/nonexistent_name", func(t *testing.T) {
		_, err := client.GetDoc(ctx, &pb.GetDocRequest{Name: "docs/does-not-exist-12345"})
		requireCode(t, err, codes.NotFound, "nonexistent name")
	})

	// ===================================================================
	// AddBlock
	// ===================================================================

	t.Run("AddBlock/empty_parent", func(t *testing.T) {
		_, err := client.AddBlock(ctx, &pb.AddBlockRequest{
			Parent: "",
			Block: &pb.Block{
				Type:    "markdown",
				Content: validContent(t),
			},
		})
		requireCode(t, err, codes.InvalidArgument, "empty parent")
	})

	t.Run("AddBlock/nonexistent_parent", func(t *testing.T) {
		_, err := client.AddBlock(ctx, &pb.AddBlockRequest{
			Parent: "docs/does-not-exist-12345",
			Block: &pb.Block{
				Type:    "markdown",
				Content: validContent(t),
			},
		})
		requireCode(t, err, codes.NotFound, "nonexistent parent")
	})

	t.Run("AddBlock/nil_block", func(t *testing.T) {
		// block is REQUIRED.
		_, err := client.AddBlock(ctx, &pb.AddBlockRequest{
			Parent: validDocName,
			Block:  nil,
		})
		requireCode(t, err, codes.InvalidArgument, "nil block")
	})

	t.Run("AddBlock/empty_type", func(t *testing.T) {
		// block.type is REQUIRED.
		_, err := client.AddBlock(ctx, &pb.AddBlockRequest{
			Parent: validDocName,
			Block: &pb.Block{
				Type:    "",
				Content: validContent(t),
			},
		})
		requireCode(t, err, codes.InvalidArgument, "empty block type")
	})

	t.Run("AddBlock/no_content", func(t *testing.T) {
		// content is REQUIRED.
		_, err := client.AddBlock(ctx, &pb.AddBlockRequest{
			Parent: validDocName,
			Block: &pb.Block{
				Type: "markdown",
			},
		})
		requireCode(t, err, codes.InvalidArgument, "no content")
	})

	// ===================================================================
	// UpdateBlock
	// ===================================================================

	t.Run("UpdateBlock/empty_parent", func(t *testing.T) {
		_, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
			Parent:      "",
			BlockNumber: validBlock.GetBlockNumber(),
			Block: &pb.Block{
				Type:    "markdown",
				Content: validContent(t),
			},
		})
		requireCode(t, err, codes.InvalidArgument, "empty parent")
	})

	t.Run("UpdateBlock/nonexistent_parent", func(t *testing.T) {
		_, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
			Parent:      "docs/does-not-exist-12345",
			BlockNumber: 1,
			Block: &pb.Block{
				Type:    "markdown",
				Content: validContent(t),
			},
		})
		requireCode(t, err, codes.NotFound, "nonexistent parent")
	})

	t.Run("UpdateBlock/zero_block_number", func(t *testing.T) {
		// block_number is REQUIRED and must be > 0.
		_, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
			Parent:      validDocName,
			BlockNumber: 0,
			Block: &pb.Block{
				Type:    "markdown",
				Content: validContent(t),
			},
		})
		requireCode(t, err, codes.InvalidArgument, "zero block_number")
	})

	t.Run("UpdateBlock/nonexistent_block_number", func(t *testing.T) {
		_, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
			Parent:      validDocName,
			BlockNumber: 99999,
			Block: &pb.Block{
				Type:    "markdown",
				Content: validContent(t),
			},
		})
		requireCode(t, err, codes.NotFound, "nonexistent block_number")
	})

	t.Run("UpdateBlock/nil_block", func(t *testing.T) {
		_, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
			Parent:      validDocName,
			BlockNumber: validBlock.GetBlockNumber(),
			Block:       nil,
		})
		requireCode(t, err, codes.InvalidArgument, "nil block")
	})

	// ===================================================================
	// GetBlock
	// ===================================================================

	t.Run("GetBlock/empty_parent", func(t *testing.T) {
		_, err := client.GetBlock(ctx, &pb.GetBlockRequest{
			Parent:      "",
			BlockNumber: 1,
		})
		requireCode(t, err, codes.InvalidArgument, "empty parent")
	})

	t.Run("GetBlock/nonexistent_parent", func(t *testing.T) {
		_, err := client.GetBlock(ctx, &pb.GetBlockRequest{
			Parent:      "docs/does-not-exist-12345",
			BlockNumber: 1,
		})
		requireCode(t, err, codes.NotFound, "nonexistent parent")
	})

	t.Run("GetBlock/zero_block_number", func(t *testing.T) {
		_, err := client.GetBlock(ctx, &pb.GetBlockRequest{
			Parent:      validDocName,
			BlockNumber: 0,
		})
		requireCode(t, err, codes.InvalidArgument, "zero block_number")
	})

	t.Run("GetBlock/nonexistent_block_number", func(t *testing.T) {
		_, err := client.GetBlock(ctx, &pb.GetBlockRequest{
			Parent:      validDocName,
			BlockNumber: 99999,
		})
		requireCode(t, err, codes.NotFound, "nonexistent block_number")
	})

	t.Run("GetBlock/negative_revision", func(t *testing.T) {
		_, err := client.GetBlock(ctx, &pb.GetBlockRequest{
			Parent:         validDocName,
			BlockNumber:    validBlock.GetBlockNumber(),
			RevisionNumber: -1,
		})
		requireCode(t, err, codes.InvalidArgument, "negative revision")
	})

	t.Run("GetBlock/nonexistent_revision", func(t *testing.T) {
		_, err := client.GetBlock(ctx, &pb.GetBlockRequest{
			Parent:         validDocName,
			BlockNumber:    validBlock.GetBlockNumber(),
			RevisionNumber: 99999,
		})
		requireCode(t, err, codes.NotFound, "nonexistent revision")
	})

	// ===================================================================
	// ListBlocks
	// ===================================================================

	t.Run("ListBlocks/empty_parent", func(t *testing.T) {
		_, err := client.ListBlocks(ctx, &pb.ListBlocksRequest{Parent: ""})
		requireCode(t, err, codes.InvalidArgument, "empty parent")
	})

	t.Run("ListBlocks/nonexistent_parent", func(t *testing.T) {
		_, err := client.ListBlocks(ctx, &pb.ListBlocksRequest{Parent: "docs/does-not-exist-12345"})
		requireCode(t, err, codes.NotFound, "nonexistent parent")
	})

	// ===================================================================
	// ListBlockHistory
	// ===================================================================

	t.Run("ListBlockHistory/empty_parent", func(t *testing.T) {
		_, err := client.ListBlockHistory(ctx, &pb.ListBlockHistoryRequest{
			Parent:      "",
			BlockNumber: 1,
		})
		requireCode(t, err, codes.InvalidArgument, "empty parent")
	})

	t.Run("ListBlockHistory/nonexistent_parent", func(t *testing.T) {
		_, err := client.ListBlockHistory(ctx, &pb.ListBlockHistoryRequest{
			Parent:      "docs/does-not-exist-12345",
			BlockNumber: 1,
		})
		requireCode(t, err, codes.NotFound, "nonexistent parent")
	})

	t.Run("ListBlockHistory/zero_block_number", func(t *testing.T) {
		_, err := client.ListBlockHistory(ctx, &pb.ListBlockHistoryRequest{
			Parent:      validDocName,
			BlockNumber: 0,
		})
		requireCode(t, err, codes.InvalidArgument, "zero block_number")
	})

	t.Run("ListBlockHistory/nonexistent_block_number", func(t *testing.T) {
		_, err := client.ListBlockHistory(ctx, &pb.ListBlockHistoryRequest{
			Parent:      validDocName,
			BlockNumber: 99999,
		})
		requireCode(t, err, codes.NotFound, "nonexistent block_number")
	})

	_ = ctx // suppress unused warning if all tests use it inline
}
