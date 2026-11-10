package controller

// Focused format checks for the host-owned framing: YAML quoting, header
// projection vs. ordinary YAML body fences, directive quoting, and
// whole-document failure. The Slack/agent export scenario lives in the
// e2e payload submission test.

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/utils"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
	"github.com/infrapad/infrapad/server/pkg/extensions/prometheus"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/shared/markdown"
)

func TestRenderDocumentFormat(t *testing.T) {
	ts := time.Date(2026, 10, 25, 15, 40, 7, 88889000, time.UTC)
	markdownType := core.NewMarkdownType()
	alertsType := prometheus.NewAlertsMatcherType()
	parent := model.BlockNumber(1)

	doc := model.Document{
		Uid:    "doc-1",
		Title:  `Fix: "quoted" #1`,
		Status: model.ActiveDocument,
		Blocks: []model.Block{
			{BlockNumber: 1, RevisionNumber: 2, Type: core.MarkdownTypeName, AuthorID: "Jane Doe",
				Record: content.NewItem(markdownType, ts, core.Markdown{
					Text:   "Notes\n```yaml\nkey: value\n```",
					Extras: map[string]any{"source": "x: y"},
				})},
			{BlockNumber: 2, RevisionNumber: 1, Type: prometheus.AlertsMatcherTypeName, ParentBlockNumber: &parent,
				Record: content.NewItem(alertsType, ts, prometheus.AlertsMatcher{
					LabelsMatchers: []map[string][]string{{"alertname": {"Down"}}},
					Since:          ts,
				})},
			{BlockNumber: 3, RevisionNumber: 1, Type: core.MarkdownTypeName,
				Record: content.NewItem(markdownType, ts, core.Markdown{Text: "plain\n"})},
		},
	}
	got, err := renderDocumentMarkdown(doc)
	if err != nil {
		t.Fatalf("renderDocumentMarkdown: %v", err)
	}
	want := utils.Dedent(`
		---
		document: doc-1
		title: 'Fix: "quoted" #1'
		namespace: ""
		status: active
		---
		::infrapad_block{block=1 rev=2 type=Core/Markdown author="Jane Doe" timestamp=2026-10-25T15:40:07.088889Z}
		` + "```yaml infrapad" + `
		content:
		  extras:
		    source: 'x: y'
		` + "```" + `
		Notes
		` + "```yaml" + `
		key: value
		` + "```" + `
		::infrapad_block{block=2 rev=1 type=Prometheus/AlertsMatcher parent=1 timestamp=2026-10-25T15:40:07.088889Z}
		` + "```yaml infrapad" + `
		content:
		  labels_matchers:
		    - alertname:
		        - Down
		  since: "2026-10-25T15:40:07.088889Z"
		` + "```" + `
		::infrapad_block{block=3 rev=1 type=Core/Markdown timestamp=2026-10-25T15:40:07.088889Z}
		plain
		`)
	if got != want {
		t.Fatalf("unexpected Markdown:\n%s\nwant:\n%s", got, want)
	}

	// A body rendering failure fails the whole export with block context.
	doc.Blocks = append(doc.Blocks, model.Block{BlockNumber: 4, RevisionNumber: 1, Type: "Test/Broken",
		Record: content.NewItem[any](brokenType{}, ts, nil)})
	if out, err := renderDocumentMarkdown(doc); err == nil || out != "" ||
		!strings.Contains(err.Error(), "block 4 (Test/Broken)") || !errors.Is(err, errBroken) {
		t.Fatalf("expected a block 4 rendering failure without output, got %q, %v", out, err)
	}
}

// TestParseRenderedDocument checks that rendered output parses back into
// the same directive values, headers, and bodies. The grammar itself is
// covered in shared/markdown.
func TestParseRenderedDocument(t *testing.T) {
	ts := time.Date(2026, 10, 25, 15, 40, 7, 0, time.UTC)
	markdownType := core.NewMarkdownType()
	rendered, err := renderDocumentMarkdown(model.Document{
		Uid: "doc-1", Title: "t", Status: model.ActiveDocument,
		Blocks: []model.Block{{BlockNumber: 4, RevisionNumber: 1, Type: core.MarkdownTypeName, AuthorID: "a b\n\"c\"",
			Record: content.NewItem(markdownType, ts, core.Markdown{Text: "x", Extras: map[string]any{"k": "v"}})}},
	})
	if err != nil {
		t.Fatalf("renderDocumentMarkdown: %v", err)
	}
	doc, err := markdown.Parse(rendered)
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, rendered)
	}
	blk := doc.Blocks[0]
	if blk.Directive.Author != "a b\n\"c\"" || !blk.Directive.Timestamp.Equal(ts) ||
		!reflect.DeepEqual(blk.Header, map[string]any{"extras": map[string]any{"k": "v"}}) || blk.Body != "x\n" {
		t.Fatalf("unexpected round trip: %+v", blk)
	}
}

var errBroken = errors.New("broken")

// brokenType is a content type whose body rendering always fails.
type brokenType struct{}

func (brokenType) Name() string                                        { return "Test/Broken" }
func (brokenType) ContentSchema() content.Schema                       { return content.Schema{} }
func (brokenType) DecodeJSON(json.RawMessage) (content.Content, error) { return nil, nil }
func (brokenType) RenderMarkdown(content.Record) (string, error)       { return "", errBroken }
