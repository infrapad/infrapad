package integration

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"testing"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
	"github.com/infrapad/infrapad/server/pkg/extensions/prometheus"
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
 * 2. create a block of Prometheus/AlertsMatcher type, that create a filter to alertname="CrashLoopBackOff" in "payments" namespace"
 * 3. create a new block "Core/Markdown" with "initial investigation writeup"
 * 4. update the alerts_matcher block with adding additional condition of alertname="KubeNodeNotReady"
 * 5. update the markdown block with "updated investigation writeup"
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

	registry, err := content.NewRegistry(core.ContentTypes(), prometheus.ContentTypes())
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	s := postgres.NewStore(db)
	ctrl := controller.New(s, registry)

	// -----------------------------------------------------------------------
	// 1. Create a new document of type 'incident' in "payments" namespace.
	// -----------------------------------------------------------------------
	doc, err := ctrl.CreateDocument(ctx, model.Document{
		Title:     "Payment service crash loop",
		Namespace: "payments",
	})
	if err != nil {
		t.Fatalf("create doc: %v", err)
	}
	if doc.Uid == "" {
		t.Fatal("expected non-empty document uid")
	}
	if doc.Namespace != "payments" {
		t.Fatalf("expected namespace 'payments', got %q", doc.Namespace)
	}
	t.Logf("created document %s", doc.Uid)

	// -----------------------------------------------------------------------
	// 2. Create a block of alerts matcher type with filter alertname="CrashLoopBackOff".
	// -----------------------------------------------------------------------
	alertsBlock, err := ctrl.AddBlock(ctx, doc.Uid, model.Block{
		Type: prometheus.AlertsMatcherTypeName,
		Content: json.RawMessage(
			`{"labels_matchers":[{"alertname":["CrashLoopBackOff"]}],"since":"2026-08-14T09:00:00Z"}`),
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
		Type:    core.MarkdownTypeName,
		Content: json.RawMessage(`{"text":"initial investigation writeup"}`),
	})
	if err != nil {
		t.Fatalf("add markdown block: %v", err)
	}
	if mdBlock.BlockNumber != 2 {
		t.Fatalf("expected block_number 2, got %d", mdBlock.BlockNumber)
	}
	t.Logf("added markdown block %d rev %d", mdBlock.BlockNumber, mdBlock.RevisionNumber)

	// -----------------------------------------------------------------------
	// 4. Update alerts_matcher block: add condition alertname="KubeNodeNotReady".
	// -----------------------------------------------------------------------
	updatedAlerts, err := ctrl.UpdateBlock(ctx, doc.Uid, alertsBlock.BlockNumber, model.Block{
		Type: prometheus.AlertsMatcherTypeName,
		Content: json.RawMessage(
			`{"labels_matchers":[{"alertname":["CrashLoopBackOff"]},{"alertname":["KubeNodeNotReady"]}],"since":"2026-08-14T09:00:00Z"}`),
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
		Type:    core.MarkdownTypeName,
		Content: json.RawMessage(`{"text":"updated investigation writeup"}`),
	})
	if err != nil {
		t.Fatalf("update markdown block: %v", err)
	}
	if updatedMd.RevisionNumber != 2 {
		t.Fatalf("expected revision 2 after update, got %d", updatedMd.RevisionNumber)
	}
	t.Logf("updated markdown block %d to rev %d", updatedMd.BlockNumber, updatedMd.RevisionNumber)

	// -----------------------------------------------------------------------
	// Verify final state: GetDocument should return 2 blocks, each at revision 2.
	// -----------------------------------------------------------------------
	finalDoc, err := ctrl.GetDocument(ctx, doc.Uid)
	if err != nil {
		t.Fatalf("get document: %v", err)
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

	// First revision should have 1 matcher, second should have 2; the record
	// timestamp must be preserved across revisions.
	am1, err := content.ItemView[prometheus.AlertsMatcher](alertHistory[0].Record)
	if err != nil {
		t.Fatalf("rev 1 record: %v", err)
	}
	am2, err := content.ItemView[prometheus.AlertsMatcher](alertHistory[1].Record)
	if err != nil {
		t.Fatalf("rev 2 record: %v", err)
	}
	if len(am1.ContentValue.LabelsMatchers) != 1 {
		t.Errorf("rev 1: expected 1 matcher, got %d", len(am1.ContentValue.LabelsMatchers))
	}
	if len(am2.ContentValue.LabelsMatchers) != 2 {
		t.Errorf("rev 2: expected 2 matchers, got %d", len(am2.ContentValue.LabelsMatchers))
	}
	if !am1.Timestamp().Equal(am2.Timestamp()) {
		t.Errorf("record timestamp not preserved across revisions: %v != %v",
			am1.Timestamp(), am2.Timestamp())
	}

	t.Log("incident investigation test passed")
}
