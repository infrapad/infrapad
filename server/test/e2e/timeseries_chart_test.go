package e2e

// Lifecycle of Prometheus/TimeSeriesChart: results refreshes stay in the
// latest revision, query edits through Markdown create a revision and clear
// the snapshot, description (body) edits create a revision keeping the
// snapshot, and earlier revisions keep their last dataset. Generic
// revision-policy paths are covered by controller and integration tests.

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
)

func TestTimeSeriesChartLifecycle(t *testing.T) {
	ctx := context.Background()
	conn, err := grpc.NewClient(grpcAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	defer conn.Close()
	client := pb.NewInfrapadServiceClient(conn)

	created, err := client.CreateDocument(ctx, &pb.CreateDocumentRequest{Title: "timeseries chart"})
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	docName := created.GetDocument().GetName()

	query := map[string]any{
		"expression": "rate(http_requests_total[5m])", "source_url": "https://prometheus.example.com",
		"start": "2026-11-13T10:00:00Z", "end": "2026-11-13T18:00:00Z", "step_seconds": 60,
	}
	chart := func(value float64) map[string]any {
		return map[string]any{"query": query, "title": "HTTP request rate", "unit": "requests/s",
			"text": "Initial description",
			"query_results": map[string]any{"series": []any{map[string]any{
				"labels": map[string]any{"instance": "app-1"}, "samples": []any{[]any{1794564000, value}, []any{1794564060, nil}},
			}}}}
	}

	// Invalid metadata is rejected.
	_, err = client.AddBlock(ctx, &pb.AddBlockRequest{Parent: docName, Block: &pb.Block{
		Type: "Prometheus/TimeSeriesChart", Record: contentRecord(t, map[string]any{"query": map[string]any{
			"expression": "up", "start": "2026-11-13T18:00:00Z", "end": "2026-11-13T10:00:00Z", "step_seconds": 60,
		}}),
	}})
	requireCode(t, err, codes.InvalidArgument, "reversed bounds")

	added, err := client.AddBlock(ctx, &pb.AddBlockRequest{Parent: docName, Block: &pb.Block{
		Type: "Prometheus/TimeSeriesChart", Record: contentRecord(t, chart(1)),
	}})
	if err != nil {
		t.Fatalf("AddBlock: %v", err)
	}
	number := added.GetBlock().GetBlockNumber()

	// A results refresh stays in revision 1.
	update := func(content map[string]any) *pb.Block {
		t.Helper()
		resp, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{Parent: docName, BlockNumber: number, Block: &pb.Block{
			Type: "Prometheus/TimeSeriesChart", Record: contentRecord(t, content),
		}})
		if err != nil {
			t.Fatalf("UpdateBlock: %v", err)
		}
		return resp.GetBlock()
	}
	if refreshed := update(chart(2)); refreshed.GetRevisionNumber() != 1 || firstValue(t, refreshed) != 2 {
		t.Fatalf("expected an in-place refresh of revision 1, got rev %d: %v", refreshed.GetRevisionNumber(), recordContent(refreshed))
	}

	// The Markdown header carries the metadata only; a query edit creates
	// revision 2 without results.
	exported, err := client.ExportDocumentMarkdown(ctx, &pb.ExportDocumentMarkdownRequest{Name: docName})
	if err != nil {
		t.Fatalf("ExportDocumentMarkdown: %v", err)
	}
	md := exported.GetMarkdown()
	if strings.Contains(md, "query_results") || strings.Contains(md, "samples") || !strings.Contains(md, "step_seconds: 60\n") {
		t.Fatalf("unexpected chart Markdown:\n%s", md)
	}
	pushed, err := client.PushDocumentMarkdown(ctx, &pb.PushDocumentMarkdownRequest{Name: docName,
		Markdown: strings.Replace(md, "step_seconds: 60\n", "step_seconds: 30\n", 1)})
	if err != nil {
		t.Fatalf("PushDocumentMarkdown: %v", err)
	}
	if len(pushed.GetChanges()) != 1 {
		t.Fatalf("unexpected changes: %v", pushed.GetChanges())
	}
	edited := getChart(t, client, docName, number, 0)
	if edited.GetRevisionNumber() != 2 || recordContent(edited)["query_results"] != nil ||
		recordContent(edited)["query"].(map[string]any)["step_seconds"] != float64(30) {
		t.Fatalf("expected revision 2 with the edited query and no results, got rev %d: %v", edited.GetRevisionNumber(), recordContent(edited))
	}

	// Replacement results are uploaded into revision 2 in place.
	query["step_seconds"] = 30
	if uploaded := update(chart(3)); uploaded.GetRevisionNumber() != 2 || firstValue(t, uploaded) != 3 {
		t.Fatalf("expected an in-place upload into revision 2, got rev %d", uploaded.GetRevisionNumber())
	}
	// Revision 1 keeps its last dataset.
	if first := getChart(t, client, docName, number, 1); firstValue(t, first) != 2 {
		t.Fatalf("revision 1 lost its last dataset: %v", recordContent(first))
	}

	// The description is the Markdown body: editing it creates revision 3
	// and keeps the snapshot.
	exported, err = client.ExportDocumentMarkdown(ctx, &pb.ExportDocumentMarkdownRequest{Name: docName})
	if err != nil {
		t.Fatalf("ExportDocumentMarkdown: %v", err)
	}
	md = exported.GetMarkdown()
	if !strings.Contains(md, "\nInitial description\n") {
		t.Fatalf("expected the description as body:\n%s", md)
	}
	if _, err := client.PushDocumentMarkdown(ctx, &pb.PushDocumentMarkdownRequest{Name: docName,
		Markdown: strings.Replace(md, "Initial description", "Updated **description**", 1)}); err != nil {
		t.Fatalf("PushDocumentMarkdown: %v", err)
	}
	described := getChart(t, client, docName, number, 0)
	if described.GetRevisionNumber() != 3 || recordContent(described)["text"] != "Updated **description**" || firstValue(t, described) != 3 {
		t.Fatalf("expected revision 3 with the new text and kept results, got rev %d: %v", described.GetRevisionNumber(), recordContent(described))
	}
}

func getChart(t *testing.T, client pb.InfrapadServiceClient, docName string, number, revision int32) *pb.Block {
	t.Helper()
	resp, err := client.GetBlock(context.Background(), &pb.GetBlockRequest{Parent: docName, BlockNumber: number, RevisionNumber: revision})
	if err != nil {
		t.Fatalf("GetBlock: %v", err)
	}
	return resp.GetBlock()
}

// firstValue returns the value of the first sample of the first series.
func firstValue(t *testing.T, block *pb.Block) float64 {
	t.Helper()
	results, _ := recordContent(block)["query_results"].(map[string]any)
	if results == nil {
		t.Fatalf("block has no results: %v", recordContent(block))
	}
	series := results["series"].([]any)[0].(map[string]any)
	return series["samples"].([]any)[0].([]any)[1].(float64)
}
