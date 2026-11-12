package e2e

// Smoke test for production Cora registration and Markdown integration.
// Mapping and validation live in cora_test.go; generic e2e tests cover
// reconciliation, revision suppression, ordering, and atomic failures.

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
)

func TestCoraReportMarkdownSmoke(t *testing.T) {
	ctx := context.Background()
	conn, err := grpc.NewClient(grpcAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	defer conn.Close()
	client := pb.NewInfrapadServiceClient(conn)

	created, err := client.CreateDocument(ctx, &pb.CreateDocumentRequest{Title: "cora report"})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	docName := created.GetDocument().GetName()

	// The reference is created through Markdown with its initial fields.
	exported, err := client.ExportDocumentMarkdown(ctx, &pb.ExportDocumentMarkdownRequest{Name: docName})
	if err != nil {
		t.Fatalf("ExportDocumentMarkdown: %v", err)
	}
	const reportURL = "https://cora.example.test/reports/r-1"
	pushed, err := client.PushDocumentMarkdown(ctx, &pb.PushDocumentMarkdownRequest{Name: docName, Markdown: exported.GetMarkdown() +
		"::infrapad_block{block=new type=Cora/Report}\n```yaml infrapad\ncontent:\n  report_id: r-1\n  url: " + reportURL + "\n```\n"})
	if err != nil {
		t.Fatalf("PushDocumentMarkdown create: %v", err)
	}
	if len(pushed.GetChanges()) != 1 || pushed.GetChanges()[0].GetAction() != "added" {
		t.Fatalf("unexpected changes: %v", pushed.GetChanges())
	}
	reportNumber := pushed.GetChanges()[0].GetBlockNumber()

	// Unlike PagerDuty, Cora locks the reference even before ingestion.
	_, err = client.PushDocumentMarkdown(ctx, &pb.PushDocumentMarkdownRequest{Name: docName,
		Markdown: strings.Replace(pushed.GetMarkdown(), "  report_id: r-1\n", "  report_id: r-2\n", 1)})
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "report_id") {
		t.Fatalf("expected a rejected reference edit before ingestion, got %v", err)
	}

	const text = "# Cora report\n\n## Root cause\n\nPool exhaustion.\n\n```sh\nkubectl get pods\n```"
	submitted, err := client.SubmitBlockPayload(ctx, &pb.SubmitBlockPayloadRequest{
		Parent: docName, BlockNumber: reportNumber, Payload: mustStruct(t, map[string]any{
			"report_id": "r-1", "timestamp": "2026-01-01T10:05:00Z", "text": text,
		}),
	})
	if err != nil {
		t.Fatalf("SubmitBlockPayload: %v", err)
	}
	generated := generatedBlocks(submitted.GetDocument(), reportNumber)
	if len(generated) != 1 || generated[0].GetType() != "AIAgent/Message" {
		t.Fatalf("expected one generated agent message, got %v", generated)
	}

	// Headings and code fences survive the host's export/push framing.
	exported, err = client.ExportDocumentMarkdown(ctx, &pb.ExportDocumentMarkdownRequest{Name: docName})
	if err != nil {
		t.Fatalf("ExportDocumentMarkdown: %v", err)
	}
	md := exported.GetMarkdown()
	if !strings.Contains(md, "```\n"+text+"\n") {
		t.Fatalf("report Markdown missing from export:\n%s", md)
	}
	noop, err := client.PushDocumentMarkdown(ctx, &pb.PushDocumentMarkdownRequest{Name: docName, Markdown: md})
	if err != nil || len(noop.GetChanges()) != 0 || noop.GetMarkdown() != md {
		t.Fatalf("expected an unchanged Markdown round-trip, got %v, %v", noop, err)
	}
}
