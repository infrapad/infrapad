package e2e

import (
	"context"
	"os"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
)

func grpcAddr() string {
	if s := os.Getenv("GRPC_ADDR"); s != "" {
		return s
	}
	return "localhost:50061"
}

// mustStruct is a test helper that creates a structpb.Struct from a map.
func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	return s
}

func TestIncidentInvestigation(t *testing.T) {
	ctx := context.Background()

	conn, err := grpc.NewClient(grpcAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	defer conn.Close()

	client := pb.NewInfrapadServiceClient(conn)

	// -----------------------------------------------------------------------
	// 1. Create a new document of type 'incident' in "payments" namespace.
	// -----------------------------------------------------------------------
	createDocResp, err := client.CreateDoc(ctx, &pb.CreateDocRequest{
		Title:     "Payment service crash loop",
		Namespace: "payments",
	})
	if err != nil {
		t.Fatalf("CreateDoc: %v", err)
	}
	doc := createDocResp.GetDoc()
	if doc.GetName() == "" {
		t.Fatal("expected non-empty doc name")
	}
	if doc.GetNamespace() != "payments" {
		t.Fatalf("expected namespace 'payments', got %q", doc.GetNamespace())
	}
	t.Logf("created doc %s", doc.GetName())

	docName := doc.GetName()

	// -----------------------------------------------------------------------
	// 2. Create a block of alerts_matcher type with filter name="CrashLoopBackOff".
	// -----------------------------------------------------------------------
	addAlertsResp, err := client.AddBlock(ctx, &pb.AddBlockRequest{
		Parent: docName,
		Block: &pb.Block{
			Type: "alerts_matcher",
			Content: mustStruct(t, map[string]any{
				"LabelsMatchers": []any{
					map[string]any{
						"name": []any{"CrashLoopBackOff"},
					},
				},
			}),
		},
	})
	if err != nil {
		t.Fatalf("AddBlock alerts_matcher: %v", err)
	}
	alertsBlock := addAlertsResp.GetBlock()
	if alertsBlock.GetBlockNumber() != 1 {
		t.Fatalf("expected block_number 1, got %d", alertsBlock.GetBlockNumber())
	}
	if alertsBlock.GetRevisionNumber() != 1 {
		t.Fatalf("expected revision 1, got %d", alertsBlock.GetRevisionNumber())
	}
	t.Logf("added alerts_matcher block %d rev %d", alertsBlock.GetBlockNumber(), alertsBlock.GetRevisionNumber())

	// -----------------------------------------------------------------------
	// 3. Create a new markdown block with "initial investigation writeup".
	// -----------------------------------------------------------------------
	addMdResp, err := client.AddBlock(ctx, &pb.AddBlockRequest{
		Parent: docName,
		Block: &pb.Block{
			Type: "markdown",
			Content: mustStruct(t, map[string]any{
				"text": "initial investigation writeup",
			}),
		},
	})
	if err != nil {
		t.Fatalf("AddBlock markdown: %v", err)
	}
	mdBlock := addMdResp.GetBlock()
	if mdBlock.GetBlockNumber() != 2 {
		t.Fatalf("expected block_number 2, got %d", mdBlock.GetBlockNumber())
	}
	t.Logf("added markdown block %d rev %d", mdBlock.GetBlockNumber(), mdBlock.GetRevisionNumber())

	// -----------------------------------------------------------------------
	// 4. Update alerts_matcher block: add condition name="KubeNodeNotReady".
	// -----------------------------------------------------------------------
	updateAlertsResp, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
		Parent:      docName,
		BlockNumber: alertsBlock.GetBlockNumber(),
		Block: &pb.Block{
			Type: "alerts_matcher",
			Content: mustStruct(t, map[string]any{
				"LabelsMatchers": []any{
					map[string]any{
						"name": []any{"CrashLoopBackOff"},
					},
					map[string]any{
						"name": []any{"KubeNodeNotReady"},
					},
				},
			}),
		},
	})
	if err != nil {
		t.Fatalf("UpdateBlock alerts_matcher: %v", err)
	}
	updatedAlerts := updateAlertsResp.GetBlock()
	if updatedAlerts.GetRevisionNumber() != 2 {
		t.Fatalf("expected revision 2 after update, got %d", updatedAlerts.GetRevisionNumber())
	}
	t.Logf("updated alerts_matcher block %d to rev %d", updatedAlerts.GetBlockNumber(), updatedAlerts.GetRevisionNumber())

	// -----------------------------------------------------------------------
	// 5. Update markdown block with "updated investigation writeup".
	// -----------------------------------------------------------------------
	updateMdResp, err := client.UpdateBlock(ctx, &pb.UpdateBlockRequest{
		Parent:      docName,
		BlockNumber: mdBlock.GetBlockNumber(),
		Block: &pb.Block{
			Type: "markdown",
			Content: mustStruct(t, map[string]any{
				"text": "updated investigation writeup",
			}),
		},
	})
	if err != nil {
		t.Fatalf("UpdateBlock markdown: %v", err)
	}
	updatedMd := updateMdResp.GetBlock()
	if updatedMd.GetRevisionNumber() != 2 {
		t.Fatalf("expected revision 2 after update, got %d", updatedMd.GetRevisionNumber())
	}
	t.Logf("updated markdown block %d to rev %d", updatedMd.GetBlockNumber(), updatedMd.GetRevisionNumber())

	// -----------------------------------------------------------------------
	// Verify final state: GetDoc should return 2 blocks, each at revision 2.
	// -----------------------------------------------------------------------
	getDocResp, err := client.GetDoc(ctx, &pb.GetDocRequest{Name: docName})
	if err != nil {
		t.Fatalf("GetDoc: %v", err)
	}
	finalDoc := getDocResp.GetDoc()
	if len(finalDoc.GetBlocks()) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(finalDoc.GetBlocks()))
	}
	for _, blk := range finalDoc.GetBlocks() {
		if blk.GetRevisionNumber() != 2 {
			t.Errorf("block %d: expected revision 2, got %d", blk.GetBlockNumber(), blk.GetRevisionNumber())
		}
	}

	// Verify revision history of the alerts_matcher block.
	histResp, err := client.ListBlockHistory(ctx, &pb.ListBlockHistoryRequest{
		Parent:      docName,
		BlockNumber: alertsBlock.GetBlockNumber(),
	})
	if err != nil {
		t.Fatalf("ListBlockHistory: %v", err)
	}
	alertHistory := histResp.GetBlocks()
	if len(alertHistory) != 2 {
		t.Fatalf("expected 2 revisions for alerts_matcher, got %d", len(alertHistory))
	}

	// First revision should have 1 matcher, second should have 2.
	am1 := alertHistory[0].GetContent().AsMap()
	am2 := alertHistory[1].GetContent().AsMap()
	matchers1, _ := am1["LabelsMatchers"].([]any)
	matchers2, _ := am2["LabelsMatchers"].([]any)
	if len(matchers1) != 1 {
		t.Errorf("rev 1: expected 1 matcher, got %d", len(matchers1))
	}
	if len(matchers2) != 2 {
		t.Errorf("rev 2: expected 2 matchers, got %d", len(matchers2))
	}

	t.Log("incident investigation e2e test passed")
}
