package markdown

import (
	"fmt"
	"strconv"
	"github.com/yuin/goldmark"
	meta "github.com/yuin/goldmark-meta"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
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
	Type           string
	BlockNumber    int
	RevisionNumber int
	AuthorID       string
}

// ParsedBlock is a block extracted from the infrapad markdown.
type ParsedBlock struct {
	Meta    BlockMeta
	Content string // raw content between directives (trimmed of surrounding blank lines)
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

	// Iterate over the document's top-level children. Each InfrapadBlock
	// directive opens a new block; subsequent sibling nodes are its content.
	var current *ParsedBlock
	var contentStart int // byte offset where current block's content begins

	finalizeCurrentBlock := func(contentEnd int) {
		if current == nil {
			return
		}
		if current.Meta.Type == "markdown" {
			current.Content = string(src[contentStart:contentEnd])
		}
	}

	for child := tree.FirstChild(); child != nil; child = child.NextSibling() {
		if ib, ok := child.(*InfrapadBlock); ok {
			// Attach the content to the previous block before starting the new one.
			finalizeCurrentBlock(lineStart(src, ib.ContentStart))
			bm, err := blockMetaFromAttrs(ib.Attrs)
			if err != nil {
				return nil, fmt.Errorf("block %d: %w", len(doc.Blocks)+1, err)
			}
			doc.Blocks = append(doc.Blocks, ParsedBlock{Meta: bm})
			current = &doc.Blocks[len(doc.Blocks)-1]
			contentStart = ib.ContentStart
			continue
		}
		if current == nil {
			continue
		}

		// For non-markdown blocks, the infrapad format wraps content in a
		// code fence for rendering. If the first sibling is a FencedCodeBlock,
		// extract just its inner lines — the fence is a format detail, not
		// actual content. Any remaining siblings (decorative) are ignored.
		if current.Meta.Type != "markdown" {
			if fcb, ok := child.(*ast.FencedCodeBlock); ok && current.Content == "" {
				current.Content = string(fcb.Lines().Value(src))
			}
		}
	}
	finalizeCurrentBlock(len(src))

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


