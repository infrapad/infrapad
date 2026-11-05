package e2e

// One API lifecycle scenario for the read-only event timestamp contract:
// output populated from the record, preservation after editing, accepted
// unchanged echoes, and rejected assignments/changes without committed
// changes.

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
)

func TestTimestampLifecycleGRPC(t *testing.T) {
	ctx := context.Background()

	conn, err := grpc.NewClient(grpcAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	defer conn.Close()

	client := pb.NewInfrapadServiceClient(conn)

	createDocResp, err := client.CreateDocument(ctx, &pb.CreateDocumentRequest{Title: "timestamp lifecycle"})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	docName := createDocResp.GetDocument().GetName()

	// Creation must omit the timestamp: assigning it is invalid input.
	supplied := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	_, err = client.CreateDocument(ctx, &pb.CreateDocumentRequest{
		Title:  "timestamp assignment",
		Blocks: []*pb.Block{{Type: "Core/Markdown", Record: timestampedRecord(t, map[string]any{"text": "x"}, supplied)}},
	})
	requireCode(t, err, codes.InvalidArgument, "initial block timestamp assignment")

	_, err = client.AddBlock(ctx, &pb.AddBlockRequest{
		Parent: docName,
		Block: &pb.Block{
			Type:   "Core/Markdown",
			Record: timestampedRecord(t, map[string]any{"text": "first"}, supplied),
		},
	})
	requireCode(t, err, codes.InvalidArgument, "add block timestamp assignment")

	addResp, err := client.AddBlock(ctx, &pb.AddBlockRequest{
		Parent: docName,
		Block: &pb.Block{
			Type:   "Core/Markdown",
			Record: contentRecord(t, map[string]any{"text": "first"}),
		},
	})
	if err != nil {
		t.Fatalf("AddBlock: %v", err)
	}
	blk := addResp.GetBlock()

	// The response exposes the record's event timestamp: for an ordinary
	// block it is the creation time (the revision's created_at is the DB
	// row timestamp of the same creation, so they coincide up to storage
	// precision).
	eventTS := recordTimestamp(t, blk)
	if eventTS.IsZero() {
		t.Fatalf("expected a populated event timestamp on the created block, got %v", blk.GetRecord())
	}
	if blk.GetCreatedAt() == nil || blk.GetCreatedAt().AsTime().Sub(eventTS) > time.Minute {
		t.Fatalf("ordinary block event timestamp %v is not its creation time %v", eventTS, blk.GetCreatedAt())
	}

	// An omitted timestamp on updates preserves the stored event time.
	updated, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
		Parent:      docName,
		BlockNumber: blk.GetBlockNumber(),
		Block: &pb.Block{
			Type:   "Core/Markdown",
			Record: contentRecord(t, map[string]any{"text": "second"}),
		},
	})
	if err != nil {
		t.Fatalf("UpdateBlock without timestamp: %v", err)
	}
	if got := recordTimestamp(t, updated.GetBlock()); !got.Equal(eventTS) {
		t.Fatalf("event timestamp not preserved across the edit: %v != %v", got, eventTS)
	}

	// An exactly echoed timestamp is accepted.
	echoed, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
		Parent:      docName,
		BlockNumber: blk.GetBlockNumber(),
		Block: &pb.Block{
			Type:   "Core/Markdown",
			Record: timestampedRecord(t, map[string]any{"text": "third"}, eventTS),
		},
	})
	if err != nil {
		t.Fatalf("UpdateBlock with unchanged timestamp echo: %v", err)
	}
	if echoed.GetBlock().GetRevisionNumber() != 3 {
		t.Fatalf("expected revision 3 after the echoed update, got %d", echoed.GetBlock().GetRevisionNumber())
	}

	// A changed timestamp is rejected without committing anything.
	changed := eventTS.Add(time.Minute)
	_, err = client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
		Parent:      docName,
		BlockNumber: blk.GetBlockNumber(),
		Block: &pb.Block{
			Type:   "Core/Markdown",
			Record: timestampedRecord(t, map[string]any{"text": "illegal"}, changed),
		},
	})
	requireCode(t, err, codes.InvalidArgument, "changed event timestamp")

	final, err := client.GetBlock(ctx, &pb.GetBlockRequest{Parent: docName, BlockNumber: blk.GetBlockNumber()})
	if err != nil {
		t.Fatalf("GetBlock: %v", err)
	}
	if final.GetBlock().GetRevisionNumber() != 3 {
		t.Fatalf("rejected timestamp change committed a revision: %d", final.GetBlock().GetRevisionNumber())
	}
	if got := recordContent(final.GetBlock())["text"]; got != "third" {
		t.Fatalf("rejected timestamp change committed content: %q", got)
	}
	if got := recordTimestamp(t, final.GetBlock()); !got.Equal(eventTS) {
		t.Fatalf("event timestamp changed after the rejected update: %v", got)
	}

	// Document views embed the same read-only event timestamps.
	docResp, err := client.GetDocument(ctx, &pb.GetDocumentRequest{Name: docName})
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	for _, b := range docResp.GetDocument().GetBlocks() {
		if recordTimestamp(t, b).IsZero() {
			t.Fatalf("document block %d carries no event timestamp", b.GetBlockNumber())
		}
	}
}
