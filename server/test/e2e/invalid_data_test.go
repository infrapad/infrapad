package e2e

// e2e capturing making requests with invalid data. It's currently known to not pass.

import (
	"context"
	"os"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
)

// skip skips the current (sub)test with an explanation, unless
// the FORCE_SKIPPED environment variable is set, in which case the test is
// allowed to run (and is expected to fail until the underlying issue is
// fixed).
func skip(t *testing.T) {
	t.Helper()
	if os.Getenv("FORCE_SKIPPED") == "" {
		t.Skip("known failing: server does not yet validate/translate this input " +
			"(see invalid_data_test.go); set FORCE_SKIPPED=1 to force run")
	}
}

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

// helperCreateDocument creates a valid document and returns its name, failing the test
// on error.
func helperCreateDocument(t *testing.T, client pb.InfrapadServiceClient) string {
	t.Helper()
	resp, err := client.CreateDocument(context.Background(), &pb.CreateDocumentRequest{
		Title: "helper document for invalid-data tests",
	})
	if err != nil {
		t.Fatalf("helperCreateDocument: %v", err)
	}
	return resp.GetDocument().GetName()
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
			Type:    "Core/Markdown",
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

	// Create a valid document + block for use in later sub-tests.
	validDocName := helperCreateDocument(t, client)
	validBlock := helperAddBlock(t, client, validDocName)

	// ===================================================================
	// CreateDoc
	// ===================================================================

	t.Run("CreateDocument/empty_title", func(t *testing.T) {
		skip(t)
		// title is REQUIRED — empty string should be rejected.
		_, err := client.CreateDocument(ctx, &pb.CreateDocumentRequest{Title: ""})
		requireCode(t, err, codes.InvalidArgument, "empty title")
	})

	// ===================================================================
	// GetDocument
	// ===================================================================

	t.Run("GetDocument/empty_name", func(t *testing.T) {
		skip(t)
		// name is REQUIRED.
		_, err := client.GetDocument(ctx, &pb.GetDocumentRequest{Name: ""})
		requireCode(t, err, codes.InvalidArgument, "empty name")
	})

	t.Run("GetDocument/nonexistent_name", func(t *testing.T) {
		skip(t)
		_, err := client.GetDocument(ctx, &pb.GetDocumentRequest{Name: "documents/does-not-exist-12345"})
		requireCode(t, err, codes.NotFound, "nonexistent name")
	})

	// ===================================================================
	// AddBlock
	// ===================================================================

	t.Run("AddBlock/empty_parent", func(t *testing.T) {
		skip(t)
		_, err := client.AddBlock(ctx, &pb.AddBlockRequest{
			Parent: "",
			Block: &pb.Block{
				Type:    "Core/Markdown",
				Content: validContent(t),
			},
		})
		requireCode(t, err, codes.InvalidArgument, "empty parent")
	})

	t.Run("AddBlock/nonexistent_parent", func(t *testing.T) {
		skip(t)
		_, err := client.AddBlock(ctx, &pb.AddBlockRequest{
			Parent: "documents/does-not-exist-12345",
			Block: &pb.Block{
				Type:    "Core/Markdown",
				Content: validContent(t),
			},
		})
		requireCode(t, err, codes.NotFound, "nonexistent parent")
	})

	t.Run("AddBlock/nil_block", func(t *testing.T) {
		skip(t)
		// block is REQUIRED.
		_, err := client.AddBlock(ctx, &pb.AddBlockRequest{
			Parent: validDocName,
			Block:  nil,
		})
		requireCode(t, err, codes.InvalidArgument, "nil block")
	})

	t.Run("AddBlock/empty_type", func(t *testing.T) {
		skip(t)
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
		skip(t)
		// content is REQUIRED.
		_, err := client.AddBlock(ctx, &pb.AddBlockRequest{
			Parent: validDocName,
			Block: &pb.Block{
				Type: "Core/Markdown",
			},
		})
		requireCode(t, err, codes.InvalidArgument, "no content")
	})

	// Content-type validation is enforced by the server: legacy type
	// names, unknown fields, and normalized-away field spellings are
	// rejected with InvalidArgument, with the offending field paths in
	// the status message.
	t.Run("AddBlock/invalid_content", func(t *testing.T) {
		cases := []struct {
			name     string
			typeName string
			content  map[string]any
			// messageSubstrings are expected offending field paths in the
			// status message (optional).
			messageSubstrings []string
		}{
			{
				name:     "legacy type name",
				typeName: "markdown",
				content:  map[string]any{"text": "x"},
			},
			{
				name:              "unknown content field",
				typeName:          "Core/Markdown",
				content:           map[string]any{"text": "x", "bogus": 1},
				messageSubstrings: []string{"bogus"},
			},
			{
				name:     "uppercase alert fields",
				typeName: "Prometheus/AlertsMatcher",
				content: map[string]any{
					"LabelsMatchers": []any{map[string]any{"alertname": []any{"x"}}},
					"Since":          "2026-08-14T09:00:00Z",
				},
				messageSubstrings: []string{"LabelsMatchers", "Since"},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				s, err := structpb.NewStruct(tc.content)
				if err != nil {
					t.Fatalf("structpb.NewStruct: %v", err)
				}
				_, err = client.AddBlock(ctx, &pb.AddBlockRequest{
					Parent: validDocName,
					Block: &pb.Block{
						Type:    tc.typeName,
						Content: s,
					},
				})
				requireCode(t, err, codes.InvalidArgument, tc.name)
				msg := status.Convert(err).Message()
				for _, want := range tc.messageSubstrings {
					if !strings.Contains(msg, want) {
						t.Errorf("error message lacks offending field path %q: %s", want, msg)
					}
				}
			})
		}
	})

	// ===================================================================
	// Provenance: host-owned immutable metadata. Public submissions can
	// never assign or change it; only the server-side host path assigns
	// parents when it creates generated children.
	// ===================================================================

	t.Run("AddBlock/public_parent_assignment", func(t *testing.T) {
		parent := int32(validBlock.GetBlockNumber())
		_, err := client.AddBlock(ctx, &pb.AddBlockRequest{
			Parent: validDocName,
			Block: &pb.Block{
				Type:              "Core/Markdown",
				Content:           validContent(t),
				ParentBlockNumber: &parent,
			},
		})
		requireCode(t, err, codes.InvalidArgument, "public parent assignment")
	})

	t.Run("AddBlock/zero_parent", func(t *testing.T) {
		zero := int32(0)
		_, err := client.AddBlock(ctx, &pb.AddBlockRequest{
			Parent: validDocName,
			Block: &pb.Block{
				Type:              "Core/Markdown",
				Content:           validContent(t),
				ParentBlockNumber: &zero,
			},
		})
		requireCode(t, err, codes.InvalidArgument, "zero parent")
	})

	t.Run("UpdateBlock/public_parent_change", func(t *testing.T) {
		wrong := validBlock.GetBlockNumber() + 1000
		_, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
			Parent:      validDocName,
			BlockNumber: validBlock.GetBlockNumber(),
			Block: &pb.Block{
				Type:              "Core/Markdown",
				Content:           validContent(t),
				ParentBlockNumber: &wrong,
			},
		})
		requireCode(t, err, codes.InvalidArgument, "public parent change")
	})

	t.Run("UpdateBlock/public_parent_assignment", func(t *testing.T) {
		// The valid block is a root: echoing any parent is an assignment
		// attempt and must be rejected.
		parent := int32(validBlock.GetBlockNumber())
		_, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
			Parent:      validDocName,
			BlockNumber: validBlock.GetBlockNumber(),
			Block: &pb.Block{
				Type:              "Core/Markdown",
				Content:           validContent(t),
				ParentBlockNumber: &parent,
			},
		})
		requireCode(t, err, codes.InvalidArgument, "public parent assignment on update")
	})

	// ===================================================================
	// UpdateBlock
	// ===================================================================

	t.Run("UpdateBlock/empty_parent", func(t *testing.T) {
		skip(t)
		_, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
			Parent:      "",
			BlockNumber: validBlock.GetBlockNumber(),
			Block: &pb.Block{
				Type:    "Core/Markdown",
				Content: validContent(t),
			},
		})
		requireCode(t, err, codes.InvalidArgument, "empty parent")
	})

	t.Run("UpdateBlock/nonexistent_parent", func(t *testing.T) {
		skip(t)
		_, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
			Parent:      "documents/does-not-exist-12345",
			BlockNumber: 1,
			Block: &pb.Block{
				Type:    "Core/Markdown",
				Content: validContent(t),
			},
		})
		requireCode(t, err, codes.NotFound, "nonexistent parent")
	})

	t.Run("UpdateBlock/zero_block_number", func(t *testing.T) {
		skip(t)
		// block_number is REQUIRED and must be > 0.
		_, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
			Parent:      validDocName,
			BlockNumber: 0,
			Block: &pb.Block{
				Type:    "Core/Markdown",
				Content: validContent(t),
			},
		})
		requireCode(t, err, codes.InvalidArgument, "zero block_number")
	})

	t.Run("UpdateBlock/nonexistent_block_number", func(t *testing.T) {
		skip(t)
		_, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
			Parent:      validDocName,
			BlockNumber: 99999,
			Block: &pb.Block{
				Type:    "Core/Markdown",
				Content: validContent(t),
			},
		})
		requireCode(t, err, codes.NotFound, "nonexistent block_number")
	})

	t.Run("UpdateBlock/nil_block", func(t *testing.T) {
		skip(t)
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
		skip(t)
		_, err := client.GetBlock(ctx, &pb.GetBlockRequest{
			Parent:      "",
			BlockNumber: 1,
		})
		requireCode(t, err, codes.InvalidArgument, "empty parent")
	})

	t.Run("GetBlock/nonexistent_parent", func(t *testing.T) {
		skip(t)
		_, err := client.GetBlock(ctx, &pb.GetBlockRequest{
			Parent:      "documents/does-not-exist-12345",
			BlockNumber: 1,
		})
		requireCode(t, err, codes.NotFound, "nonexistent parent")
	})

	t.Run("GetBlock/zero_block_number", func(t *testing.T) {
		skip(t)
		_, err := client.GetBlock(ctx, &pb.GetBlockRequest{
			Parent:      validDocName,
			BlockNumber: 0,
		})
		requireCode(t, err, codes.InvalidArgument, "zero block_number")
	})

	t.Run("GetBlock/nonexistent_block_number", func(t *testing.T) {
		skip(t)
		_, err := client.GetBlock(ctx, &pb.GetBlockRequest{
			Parent:      validDocName,
			BlockNumber: 99999,
		})
		requireCode(t, err, codes.NotFound, "nonexistent block_number")
	})

	t.Run("GetBlock/negative_revision", func(t *testing.T) {
		skip(t)
		_, err := client.GetBlock(ctx, &pb.GetBlockRequest{
			Parent:         validDocName,
			BlockNumber:    validBlock.GetBlockNumber(),
			RevisionNumber: -1,
		})
		requireCode(t, err, codes.InvalidArgument, "negative revision")
	})

	t.Run("GetBlock/nonexistent_revision", func(t *testing.T) {
		skip(t)
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
		skip(t)
		_, err := client.ListBlocks(ctx, &pb.ListBlocksRequest{Parent: ""})
		requireCode(t, err, codes.InvalidArgument, "empty parent")
	})

	t.Run("ListBlocks/nonexistent_parent", func(t *testing.T) {
		skip(t)
		_, err := client.ListBlocks(ctx, &pb.ListBlocksRequest{Parent: "documents/does-not-exist-12345"})
		requireCode(t, err, codes.NotFound, "nonexistent parent")
	})

	// ===================================================================
	// ListBlockHistory
	// ===================================================================

	t.Run("ListBlockHistory/empty_parent", func(t *testing.T) {
		skip(t)
		_, err := client.ListBlockHistory(ctx, &pb.ListBlockHistoryRequest{
			Parent:      "",
			BlockNumber: 1,
		})
		requireCode(t, err, codes.InvalidArgument, "empty parent")
	})

	t.Run("ListBlockHistory/nonexistent_parent", func(t *testing.T) {
		skip(t)
		_, err := client.ListBlockHistory(ctx, &pb.ListBlockHistoryRequest{
			Parent:      "documents/does-not-exist-12345",
			BlockNumber: 1,
		})
		requireCode(t, err, codes.NotFound, "nonexistent parent")
	})

	t.Run("ListBlockHistory/zero_block_number", func(t *testing.T) {
		skip(t)
		_, err := client.ListBlockHistory(ctx, &pb.ListBlockHistoryRequest{
			Parent:      validDocName,
			BlockNumber: 0,
		})
		requireCode(t, err, codes.InvalidArgument, "zero block_number")
	})

	t.Run("ListBlockHistory/nonexistent_block_number", func(t *testing.T) {
		skip(t)
		_, err := client.ListBlockHistory(ctx, &pb.ListBlockHistoryRequest{
			Parent:      validDocName,
			BlockNumber: 99999,
		})
		requireCode(t, err, codes.NotFound, "nonexistent block_number")
	})

	_ = ctx // suppress unused warning if all tests use it inline
}
