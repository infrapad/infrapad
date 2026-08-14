package markdown

import (
	"strings"
	"testing"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestRender(t *testing.T) {
	doc := &pb.Doc{
		Name:      "docs/796756c0-1829-41ae-91c2-3cdc531e3d59",
		Title:     "Payment service crash loop",
		Namespace: "payments",
		Status:    "active",
	}

	alertsContent, _ := structpb.NewStruct(map[string]any{
		"LabelsMatchers": []any{
			map[string]any{"name": []any{"CrashLoopBackOff"}},
			map[string]any{"name": []any{"KubeNodeNotReady"}},
		},
	})

	mdContent, _ := structpb.NewStruct(map[string]any{
		"text": "# Initial investigation\n\nThis incident started at 2026-07-12 12:42 by CrashLoopBackOff...\n",
	})

	blocks := []*pb.Block{
		{
			BlockNumber:    1,
			RevisionNumber: 2,
			AuthorId:       "incident_detector:123",
			Type:           "alerts_matcher",
			Content:        alertsContent,
		},
		{
			BlockNumber:    2,
			RevisionNumber: 2,
			AuthorId:       "agentic_run_analysis:456",
			Type:           "markdown",
			Content:        mdContent,
		},
	}

	out, err := Render(doc, blocks)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}

	// Frontmatter.
	if !strings.Contains(out, "doc: 796756c0-1829-41ae-91c2-3cdc531e3d59") {
		t.Error("missing doc ID in frontmatter")
	}
	if !strings.Contains(out, "title: Payment service crash loop") {
		t.Error("missing title in frontmatter")
	}
	if !strings.Contains(out, "namespace: payments") {
		t.Error("missing namespace in frontmatter")
	}
	if !strings.Contains(out, "status: active") {
		t.Error("missing status in frontmatter")
	}

	// Block 1 directive.
	if !strings.Contains(out, "::infrapad_block{block=1 rev=2 type=alerts_matcher author=incident_detector:123}") {
		t.Error("missing block 1 directive")
	}
	// Block 1 content in a yaml code fence.
	if !strings.Contains(out, "```yaml\n") {
		t.Error("missing yaml code fence for alerts_matcher block")
	}
	if !strings.Contains(out, "CrashLoopBackOff") {
		t.Error("missing CrashLoopBackOff in alerts_matcher block content")
	}

	// Block 2 directive.
	if !strings.Contains(out, "::infrapad_block{block=2 rev=2 type=markdown author=agentic_run_analysis:456}") {
		t.Error("missing block 2 directive")
	}
	// Block 2 content (inline markdown, no code fence).
	if !strings.Contains(out, "# Initial investigation") {
		t.Error("missing markdown content")
	}

	// Round-trip: parse the rendered output and verify structure.
	parsed, err := Parse([]byte(out))
	if err != nil {
		t.Fatalf("Parse of rendered output failed: %v", err)
	}
	if parsed.Meta.DocID != "796756c0-1829-41ae-91c2-3cdc531e3d59" {
		t.Errorf("round-trip DocID = %q", parsed.Meta.DocID)
	}
	if len(parsed.Blocks) != 2 {
		t.Fatalf("round-trip got %d blocks, want 2", len(parsed.Blocks))
	}
	if parsed.Blocks[0].Meta.Type != "alerts_matcher" {
		t.Errorf("round-trip block 0 type = %q", parsed.Blocks[0].Meta.Type)
	}
	if parsed.Blocks[1].Meta.Type != "markdown" {
		t.Errorf("round-trip block 1 type = %q", parsed.Blocks[1].Meta.Type)
	}
	if text, _ := parsed.Blocks[1].Content["text"].(string); !strings.Contains(text, "# Initial investigation") {
		t.Errorf("round-trip block 1 content = %v", parsed.Blocks[1].Content)
	}
}

func TestRenderNoAuthor(t *testing.T) {
	doc := &pb.Doc{
		Name:      "docs/abc",
		Title:     "Test",
		Namespace: "ns",
		Status:    "active",
	}
	content, _ := structpb.NewStruct(map[string]any{"text": "hello\n"})
	blocks := []*pb.Block{
		{
			BlockNumber:    1,
			RevisionNumber: 1,
			Type:           "markdown",
			Content:        content,
		},
	}

	out, err := Render(doc, blocks)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	// Should not have author= in the directive.
	if strings.Contains(out, "author=") {
		t.Error("should not include author= when author is empty")
	}
	if !strings.Contains(out, "::infrapad_block{block=1 rev=1 type=markdown}") {
		t.Errorf("unexpected directive line in: %s", out)
	}
}
