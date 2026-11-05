package e2e

import (
	"fmt"
	"testing"
)

func TestIncidentInvestigationHTTP(t *testing.T) {
	base := httpBaseURL()

	// -----------------------------------------------------------------------
	// 1. Create a new document of type 'incident' in "payments" namespace.
	// -----------------------------------------------------------------------
	var createDocResp struct {
		Document struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"document"`
	}
	httpDo(t, "POST", base+"/v1/documents", map[string]any{
		"title":     "Payment service crash loop",
		"namespace": "payments",
	}, &createDocResp)

	docName := createDocResp.Document.Name
	if docName == "" {
		t.Fatal("expected non-empty document name")
	}
	if createDocResp.Document.Namespace != "payments" {
		t.Fatalf("expected namespace 'payments', got %q", createDocResp.Document.Namespace)
	}
	t.Logf("created document %s", docName)

	// The document name is like "documents/<id>"; for URL paths we use it directly
	// since the gateway routes are /v1/{parent=documents/*}/...
	docURL := fmt.Sprintf("%s/v1/%s", base, docName)

	// -----------------------------------------------------------------------
	// 2. Add a block of alerts_matcher type with filter name="CrashLoopBackOff".
	// -----------------------------------------------------------------------
	var addAlertsResp struct {
		Block struct {
			BlockNumber    int `json:"blockNumber"`
			RevisionNumber int `json:"revisionNumber"`
		} `json:"block"`
	}
	httpDo(t, "POST", docURL+"/blocks", map[string]any{
		"block": map[string]any{
			"type": "Prometheus/AlertsMatcher",
			"record": map[string]any{"content": map[string]any{
				"labels_matchers": []any{
					map[string]any{
						"alertname": []any{"CrashLoopBackOff"},
					},
				},
				"since": "2026-08-14T09:00:00Z",
			}},
		},
	}, &addAlertsResp)

	if addAlertsResp.Block.BlockNumber != 1 {
		t.Fatalf("expected block_number 1, got %d", addAlertsResp.Block.BlockNumber)
	}
	if addAlertsResp.Block.RevisionNumber != 1 {
		t.Fatalf("expected revision 1, got %d", addAlertsResp.Block.RevisionNumber)
	}
	t.Logf("added alerts_matcher block %d rev %d", addAlertsResp.Block.BlockNumber, addAlertsResp.Block.RevisionNumber)

	// -----------------------------------------------------------------------
	// 3. Add a markdown block with "initial investigation writeup".
	// -----------------------------------------------------------------------
	var addMdResp struct {
		Block struct {
			BlockNumber    int `json:"blockNumber"`
			RevisionNumber int `json:"revisionNumber"`
		} `json:"block"`
	}
	httpDo(t, "POST", docURL+"/blocks", map[string]any{
		"block": map[string]any{
			"type": "Core/Markdown",
			"record": map[string]any{"content": map[string]any{
				"text": "initial investigation writeup",
			}},
		},
	}, &addMdResp)

	if addMdResp.Block.BlockNumber != 2 {
		t.Fatalf("expected block_number 2, got %d", addMdResp.Block.BlockNumber)
	}
	t.Logf("added markdown block %d rev %d", addMdResp.Block.BlockNumber, addMdResp.Block.RevisionNumber)

	// -----------------------------------------------------------------------
	// 4. Update alerts_matcher block: add condition name="KubeNodeNotReady".
	// -----------------------------------------------------------------------
	var updateAlertsResp struct {
		Block struct {
			BlockNumber    int `json:"blockNumber"`
			RevisionNumber int `json:"revisionNumber"`
		} `json:"block"`
	}
	httpDo(t, "PUT", fmt.Sprintf("%s/blocks/%d", docURL, addAlertsResp.Block.BlockNumber), map[string]any{
		"block": map[string]any{
			"type": "Prometheus/AlertsMatcher",
			"record": map[string]any{"content": map[string]any{
				"labels_matchers": []any{
					map[string]any{
						"alertname": []any{"CrashLoopBackOff"},
					},
					map[string]any{
						"alertname": []any{"KubeNodeNotReady"},
					},
				},
				"since": "2026-08-14T09:00:00Z",
			}},
		},
	}, &updateAlertsResp)

	if updateAlertsResp.Block.RevisionNumber != 2 {
		t.Fatalf("expected revision 2 after update, got %d", updateAlertsResp.Block.RevisionNumber)
	}
	t.Logf("updated alerts_matcher block %d to rev %d", updateAlertsResp.Block.BlockNumber, updateAlertsResp.Block.RevisionNumber)

	// -----------------------------------------------------------------------
	// 5. Update markdown block with "updated investigation writeup".
	// -----------------------------------------------------------------------
	var updateMdResp struct {
		Block struct {
			BlockNumber    int `json:"blockNumber"`
			RevisionNumber int `json:"revisionNumber"`
		} `json:"block"`
	}
	httpDo(t, "PUT", fmt.Sprintf("%s/blocks/%d", docURL, addMdResp.Block.BlockNumber), map[string]any{
		"block": map[string]any{
			"type": "Core/Markdown",
			"record": map[string]any{"content": map[string]any{
				"text": "updated investigation writeup",
			}},
		},
	}, &updateMdResp)

	if updateMdResp.Block.RevisionNumber != 2 {
		t.Fatalf("expected revision 2 after update, got %d", updateMdResp.Block.RevisionNumber)
	}
	t.Logf("updated markdown block %d to rev %d", updateMdResp.Block.BlockNumber, updateMdResp.Block.RevisionNumber)

	// -----------------------------------------------------------------------
	// Verify final state: GetDocument should return 2 blocks, each at revision 2.
	// -----------------------------------------------------------------------
	var getDocResp struct {
		Document struct {
			Blocks []struct {
				BlockNumber    int `json:"blockNumber"`
				RevisionNumber int `json:"revisionNumber"`
			} `json:"blocks"`
		} `json:"document"`
	}
	httpDo(t, "GET", docURL, nil, &getDocResp)

	if len(getDocResp.Document.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(getDocResp.Document.Blocks))
	}
	for _, blk := range getDocResp.Document.Blocks {
		if blk.RevisionNumber != 2 {
			t.Errorf("block %d: expected revision 2, got %d", blk.BlockNumber, blk.RevisionNumber)
		}
	}

	// -----------------------------------------------------------------------
	// Verify revision history of the alerts_matcher block.
	// -----------------------------------------------------------------------
	var histResp struct {
		Blocks []struct {
			BlockNumber    int `json:"blockNumber"`
			RevisionNumber int `json:"revisionNumber"`
			Record         struct {
				Content map[string]any `json:"content"`
			} `json:"record"`
		} `json:"blocks"`
	}
	httpDo(t, "GET", fmt.Sprintf("%s/blocks/%d/history", docURL, addAlertsResp.Block.BlockNumber), nil, &histResp)

	if len(histResp.Blocks) != 2 {
		t.Fatalf("expected 2 revisions for alerts_matcher, got %d", len(histResp.Blocks))
	}

	// First revision should have 1 matcher, second should have 2.
	matchers1, _ := histResp.Blocks[0].Record.Content["labels_matchers"].([]any)
	matchers2, _ := histResp.Blocks[1].Record.Content["labels_matchers"].([]any)
	if len(matchers1) != 1 {
		t.Errorf("rev 1: expected 1 matcher, got %d", len(matchers1))
	}
	if len(matchers2) != 2 {
		t.Errorf("rev 2: expected 2 matchers, got %d", len(matchers2))
	}

	t.Log("incident investigation HTTP e2e test passed")
}
