package markdown

import (
	"os"
	"strings"
	"testing"
)

const testInput = `---
doc: 42cd704a-f697-4a78-9c29-3c7235c9500f
title: Payment service crash loop
namespace: payments
status: active
---
::infrapad_block{type=alerts_matcher block=1 rev=2 author=incident_detector:123}
` + "```yaml\n" + `LabelsMatchers:
  - name: [CrashLoopBackOff]
  - name: [KubeNodeNotReady]
` + "```\n" + `::infrapad_block{type=markdown block=2 rev=2 author=agentic_run_analysis:456}
# Initial investigation

## Situation description

This incident started at 2026-07-12 12:42 by CrashLoopBackOff...

::infrapad_block{type=markdown block=3 rev=1 author=agentic_run_execution:543}
Get the pods in the namespace
` + "```\n" + `$ oc get pods --namespace paymants
` + "```\n"

func TestParse(t *testing.T) {
	doc, err := Parse([]byte(testInput))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Document metadata.
	if doc.Meta.DocID != "42cd704a-f697-4a78-9c29-3c7235c9500f" {
		t.Errorf("DocID = %q", doc.Meta.DocID)
	}
	if doc.Meta.Title != "Payment service crash loop" {
		t.Errorf("Title = %q", doc.Meta.Title)
	}
	if doc.Meta.Namespace != "payments" {
		t.Errorf("Namespace = %q", doc.Meta.Namespace)
	}
	if doc.Meta.Status != "active" {
		t.Errorf("Status = %q", doc.Meta.Status)
	}

	// Blocks.
	if len(doc.Blocks) != 3 {
		t.Fatalf("len(Blocks) = %d, want 3", len(doc.Blocks))
	}

	// Block 1: alerts_matcher
	b1 := doc.Blocks[0]
	if b1.Meta.Type != "alerts_matcher" {
		t.Errorf("block 1 type = %q", b1.Meta.Type)
	}
	if b1.Meta.BlockNumber != 1 {
		t.Errorf("block 1 number = %d", b1.Meta.BlockNumber)
	}
	if b1.Meta.RevisionNumber != 2 {
		t.Errorf("block 1 rev = %d", b1.Meta.RevisionNumber)
	}
	if b1.Meta.AuthorID != "incident_detector:123" {
		t.Errorf("block 1 author = %q", b1.Meta.AuthorID)
	}
	if !strings.Contains(b1.Content, "CrashLoopBackOff") {
		t.Errorf("block 1 content missing CrashLoopBackOff: %q", b1.Content)
	}

	// Block 2: markdown
	b2 := doc.Blocks[1]
	if b2.Meta.Type != "markdown" {
		t.Errorf("block 2 type = %q", b2.Meta.Type)
	}
	if b2.Meta.BlockNumber != 2 {
		t.Errorf("block 2 number = %d", b2.Meta.BlockNumber)
	}
	if b2.Meta.AuthorID != "agentic_run_analysis:456" {
		t.Errorf("block 2 author = %q", b2.Meta.AuthorID)
	}
	if !strings.Contains(b2.Content, "# Initial investigation") {
		t.Errorf("block 2 content missing heading: %q", b2.Content)
	}
	if !strings.Contains(b2.Content, "12:42") {
		t.Errorf("block 2 content missing timestamp 12:42: %q", b2.Content)
	}

	// Block 3: markdown
	b3 := doc.Blocks[2]
	if b3.Meta.Type != "markdown" {
		t.Errorf("block 3 type = %q", b3.Meta.Type)
	}
	if b3.Meta.BlockNumber != 3 {
		t.Errorf("block 3 number = %d", b3.Meta.BlockNumber)
	}
	if !strings.Contains(b3.Content, "oc get pods") {
		t.Errorf("block 3 content missing command: %q", b3.Content)
	}
}

func TestParseExampleFile(t *testing.T) {
	data, err := os.ReadFile("../../../docs/infrapad-markdown-example.md")
	if err != nil {
		t.Skipf("example file not available: %v", err)
	}
	doc, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if doc.Meta.DocID == "" {
		t.Error("expected non-empty doc ID")
	}
	if len(doc.Blocks) != 3 {
		t.Errorf("expected 3 blocks, got %d", len(doc.Blocks))
	}
}

func TestParseNoFrontmatter(t *testing.T) {
	input := "::infrapad_block{type=markdown block=1 rev=1}\nHello world\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if doc.Meta.DocID != "" {
		t.Errorf("expected empty doc ID, got %q", doc.Meta.DocID)
	}
	if len(doc.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(doc.Blocks))
	}
	if doc.Blocks[0].Content != "Hello world" {
		t.Errorf("content = %q", doc.Blocks[0].Content)
	}
}
