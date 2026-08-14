package markdown

import (
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// ---------------------------------------------------------------------------
// AST node
// ---------------------------------------------------------------------------

// KindInfrapadBlock is the AST node kind for an ::infrapad_block leaf directive.
var KindInfrapadBlock = ast.NewNodeKind("InfrapadBlock")

// InfrapadBlock is the AST node produced by the leaf directive parser.
// It captures the directive attributes; the block's content is the source text
// between this node and the next InfrapadBlock (or EOF).
type InfrapadBlock struct {
	ast.BaseBlock
	Attrs map[string]string
	// ContentStart is the byte offset in the source where this block's
	// content begins (i.e. just past the directive line).
	ContentStart int
}

func (n *InfrapadBlock) Kind() ast.NodeKind { return KindInfrapadBlock }

func (n *InfrapadBlock) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, n.Attrs, nil)
}

// ---------------------------------------------------------------------------
// Goldmark extension
// ---------------------------------------------------------------------------

// InfrapadDirectives is the goldmark extension that adds the
// ::infrapad_block leaf directive parser.
var InfrapadDirectives goldmark.Extender = &infrapadDirectivesExtension{}

type infrapadDirectivesExtension struct{}

func (e *infrapadDirectivesExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithBlockParsers(
			util.Prioritized(&infrapadBlockParser{}, infrapadBlockParserPriority),
		),
	)
}

// ---------------------------------------------------------------------------
// Block parser
// ---------------------------------------------------------------------------

// infrapadBlockParser is a goldmark block parser that recognises
// ::infrapad_block{…} leaf directives.
//
// A leaf directive is a line that starts with exactly two colons, followed
// by the directive name and optional {key=value …} attributes. It must
// appear at the start of a line (up to 3 spaces of indentation).
type infrapadBlockParser struct{}

const directiveName = "infrapad_block"

// Priority sits above the paragraph parser so the directive line is claimed
// before it can be absorbed into a paragraph, but below the fenced-code
// parser so that directives inside code fences are left alone.
const infrapadBlockParserPriority = 799

func (p *infrapadBlockParser) Trigger() []byte { return []byte{':'} }

func (p *infrapadBlockParser) Open(_ ast.Node, reader text.Reader, _ parser.Context) (ast.Node, parser.State) {
	line, segment := reader.PeekLine()
	indent := countLeadingSpaces(line)
	if indent >= 4 {
		return nil, parser.NoChildren
	}
	rest := line[indent:]

	// Must start with exactly "::" (not ":::" which is a container directive).
	if len(rest) < 2 || rest[0] != ':' || rest[1] != ':' {
		return nil, parser.NoChildren
	}
	if len(rest) > 2 && rest[2] == ':' {
		return nil, parser.NoChildren
	}

	after := rest[2:]
	nameEnd := 0
	for nameEnd < len(after) && isNameByte(after[nameEnd]) {
		nameEnd++
	}
	name := string(after[:nameEnd])
	if name != directiveName {
		return nil, parser.NoChildren
	}

	tail := strings.TrimSpace(string(after[nameEnd:]))
	attrs := map[string]string{}
	if tail != "" {
		if !strings.HasPrefix(tail, "{") || !strings.HasSuffix(tail, "}") {
			return nil, parser.NoChildren
		}
		attrs = parseAttributes(tail[1 : len(tail)-1])
	}

	// ContentStart is the byte offset right after this directive line.
	contentStart := segment.Start + segment.Len()
	reader.Advance(segment.Len() - 1)
	return &InfrapadBlock{Attrs: attrs, ContentStart: contentStart}, parser.NoChildren
}

func (p *infrapadBlockParser) Continue(_ ast.Node, _ text.Reader, _ parser.Context) parser.State {
	// Leaf directive is a single line — always close immediately.
	return parser.Close
}

func (p *infrapadBlockParser) Close(_ ast.Node, _ text.Reader, _ parser.Context) {}

func (p *infrapadBlockParser) CanInterruptParagraph() bool { return true }

func (p *infrapadBlockParser) CanAcceptIndentedLine() bool { return false }

// ---------------------------------------------------------------------------
// Attribute parsing
// ---------------------------------------------------------------------------

// parseAttributes parses `key=value key="quoted" bare` into a map.
func parseAttributes(s string) map[string]string {
	attrs := map[string]string{}
	i := 0
	for i < len(s) {
		// skip whitespace
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			break
		}

		// read key
		keyStart := i
		for i < len(s) && s[i] != ' ' && s[i] != '\t' && s[i] != '=' {
			i++
		}
		key := s[keyStart:i]
		if key == "" {
			i++
			continue
		}

		// bare key (no =)
		if i >= len(s) || s[i] != '=' {
			attrs[key] = ""
			continue
		}
		i++ // consume '='

		// quoted value
		if i < len(s) && (s[i] == '"' || s[i] == '\'') {
			quote := s[i]
			i++
			valStart := i
			for i < len(s) && s[i] != quote {
				i++
			}
			attrs[key] = s[valStart:i]
			if i < len(s) {
				i++ // closing quote
			}
			continue
		}

		// unquoted value
		valStart := i
		for i < len(s) && s[i] != ' ' && s[i] != '\t' {
			i++
		}
		attrs[key] = s[valStart:i]
	}
	return attrs
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func isNameByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '-' || b == '_'
}

func countLeadingSpaces(line []byte) int {
	n := 0
	for n < len(line) && (line[n] == ' ' || line[n] == '\t') {
		n++
	}
	return n
}
