package markdown_test

// Focused structural parsing checks: escaped directive attributes,
// directive examples inside body fences, header vs. ordinary YAML fences,
// render/parse symmetry, and framing rejections.

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/utils"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
	"github.com/infrapad/infrapad/server/pkg/markdown"
	"github.com/infrapad/infrapad/server/pkg/model"
)

func TestParseStructure(t *testing.T) {
	source := utils.Dedent(`
		---
		document: doc-1
		title: t
		namespace: ""
		status: active
		---
		::infrapad_block{block=1 rev=2 type=Core/Markdown author="Jane \"J\" Doe\\x" parent=3 timestamp=2026-10-25T15:40:07.088889Z}
		` + "````yaml infrapad" + `
		content:
		  extras:
		    count: 2
		    since: 2026-10-25T10:00:00Z
		` + "````" + `
		Notes
		` + "```markdown" + `
		::infrapad_block{block=9 rev=1 type=Core/Markdown}
		` + "```" + `
		` + "```yaml" + `
		key: value
		` + "```" + `

		::infrapad_block{block=new type=Core/Markdown}
		New text
	`)
	doc, err := markdown.Parse(source)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Frontmatter.Document != "doc-1" || len(doc.Blocks) != 2 {
		t.Fatalf("unexpected document: %+v", doc)
	}
	first, second := doc.Blocks[0], doc.Blocks[1]
	d := first.Directive
	if d.BlockNumber != 1 || d.Revision != 2 || d.Author != `Jane "J" Doe\x` || d.Parent == nil || *d.Parent != 3 ||
		d.Timestamp == nil || !d.Timestamp.Equal(time.Date(2026, 10, 25, 15, 40, 7, 88889000, time.UTC)) {
		t.Fatalf("unexpected directive: %+v", d)
	}
	wantHeader := content.MarkdownFields{"extras": map[string]any{"count": float64(2), "since": "2026-10-25T10:00:00Z"}}
	if !first.HasHeader || !reflect.DeepEqual(first.Header, wantHeader) {
		t.Fatalf("unexpected header: %#v", first.Header)
	}
	wantBody := "Notes\n```markdown\n::infrapad_block{block=9 rev=1 type=Core/Markdown}\n```\n```yaml\nkey: value\n```\n\n"
	if first.Body != wantBody {
		t.Fatalf("unexpected body: %q", first.Body)
	}
	if !second.Directive.New || second.Directive.Type != "Core/Markdown" || second.HasHeader || strings.TrimSpace(second.Body) != "New text" || second.Index != 2 {
		t.Fatalf("unexpected new block: %+v", second)
	}
}

// TestParseRenderedDocument checks that rendered output parses back into
// the same directive values, headers, and bodies.
func TestParseRenderedDocument(t *testing.T) {
	ts := time.Date(2026, 10, 25, 15, 40, 7, 0, time.UTC)
	markdownType := core.NewMarkdownType()
	rendered, err := markdown.RenderDocument(model.Document{
		Uid: "doc-1", Title: "t", Status: model.ActiveDocument,
		Blocks: []model.Block{{BlockNumber: 4, RevisionNumber: 1, Type: core.MarkdownTypeName, AuthorID: "a b\n\"c\"",
			Record: content.NewItem(markdownType, ts, core.Markdown{Text: "x", Extras: map[string]any{"k": "v"}})}},
	})
	if err != nil {
		t.Fatalf("RenderDocument: %v", err)
	}
	doc, err := markdown.Parse(rendered)
	if err != nil {
		t.Fatalf("Parse: %v\n%s", err, rendered)
	}
	blk := doc.Blocks[0]
	if blk.Directive.Author != "a b\n\"c\"" || !blk.Directive.Timestamp.Equal(ts) ||
		!reflect.DeepEqual(blk.Header, content.MarkdownFields{"extras": map[string]any{"k": "v"}}) || blk.Body != "x\n" {
		t.Fatalf("unexpected round trip: %+v", blk)
	}
}

func TestParseRejections(t *testing.T) {
	front := "---\ndocument: doc-1\n---\n"
	cases := map[string]string{
		"missing frontmatter":   "::infrapad_block{block=1 rev=1 type=Core/Markdown}\n",
		"unknown frontmatter":   "---\ndocument: d\nowner: x\n---\n",
		"content outside block": front + "stray\n",
		"unsupported attribute": front + "::infrapad_block{block=1 rev=1 type=Core/Markdown status=published}\n",
		"duplicate attribute":   front + "::infrapad_block{block=1 rev=1 rev=2 type=Core/Markdown}\n",
		"duplicate block":       front + "::infrapad_block{block=1 rev=1 type=Core/Markdown}\n::infrapad_block{block=1 rev=1 type=Core/Markdown}\n",
		"missing rev":           front + "::infrapad_block{block=1 type=Core/Markdown}\n",
		"new with rev":          front + "::infrapad_block{block=new rev=1 type=Core/Markdown}\n",
		"bad escape":            front + "::infrapad_block{block=1 rev=1 type=Core/Markdown author=\"a\\t\"}\n",
		"unknown header field":  front + "::infrapad_block{block=1 rev=1 type=Core/Markdown}\n```yaml infrapad\nextras: {}\n```\n",
		"unterminated header":   front + "::infrapad_block{block=1 rev=1 type=Core/Markdown}\n```yaml infrapad\ncontent: {}\n",
	}
	for name, source := range cases {
		if _, err := markdown.Parse(source); err == nil {
			t.Errorf("%s: expected a parse error", name)
		} else if !strings.Contains(err.Error(), "line") && name != "unknown frontmatter" {
			t.Errorf("%s: expected line context, got %v", name, err)
		}
	}
}
