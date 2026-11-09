package markdown

import (
	"errors"
	"testing"
)

func TestReadFrontmatter(t *testing.T) {
	t.Parallel()

	valid := "---\ndocument: doc-1\ntitle: Crash\nnamespace: payments\nstatus: active\nextra: ignored\n---\n" +
		"::infrapad_block{block=1 rev=1 type=Unknown/Thing}\n```yaml infrapad\ncontent: [unterminated\n" +
		"::infrapad_block{block=oops\n"
	meta, err := ReadFrontmatter([]byte(valid))
	if err != nil {
		t.Fatalf("valid frontmatter: %v", err)
	}
	want := DocumentMeta{DocumentID: "doc-1", Title: "Crash", Namespace: "payments", Status: "active"}
	if meta != want {
		t.Fatalf("meta = %+v, want %+v", meta, want)
	}

	if _, err := ReadFrontmatter([]byte("---\ntitle: Crash\n---\nbody\n")); !errors.Is(err, ErrMissingDocumentID) {
		t.Fatalf("missing document ID: err = %v", err)
	}

	for name, src := range map[string]string{
		"malformed YAML": "---\ndocument: [doc-1\n---\n",
		"no frontmatter": "# Title\n",
		"unterminated":   "---\ndocument: doc-1\n",
	} {
		if _, err := ReadFrontmatter([]byte(src)); err == nil || errors.Is(err, ErrMissingDocumentID) {
			t.Errorf("%s: err = %v, want a frontmatter error", name, err)
		}
	}
}
