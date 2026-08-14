package markdown

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	meta "github.com/yuin/goldmark-meta"
)

// DocMeta holds document-level metadata from the YAML frontmatter.
type DocMeta struct {
	DocID     string
	Title     string
	Namespace string
	Status    string
}

// BlockMeta holds per-block metadata from the ::infrapad_block directive.
type BlockMeta struct {
	Type         string
	BlockNumber  int
	RevisionNumber int
	AuthorID     string
}

// ParsedBlock is a block extracted from the infrapad markdown.
type ParsedBlock struct {
	Meta    BlockMeta
	Content string // raw content between directives (trimmed of surrounding blank lines)

	contentStart int // unexported; used during parsing to track byte offset
}

// ParsedDoc is the result of parsing an infrapad markdown file.
type ParsedDoc struct {
	Meta   DocMeta
	Blocks []ParsedBlock
}

// Parse parses an infrapad-flavoured markdown document and returns the
// document metadata and ordered list of blocks.
func Parse(src []byte) (*ParsedDoc, error) {
	// Parse with goldmark-meta (frontmatter) + our directive extension.
	md := goldmark.New(goldmark.WithExtensions(meta.Meta, InfrapadDirectives))
	ctx := parser.NewContext()
	reader := text.NewReader(src)
	tree := md.Parser().Parse(reader, parser.WithContext(ctx))

	doc := &ParsedDoc{
		Meta: docMetaFromMap(meta.Get(ctx)),
	}

	// Walk the AST to collect blocks. Each directive starts a new block;
	// its content runs until the next directive (or end of file).
	var prevBlock *ParsedBlock

	_ = ast.Walk(tree, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		ib, ok := n.(*InfrapadBlock)
		if !ok {
			return ast.WalkContinue, nil
		}

		// Close previous block: its content is src[prevStart:thisDirectiveLine].
		if prevBlock != nil {
			contentEnd := lineStart(src, ib.ContentStart)
			prevBlock.Content = trimBlankLines(string(src[prevBlock.contentStart:contentEnd]))
		}

		bm, err := blockMetaFromAttrs(ib.Attrs)
		if err != nil {
			return ast.WalkStop, fmt.Errorf("block %d: %w", len(doc.Blocks)+1, err)
		}

		doc.Blocks = append(doc.Blocks, ParsedBlock{Meta: bm, contentStart: ib.ContentStart})
		prevBlock = &doc.Blocks[len(doc.Blocks)-1]

		return ast.WalkContinue, nil
	})

	// Close the last block: content runs to end of source.
	if prevBlock != nil {
		prevBlock.Content = trimBlankLines(string(src[prevBlock.contentStart:]))
	}

	return doc, nil
}

// docMetaFromMap converts the frontmatter map to DocMeta.
func docMetaFromMap(m map[string]interface{}) DocMeta {
	var dm DocMeta
	if v, ok := m["doc"].(string); ok {
		dm.DocID = v
	}
	if v, ok := m["title"].(string); ok {
		dm.Title = v
	}
	if v, ok := m["namespace"].(string); ok {
		dm.Namespace = v
	}
	if v, ok := m["status"].(string); ok {
		dm.Status = v
	}
	return dm
}

// blockMetaFromAttrs converts directive attributes to BlockMeta.
func blockMetaFromAttrs(attrs map[string]string) (BlockMeta, error) {
	bm := BlockMeta{
		Type:     attrs["type"],
		AuthorID: attrs["author"],
	}

	if v, ok := attrs["block"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return bm, fmt.Errorf("invalid block number %q: %w", v, err)
		}
		bm.BlockNumber = n
	}

	if v, ok := attrs["rev"]; ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return bm, fmt.Errorf("invalid revision number %q: %w", v, err)
		}
		bm.RevisionNumber = n
	}

	return bm, nil
}

// lineStart returns the byte offset of the start of the line containing pos.
func lineStart(src []byte, pos int) int {
	i := pos - 1
	for i > 0 && src[i-1] != '\n' {
		i--
	}
	return i
}

// trimBlankLines removes leading and trailing blank lines from s.
func trimBlankLines(s string) string {
	lines := strings.Split(s, "\n")

	// Trim leading blank lines.
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	// Trim trailing blank lines.
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}

	if start >= end {
		return ""
	}
	return strings.Join(lines[start:end], "\n")
}
