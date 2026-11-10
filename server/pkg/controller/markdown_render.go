package controller

// Server-side rendering of InfraPad Markdown.
//
// The host owns the document frontmatter, the ::infrapad_block directives
// (block identity, revision, type, provenance, and event time), the
// optional structured `yaml infrapad` header framing, and line breaks
// between blocks. Registered content types own the structured header
// fields (content.MarkdownHeader) and the Markdown body
// (ContentType.RenderMarkdown).
//
// The structural format (constants, framing types, and the strict parser
// used for pushed Markdown) lives in the shared
// github.com/infrapad/infrapad/shared/markdown package; the controller keeps
// the server-only rendering through registered content types.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/shared/markdown"
)

// renderDocumentMarkdown renders doc and its blocks (in the given order) as InfraPad
// Markdown. Callers select the blocks: export passes the latest live blocks
// in chronological order, with decoded records. Any failure fails the whole
// rendering with block/type context; no partial output is returned.
func renderDocumentMarkdown(doc model.Document) (string, error) {
	var out strings.Builder
	if err := writeFrontmatter(&out, doc); err != nil {
		return "", err
	}
	for _, blk := range doc.Blocks {
		if err := writeBlock(&out, blk); err != nil {
			return "", fmt.Errorf("render block %d (%s): %w", blk.BlockNumber, blk.Type, err)
		}
	}
	return out.String(), nil
}

func writeFrontmatter(out *strings.Builder, doc model.Document) error {
	data, err := encodeYAML(markdown.Frontmatter{
		DocumentID: string(doc.Uid),
		Title:      doc.Title,
		Namespace:  doc.Namespace,
		Status:     string(doc.Status),
	})
	if err != nil {
		return fmt.Errorf("render frontmatter: %w", err)
	}
	out.WriteString("---\n")
	out.WriteString(data)
	out.WriteString("---\n")
	return nil
}

func writeBlock(out *strings.Builder, blk model.Block) error {
	record := blk.Record
	if record == nil {
		return fmt.Errorf("no decoded record")
	}
	header, err := renderHeader(record)
	if err != nil {
		return err
	}
	body, err := record.ContentType().RenderMarkdown(record)
	if err != nil {
		return fmt.Errorf("render body: %w", err)
	}

	out.WriteString(renderDirective(blk))
	out.WriteString(header)
	out.WriteString(body)
	if body != "" && !strings.HasSuffix(body, "\n") {
		out.WriteString("\n")
	}
	return nil
}

// renderDirective returns the block directive line. Block status, generated
// keys, and collection membership are deliberately not exported.
func renderDirective(blk model.Block) string {
	attrs := []string{
		"block=" + strconv.Itoa(int(blk.BlockNumber)),
		"rev=" + strconv.Itoa(int(blk.RevisionNumber)),
		"type=" + directiveValue(blk.Type),
	}
	if blk.AuthorID != "" {
		attrs = append(attrs, "author="+directiveValue(string(blk.AuthorID)))
	}
	if blk.ParentBlockNumber != nil {
		attrs = append(attrs, "parent="+strconv.Itoa(int(*blk.ParentBlockNumber)))
	}
	attrs = append(attrs, "timestamp="+blk.Record.Timestamp().UTC().Format(time.RFC3339Nano))
	return "::" + markdown.DirectiveName + "{" + strings.Join(attrs, " ") + "}\n"
}

// directiveValue returns value unquoted when it is a plain token, otherwise
// double-quoted with backslash escapes for `"` and `\` (newlines escaped
// as \n so the directive stays on one line).
func directiveValue(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\r\n\"'\\{}=") {
		return value
	}
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`).Replace(value)
	return `"` + escaped + `"`
}

// renderHeader returns the structured header fence of record, or "" when
// its type has no header capability or projects no fields.
func renderHeader(record content.Record) (string, error) {
	mdHeaderProvider, ok := record.ContentType().(content.MarkdownHeader)
	if !ok {
		return "", nil
	}
	fields, err := mdHeaderProvider.MarkdownHeader(record)
	if err != nil {
		return "", fmt.Errorf("render header: %w", err)
	}
	if len(fields) == 0 {
		return "", nil
	}
	data, err := encodeYAML(map[string]any{markdown.HeaderContentKey: map[string]any(fields)})
	if err != nil {
		return "", fmt.Errorf("render header: %w", err)
	}
	fence := strings.Repeat("`", max(3, longestBacktickRun(data)+1))
	return fence + markdown.HeaderInfo + "\n" + data + fence + "\n", nil
}

func encodeYAML(value any) (string, error) {
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(value); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// longestBacktickRun lets the header fence outgrow any backtick run inside
// the YAML (e.g. code in a multi-line string).
func longestBacktickRun(s string) int {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}
