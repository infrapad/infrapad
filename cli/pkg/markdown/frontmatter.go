package markdown

import (
	"bytes"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// ErrMissingDocumentID is returned by ReadFrontmatter when the frontmatter
// does not carry a document ID.
var ErrMissingDocumentID = errors.New("frontmatter has no document ID")

// ReadFrontmatter reads only the leading YAML frontmatter of an InfraPad
// Markdown document and returns its document metadata.
//
// The rest of the document (block directives, structured headers, bodies) is
// deliberately not interpreted: unknown content types or malformed block
// content never prevent reading valid frontmatter. Unknown frontmatter keys are
// ignored. A missing document ID is reported as ErrMissingDocumentID.
func ReadFrontmatter(src []byte) (DocumentMeta, error) {
	data, err := frontmatterBytes(src)
	if err != nil {
		return DocumentMeta{}, err
	}

	var fields struct {
		Document  string `yaml:"document"`
		Title     string `yaml:"title"`
		Namespace string `yaml:"namespace"`
		Status    string `yaml:"status"`
	}
	if err := yaml.Unmarshal(data, &fields); err != nil {
		return DocumentMeta{}, fmt.Errorf("parse frontmatter: %w", err)
	}

	meta := DocumentMeta{
		DocumentID: fields.Document,
		Title:      fields.Title,
		Namespace:  fields.Namespace,
		Status:     fields.Status,
	}
	if meta.DocumentID == "" {
		return meta, ErrMissingDocumentID
	}
	return meta, nil
}

// frontmatterBytes returns the YAML between the leading `---` delimiter line
// and the next `---` line.
func frontmatterBytes(src []byte) ([]byte, error) {
	src = bytes.TrimPrefix(src, []byte("\ufeff"))
	first, _, found := bytes.Cut(src, []byte("\n"))
	if !found || !isDelimiter(first) {
		return nil, errors.New("document must start with a `---` YAML frontmatter")
	}

	start := len(first) + 1
	for pos := start; pos < len(src); {
		end := bytes.IndexByte(src[pos:], '\n')
		if end < 0 {
			end = len(src) - pos
		}
		if isDelimiter(src[pos : pos+end]) {
			return src[start:pos], nil
		}
		pos += end + 1
	}
	return nil, errors.New("unterminated frontmatter")
}

func isDelimiter(line []byte) bool {
	return string(bytes.TrimRight(line, " \t\r")) == "---"
}
