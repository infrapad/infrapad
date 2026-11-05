package e2e

// Invalid-input and forbidden-operation API checks. Known failing cases
// are individually skipped until their validation/status mapping is implemented.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
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

// validRecord returns a simple valid public record for testing.
func validRecord(t *testing.T) *structpb.Struct {
	t.Helper()
	return contentRecord(t, map[string]any{"text": "valid block"})
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
				Type:   "Core/Markdown",
				Record: validRecord(t),
			},
		})
		requireCode(t, err, codes.InvalidArgument, "empty parent")
	})

	t.Run("AddBlock/nonexistent_parent", func(t *testing.T) {
		skip(t)
		_, err := client.AddBlock(ctx, &pb.AddBlockRequest{
			Parent: "documents/does-not-exist-12345",
			Block: &pb.Block{
				Type:   "Core/Markdown",
				Record: validRecord(t),
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
				Type:   "",
				Record: validRecord(t),
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
				_, err := client.AddBlock(ctx, &pb.AddBlockRequest{
					Parent: validDocName,
					Block: &pb.Block{
						Type:   tc.typeName,
						Record: mustStruct(t, map[string]any{"content": tc.content}),
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

	t.Run("AddBlock/public_key_assignment", func(t *testing.T) {
		_, err := client.AddBlock(ctx, &pb.AddBlockRequest{Parent: validDocName, Block: &pb.Block{
			Type: "Core/Markdown", Record: mustStruct(t, map[string]any{"key": "k", "content": map[string]any{"text": "x"}}),
		}})
		requireCode(t, err, codes.InvalidArgument, "public key assignment")
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
				Record:            validRecord(t),
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
				Record:            validRecord(t),
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
				Record:            validRecord(t),
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
				Record:            validRecord(t),
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
				Type:   "Core/Markdown",
				Record: validRecord(t),
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
				Type:   "Core/Markdown",
				Record: validRecord(t),
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
				Type:   "Core/Markdown",
				Record: validRecord(t),
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
				Type:   "Core/Markdown",
				Record: validRecord(t),
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

// Submission rejection checks need only a small populated thread, not the
// changed-snapshot/history lifecycle in payload_submission_test.go.
func TestInvalidPayloadSubmission(t *testing.T) {
	ctx := context.Background()
	conn, err := grpc.NewClient(grpcAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	defer conn.Close()
	client := pb.NewInfrapadServiceClient(conn)
	docName := helperCreateDocument(t, client)
	ordinary := helperAddBlock(t, client, docName)
	thread, err := client.AddBlock(ctx, &pb.AddBlockRequest{Parent: docName, Block: &pb.Block{
		Type:   "Slack/Thread",
		Record: contentRecord(t, map[string]any{"url": "https://slack.example/archives/C1/p" + slackID(0)}),
	}})
	if err != nil {
		t.Fatalf("AddBlock thread: %v", err)
	}
	threadNumber := thread.GetBlock().GetBlockNumber()
	submitted, err := client.SubmitBlockPayload(ctx, &pb.SubmitBlockPayloadRequest{
		Parent: docName, BlockNumber: threadNumber,
		Payload: slackSnapshot(t, slackMessage(1, "U1", "Alice", "hello"), slackMessage(2, "U3", "", "analysis")),
	})
	if err != nil {
		t.Fatalf("SubmitBlockPayload: %v", err)
	}
	doc := submitted.GetDocument()
	generated := generatedBlocks(doc, threadNumber)
	if len(generated) != 2 {
		t.Fatalf("expected conversation and agent blocks, got %v", generated)
	}
	conversation, agent := generated[0], generated[1]

	// Every rejection must preserve the populated document, including cached
	// content and revision numbers. Register per-subtest so failures are named.
	unchanged := func(t *testing.T) {
		t.Helper()
		t.Cleanup(func() {
			resp, err := client.GetDocument(ctx, &pb.GetDocumentRequest{Name: docName})
			if err != nil {
				t.Fatalf("GetDocument: %v", err)
			}
			if !proto.Equal(resp.GetDocument(), doc) {
				t.Fatalf("rejected operation changed the document: got %v, want %v", resp.GetDocument(), doc)
			}
		})
	}
	for _, tc := range []struct {
		name     string
		document string
		block    int32
		payload  *structpb.Struct
		code     codes.Code
		reason   string
		field    string
	}{
		{
			name: "invalid_schema", document: docName, block: threadNumber,
			payload: mustStruct(t, map[string]any{"messages": []any{map[string]any{"ts": "x", "text": "t", "bogus": 1}}}),
			code:    codes.InvalidArgument, reason: "INVALID_PAYLOAD", field: "payload.messages.0.bogus",
		},
		{
			name: "generated_target", document: docName, block: conversation.GetBlockNumber(),
			code: codes.PermissionDenied, reason: "GENERATED_BLOCK",
		},
		{
			name: "unsupported_target", document: docName, block: ordinary.GetBlockNumber(),
			code: codes.FailedPrecondition, reason: "EXPANSION_UNSUPPORTED",
		},
		{
			name: "missing_block", document: docName, block: 999,
			code: codes.NotFound, reason: "BLOCK_NOT_FOUND",
		},
		{
			name: "missing_document", document: "documents/00000000-0000-4000-8000-000000000000", block: threadNumber,
			code: codes.NotFound, reason: "DOCUMENT_NOT_FOUND",
		},
	} {
		t.Run("SubmitBlockPayload/"+tc.name, func(t *testing.T) {
			unchanged(t)
			payload := tc.payload
			if payload == nil {
				payload = slackSnapshot(t)
			}
			_, err := client.SubmitBlockPayload(ctx, &pb.SubmitBlockPayloadRequest{
				Parent: tc.document, BlockNumber: tc.block, Payload: payload,
			})
			expectRejection(t, err, tc.code, tc.reason, tc.field)
		})
	}

	t.Run("SubmitBlockPayload/http_generated_target", func(t *testing.T) {
		unchanged(t)
		url := fmt.Sprintf("%s/v1/%s/blocks/%d:submitPayload", httpBaseURL(), docName, conversation.GetBlockNumber())
		code, body := httpPost(t, url, map[string]any{"payload": slackSnapshot(t).AsMap()})
		if code != http.StatusForbidden || !strings.Contains(body, "GENERATED_BLOCK") || !strings.Contains(body, "google.rpc.ErrorInfo") {
			t.Fatalf("expected HTTP 403 with ErrorInfo details, got %d: %s", code, body)
		}
	})

	t.Run("AddBlock/public_collection_membership", func(t *testing.T) {
		unchanged(t)
		_, err := client.AddBlock(ctx, &pb.AddBlockRequest{Parent: docName, Block: &pb.Block{
			Type: conversation.GetType(), Record: conversation.GetRecord(),
		}})
		requireCode(t, err, codes.InvalidArgument, "public collection membership")
	})

	t.Run("UpdateBlock/generated_echo", func(t *testing.T) {
		unchanged(t)
		_, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{Parent: docName, BlockNumber: agent.GetBlockNumber(), Block: &pb.Block{
			Type: agent.GetType(), Record: agent.GetRecord(),
		}})
		requireCode(t, err, codes.PermissionDenied, "generated echo")
	})
}
