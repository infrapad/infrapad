package integration

import (
	"context"
	"log"
	"os"
	"testing"

	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store/postgres"
)

/*
 * An integration test to cover the basic flow covered by the public API
 * of the model and db packages
 * (no HTTP/gRPC api or cli involved).
 *
 * Scenario to test:
 * 1. create a new document of type 'incident' in "payments" namespace
 * 2. create a block of alerts_matcher type, that create a filter to name="CrashLoopBackOff" in "payments" namespace"
 * 3. create a new block "markdown" with "initial investigation writeup"
 * 4. update the alerts_matcher block with adding additional condition of name="KubeNodeNotReady"
 * 5. update the markdown block with "updated investigation writeup"
 *
 *
 */

func testConnStr() string {
	if s := os.Getenv("TEST_DBSTRING"); s != "" {
		return s
	}
	log.Fatalln("TEST_DBSTRING not defined.")
	return ""
}

func TestIncidentInvestigation(t *testing.T) {
	ctx := context.Background()

	db, err := postgres.OpenPG(testConnStr())
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	s := postgres.NewStore(db)
	ctrl := controller.New(s)

	// -----------------------------------------------------------------------
	// 1. Create a new document of type 'incident' in "payments" namespace.
	// -----------------------------------------------------------------------
	doc, err := ctrl.CreateDoc(ctx, model.Doc{
		Title:     "Payment service crash loop",
		Namespace: "payments",
	})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if doc.Uid == "" {
		t.Fatal("expected non-empty doc uid")
	}
	if doc.Namespace != "payments" {
		t.Fatalf("expected namespace 'payments', got %q", doc.Namespace)
	}
	t.Logf("created doc %s", doc.Uid)

	// -----------------------------------------------------------------------
	// 2. Create a block of alerts_matcher type with filter name="CrashLoopBackOff".
	// -----------------------------------------------------------------------
	alertsBlock, err := ctrl.AddBlock(ctx, doc.Uid, model.Block{
		Type: "alerts_matcher",
		Content: model.AlertsMatcherBC{
			LabelsMatchers: []map[string][]string{
				{"name": {"CrashLoopBackOff"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("add alerts_matcher block: %v", err)
	}
	if alertsBlock.BlockNumber != 1 {
		t.Fatalf("expected block_number 1, got %d", alertsBlock.BlockNumber)
	}
	if alertsBlock.RevisionNumber != 1 {
		t.Fatalf("expected revision 1, got %d", alertsBlock.RevisionNumber)
	}
	t.Logf("added alerts_matcher block %d rev %d", alertsBlock.BlockNumber, alertsBlock.RevisionNumber)

	// -----------------------------------------------------------------------
	// 3. Create a new markdown block with "initial investigation writeup".
	// -----------------------------------------------------------------------
	mdBlock, err := ctrl.AddBlock(ctx, doc.Uid, model.Block{
		Type:    "markdown",
		Content: model.NewMarkdownBC("initial investigation writeup"),
	})
	if err != nil {
		t.Fatalf("add markdown block: %v", err)
	}
	if mdBlock.BlockNumber != 2 {
		t.Fatalf("expected block_number 2, got %d", mdBlock.BlockNumber)
	}
	t.Logf("added markdown block %d rev %d", mdBlock.BlockNumber, mdBlock.RevisionNumber)

	// -----------------------------------------------------------------------
	// 4. Update alerts_matcher block: add condition name="KubeNodeNotReady".
	// -----------------------------------------------------------------------
	updatedAlerts, err := ctrl.UpdateBlock(ctx, doc.Uid, alertsBlock.BlockNumber, model.Block{
		Type: "alerts_matcher",
		Content: model.AlertsMatcherBC{
			LabelsMatchers: []map[string][]string{
				{"name": {"CrashLoopBackOff"}},
				{"name": {"KubeNodeNotReady"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("update alerts_matcher block: %v", err)
	}
	if updatedAlerts.RevisionNumber != 2 {
		t.Fatalf("expected revision 2 after update, got %d", updatedAlerts.RevisionNumber)
	}
	t.Logf("updated alerts_matcher block %d to rev %d", updatedAlerts.BlockNumber, updatedAlerts.RevisionNumber)

	// -----------------------------------------------------------------------
	// 5. Update markdown block with "updated investigation writeup".
	// -----------------------------------------------------------------------
	updatedMd, err := ctrl.UpdateBlock(ctx, doc.Uid, mdBlock.BlockNumber, model.Block{
		Type:    "markdown",
		Content: model.NewMarkdownBC("updated investigation writeup"),
	})
	if err != nil {
		t.Fatalf("update markdown block: %v", err)
	}
	if updatedMd.RevisionNumber != 2 {
		t.Fatalf("expected revision 2 after update, got %d", updatedMd.RevisionNumber)
	}
	t.Logf("updated markdown block %d to rev %d", updatedMd.BlockNumber, updatedMd.RevisionNumber)

	// -----------------------------------------------------------------------
	// Verify final state: GetDoc should return 2 blocks, each at revision 2.
	// -----------------------------------------------------------------------
	finalDoc, err := ctrl.GetDoc(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("get doc: %v", err)
	}
	if len(finalDoc.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(finalDoc.Blocks))
	}
	for _, blk := range finalDoc.Blocks {
		if blk.RevisionNumber != 2 {
			t.Errorf("block %d: expected revision 2, got %d", blk.BlockNumber, blk.RevisionNumber)
		}
	}

	// Verify revision history of the alerts_matcher block.
	alertHistory, err := ctrl.ListBlockHistory(ctx, doc.Uid, alertsBlock.BlockNumber)
	if err != nil {
		t.Fatalf("list block history: %v", err)
	}
	if len(alertHistory) != 2 {
		t.Fatalf("expected 2 revisions for alerts_matcher, got %d", len(alertHistory))
	}

	// First revision should have 1 matcher, second should have 2.
	am1 := alertHistory[0].Content.(model.AlertsMatcherBC)
	am2 := alertHistory[1].Content.(model.AlertsMatcherBC)
	if len(am1.LabelsMatchers) != 1 {
		t.Errorf("rev 1: expected 1 matcher, got %d", len(am1.LabelsMatchers))
	}
	if len(am2.LabelsMatchers) != 2 {
		t.Errorf("rev 2: expected 2 matchers, got %d", len(am2.LabelsMatchers))
	}

	t.Log("incident investigation test passed")
}
