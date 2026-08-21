package markdown

import (
	"os"
	"strings"
	"testing"
)

const testInput = `---
document: 42cd704a-f697-4a78-9c29-3c7235c9500f
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
	if doc.Meta.DocumentID != "42cd704a-f697-4a78-9c29-3c7235c9500f" {
		t.Errorf("DocumentID = %q", doc.Meta.DocumentID)
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
	// Non-markdown blocks should have their YAML content parsed into a map.
	matchers, ok := b1.Content["LabelsMatchers"]
	if !ok {
		t.Fatalf("block 1 content missing LabelsMatchers key: %v", b1.Content)
	}
	matcherList, ok := matchers.([]any)
	if !ok || len(matcherList) != 2 {
		t.Errorf("block 1 LabelsMatchers = %v, want 2-element list", matchers)
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
	b2Text, _ := b2.Content["text"].(string)
	if !strings.Contains(b2Text, "# Initial investigation") {
		t.Errorf("block 2 content missing heading: %q", b2Text)
	}
	if !strings.Contains(b2Text, "12:42") {
		t.Errorf("block 2 content missing timestamp 12:42: %q", b2Text)
	}

	// Block 3: markdown
	b3 := doc.Blocks[2]
	if b3.Meta.Type != "markdown" {
		t.Errorf("block 3 type = %q", b3.Meta.Type)
	}
	if b3.Meta.BlockNumber != 3 {
		t.Errorf("block 3 number = %d", b3.Meta.BlockNumber)
	}
	b3Text, _ := b3.Content["text"].(string)
	if !strings.Contains(b3Text, "oc get pods") {
		t.Errorf("block 3 content missing command: %q", b3Text)
	}
	// Markdown blocks keep their embedded code fences.
	if !strings.Contains(b3Text, "```") {
		t.Errorf("block 3 content should preserve code fence: %q", b3Text)
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
	if doc.Meta.DocumentID == "" {
		t.Error("expected non-empty document ID")
	}
	if len(doc.Blocks) != 3 {
		t.Errorf("expected 3 blocks, got %d", len(doc.Blocks))
	}
}

func TestParseNewBlock(t *testing.T) {
	input := `---
document: test-doc-id
title: Test
namespace: test
status: active
---
::infrapad_block{type=markdown block=1 rev=1}
Existing content

::infrapad_block{block=new type=markdown}

# New block heading

New block content.
`
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(doc.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(doc.Blocks))
	}

	// Block 1: existing block
	b1 := doc.Blocks[0]
	if b1.Meta.IsNew {
		t.Error("block 1 should not be new")
	}
	if b1.Meta.BlockNumber != 1 {
		t.Errorf("block 1 number = %d, want 1", b1.Meta.BlockNumber)
	}

	// Block 2: new block
	b2 := doc.Blocks[1]
	if !b2.Meta.IsNew {
		t.Error("block 2 should be new")
	}
	if b2.Meta.BlockNumber != 0 {
		t.Errorf("new block number = %d, want 0", b2.Meta.BlockNumber)
	}
	if b2.Meta.Type != "markdown" {
		t.Errorf("new block type = %q, want markdown", b2.Meta.Type)
	}
	b2Text, _ := b2.Content["text"].(string)
	if !strings.Contains(b2Text, "# New block heading") {
		t.Errorf("new block content missing heading: %q", b2Text)
	}
	if !strings.Contains(b2Text, "New block content.") {
		t.Errorf("new block content missing body: %q", b2Text)
	}
}

func TestParseNoFrontmatter(t *testing.T) {
	input := "::infrapad_block{type=markdown block=1 rev=1}\nHello world\n"
	doc, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if doc.Meta.DocumentID != "" {
		t.Errorf("expected empty document ID, got %q", doc.Meta.DocumentID)
	}
	if len(doc.Blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(doc.Blocks))
	}
	if text, _ := doc.Blocks[0].Content["text"].(string); text != "Hello world\n" {
		t.Errorf("content = %q", doc.Blocks[0].Content)
	}
}
