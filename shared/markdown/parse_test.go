package markdown_test

// Focused structural parsing checks: escaped directive attributes,
// directive examples inside body fences, header vs. ordinary YAML fences,
// framing rejections, and tolerant frontmatter-only reading.

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/infrapad/infrapad/shared/markdown"
)

func TestParseStructure(t *testing.T) {
	source := dedent(`
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
	if doc.Frontmatter.DocumentID != "doc-1" || len(doc.Blocks) != 2 {
		t.Fatalf("unexpected document: %+v", doc)
	}
	first, second := doc.Blocks[0], doc.Blocks[1]
	d := first.Directive
	if d.BlockNumber != 1 || d.Revision != 2 || d.Author != `Jane "J" Doe\x` || d.Parent == nil || *d.Parent != 3 ||
		d.Timestamp == nil || !d.Timestamp.Equal(time.Date(2026, 10, 25, 15, 40, 7, 88889000, time.UTC)) {
		t.Fatalf("unexpected directive: %+v", d)
	}
	wantHeader := map[string]any{"extras": map[string]any{"count": float64(2), "since": "2026-10-25T10:00:00Z"}}
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

func TestReadFrontmatter(t *testing.T) {
	valid := "\ufeff---\ndocument: doc-1\ntitle: Crash\nnamespace: payments\nstatus: active\nextra: ignored\n--- \r\n" +
		"::infrapad_block{block=1 rev=1 type=Unknown/Thing}\n```yaml infrapad\ncontent: [unterminated\n" +
		"::infrapad_block{block=oops\n"
	fm, err := markdown.ReadFrontmatter([]byte(valid))
	if err != nil {
		t.Fatalf("valid frontmatter: %v", err)
	}
	want := markdown.Frontmatter{DocumentID: "doc-1", Title: "Crash", Namespace: "payments", Status: "active"}
	if fm != want {
		t.Fatalf("frontmatter = %+v, want %+v", fm, want)
	}

	if _, err := markdown.ReadFrontmatter([]byte("---\ntitle: Crash\n---\nbody\n")); !errors.Is(err, markdown.ErrMissingDocumentID) {
		t.Fatalf("missing document ID: err = %v", err)
	}

	for name, src := range map[string]string{
		"malformed YAML": "---\ndocument: [doc-1\n---\n",
		"no frontmatter": "# Title\n",
		"unterminated":   "---\ndocument: doc-1\n",
	} {
		if _, err := markdown.ReadFrontmatter([]byte(src)); err == nil || errors.Is(err, markdown.ErrMissingDocumentID) {
			t.Errorf("%s: err = %v, want a frontmatter error", name, err)
		}
	}
}

// dedent removes the common leading tabs of a raw multi-line string and
// its first empty line.
func dedent(s string) string {
	s = strings.TrimPrefix(s, "\n")
	lines := strings.Split(s, "\n")
	prefix := ""
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := line[:len(line)-len(strings.TrimLeft(line, "\t"))]
		if prefix == "" || len(indent) < len(prefix) {
			prefix = indent
		}
	}
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, prefix)
	}
	return strings.Join(lines, "\n")
}
