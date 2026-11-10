package markdown

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ErrMissingDocumentID reports frontmatter without a document ID, both from
// ReadFrontmatter and (wrapped) from Parse.
var ErrMissingDocumentID = errors.New("missing document ID")

// ReadFrontmatter reads only the leading YAML frontmatter of an InfraPad
// Markdown document, for document discovery.
//
// Unlike Parse, it is tolerant: the rest of the document (directives,
// headers, bodies) is never interpreted, so unknown content types or
// malformed blocks do not prevent reading valid frontmatter. Unknown
// frontmatter keys are ignored, a leading UTF-8 BOM is accepted, and
// delimiter lines may carry trailing whitespace. A missing document ID is
// reported as ErrMissingDocumentID together with the other fields read.
func ReadFrontmatter(src []byte) (Frontmatter, error) {
	lines := splitLines(strings.TrimPrefix(string(src), "\ufeff"))
	data, _, err := scanFrontmatter(lines, isTolerantDelimiter)
	if err != nil {
		return Frontmatter{}, err
	}
	fm, err := decodeFrontmatter(data, false)
	if err != nil {
		return Frontmatter{}, err
	}
	if fm.DocumentID == "" {
		return fm, ErrMissingDocumentID
	}
	return fm, nil
}

// parseFrontmatter strictly parses the frontmatter at the start of lines
// for Parse, returning the index of the first line after it.
func parseFrontmatter(lines []string) (Frontmatter, int, error) {
	data, next, err := scanFrontmatter(lines, isStrictDelimiter)
	if err != nil {
		return Frontmatter{}, 0, &ParseError{Line: 1, Err: err}
	}
	fm, err := decodeFrontmatter(data, true)
	if err != nil {
		return Frontmatter{}, 0, &ParseError{Line: 2, Err: err}
	}
	if fm.DocumentID == "" {
		return Frontmatter{}, 0, &ParseError{Line: 1, Err: fmt.Errorf("frontmatter: %w", ErrMissingDocumentID)}
	}
	return fm, next, nil
}

// scanFrontmatter returns the YAML between the leading delimiter line and
// the next one, plus the index of the line after the closing delimiter.
func scanFrontmatter(lines []string, isDelimiter func(string) bool) (string, int, error) {
	if len(lines) == 0 || !isDelimiter(lines[0]) {
		return "", 0, errors.New("document must start with a `---` YAML frontmatter")
	}
	for n := 1; n < len(lines); n++ {
		if isDelimiter(lines[n]) {
			return strings.Join(lines[1:n], ""), n + 1, nil
		}
	}
	return "", 0, errors.New("unterminated frontmatter")
}

// decodeFrontmatter decodes frontmatter YAML; strict decoding rejects
// unknown keys.
func decodeFrontmatter(data string, strict bool) (Frontmatter, error) {
	var fm Frontmatter
	decoder := yaml.NewDecoder(strings.NewReader(data))
	decoder.KnownFields(strict)
	if err := decoder.Decode(&fm); err != nil && !errors.Is(err, io.EOF) {
		return Frontmatter{}, fmt.Errorf("frontmatter: %w", err)
	}
	return fm, nil
}

func isStrictDelimiter(line string) bool {
	return strings.TrimRight(line, "\n") == "---"
}

func isTolerantDelimiter(line string) bool {
	return strings.TrimRight(line, " \t\r\n") == "---"
}

// splitLines splits source into lines retaining their "\n" terminators;
// CRLF line endings are normalized to LF.
func splitLines(source string) []string {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	lines := strings.SplitAfter(source, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
