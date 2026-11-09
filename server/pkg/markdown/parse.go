package markdown

// Structural parsing of pushed InfraPad Markdown. Parsing is purely
// structural: it separates the frontmatter, directive attributes, optional
// structured header fields, and raw body text without interpreting any
// extension-specific content. Host semantics (baselines, ownership,
// capabilities) belong to the caller.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/infrapad/infrapad/server/pkg/content"
)

// NewBlockValue is the `block` directive value of a block to be created.
const NewBlockValue = "new"

// Document is a structurally parsed InfraPad Markdown document.
type Document struct {
	Frontmatter Frontmatter
	Blocks      []Block
}

// Frontmatter is the parsed document-level YAML header.
type Frontmatter struct {
	Document  string `yaml:"document"`
	Title     string `yaml:"title"`
	Namespace string `yaml:"namespace"`
	Status    string `yaml:"status"`
}

// Block is one parsed ::infrapad_block section.
type Block struct {
	// Index is the 1-based position of the block in the document.
	Index int
	// Line is the 1-based line number of the directive.
	Line      int
	Directive Directive
	// HasHeader reports whether a `yaml infrapad` header fence follows
	// the directive (possibly with empty fields).
	HasHeader bool
	// Header is the mapping nested under `content:`, normalized to the
	// JSON-compatible data model; nil when absent or empty.
	Header content.MarkdownFields
	// Body is the raw body text up to the next directive (or the end of
	// the document), including its line breaks.
	Body string
}

// Directive holds the parsed attributes of a block directive. Existing
// blocks carry BlockNumber and Revision; new blocks (`block=new`) carry
// only Type.
type Directive struct {
	New         bool
	BlockNumber int
	Revision    int
	Type        string
	Author      string
	Parent      *int
	Timestamp   *time.Time
}

// ParseError is a structural parsing failure with its source line (0 when
// not attributable to a line) and block position (0 outside blocks).
type ParseError struct {
	Line       int
	BlockIndex int
	Err        error
}

func (e *ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %v", e.Line, e.Err)
	}
	return e.Err.Error()
}

func (e *ParseError) Unwrap() error { return e.Err }

// Parse structurally parses an InfraPad Markdown document. It rejects
// malformed framing, non-blank content outside blocks, duplicate existing
// block numbers, unsupported or duplicate directive attributes, and
// unknown header-envelope fields. Directives are recognized only outside
// fenced code; only a `yaml infrapad` fence immediately following a
// directive is a structured header, other fences are body text.
func Parse(source string) (Document, error) {
	lines := splitLines(source)
	var doc Document
	n, err := parseFrontmatter(lines, &doc.Frontmatter)
	if err != nil {
		return Document{}, err
	}

	seen := map[int]int{}
	var current *Block
	var body strings.Builder
	var fence *fenceMarker
	flush := func() {
		if current != nil {
			current.Body = body.String()
			doc.Blocks = append(doc.Blocks, *current)
		}
		body.Reset()
	}
	for n < len(lines) {
		line := lines[n]
		lineNo := n + 1
		if fence == nil {
			if attrs, ok := directiveAttributes(line); ok {
				flush()
				index := len(doc.Blocks) + 1
				fail := func(err error) error { return &ParseError{Line: lineNo, BlockIndex: index, Err: err} }
				directive, err := parseDirective(attrs)
				if err != nil {
					return Document{}, fail(err)
				}
				if !directive.New {
					if prev, dup := seen[directive.BlockNumber]; dup {
						return Document{}, fail(fmt.Errorf("duplicate block %d (also at line %d)", directive.BlockNumber, prev))
					}
					seen[directive.BlockNumber] = lineNo
				}
				current = &Block{Index: index, Line: lineNo, Directive: directive}
				n++
				if n < len(lines) {
					if open, ok := openFence(lines[n]); ok && open.info == HeaderInfo {
						end, fields, err := parseHeader(lines, n, open)
						if err != nil {
							return Document{}, &ParseError{Line: n + 1, BlockIndex: index, Err: err}
						}
						current.HasHeader, current.Header = true, fields
						n = end
					}
				}
				continue
			}
			if open, ok := openFence(line); ok {
				fence = &open
			}
		} else if closesFence(line, *fence) {
			fence = nil
		}
		if current == nil {
			if strings.TrimSpace(line) != "" {
				return Document{}, &ParseError{Line: lineNo, Err: errors.New("content outside a block directive")}
			}
		} else {
			body.WriteString(line)
		}
		n++
	}
	flush()
	return doc, nil
}

// splitLines splits source into lines retaining their "\n" terminators.
func splitLines(source string) []string {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	lines := strings.SplitAfter(source, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func parseFrontmatter(lines []string, into *Frontmatter) (int, error) {
	if len(lines) == 0 || strings.TrimRight(lines[0], "\n") != "---" {
		return 0, &ParseError{Line: 1, Err: errors.New("document must start with a `---` YAML frontmatter")}
	}
	for n := 1; n < len(lines); n++ {
		if strings.TrimRight(lines[n], "\n") != "---" {
			continue
		}
		data := strings.Join(lines[1:n], "")
		decoder := yaml.NewDecoder(strings.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(into); err != nil && !errors.Is(err, io.EOF) {
			return 0, &ParseError{Line: 2, Err: fmt.Errorf("frontmatter: %w", err)}
		}
		if into.Document == "" {
			return 0, &ParseError{Line: 1, Err: errors.New("frontmatter: missing document")}
		}
		return n + 1, nil
	}
	return 0, &ParseError{Line: 1, Err: errors.New("unterminated frontmatter")}
}

// ---------------------------------------------------------------------------
// Directives
// ---------------------------------------------------------------------------

// directiveAttributes returns the attribute text of a directive line.
func directiveAttributes(line string) (string, bool) {
	trimmed := strings.TrimRight(line, " \t\n")
	prefix := "::" + DirectiveName + "{"
	if !strings.HasPrefix(trimmed, prefix) || !strings.HasSuffix(trimmed, "}") {
		return "", false
	}
	return trimmed[len(prefix) : len(trimmed)-1], true
}

// parseDirective parses and checks directive attributes: existing blocks
// need positive block and rev numbers and a type; new blocks
// (`block=new`) carry only a type.
func parseDirective(text string) (Directive, error) {
	attrs, err := parseAttributes(text)
	if err != nil {
		return Directive{}, err
	}
	var d Directive
	d.Type = attrs["type"]
	if d.Type == "" {
		return Directive{}, errors.New("directive requires a type")
	}
	positive := func(name string) (int, error) {
		value, ok := attrs[name]
		if !ok {
			return 0, fmt.Errorf("directive requires %s", name)
		}
		number, err := strconv.Atoi(value)
		if err != nil || number <= 0 {
			return 0, fmt.Errorf("directive %s %q: must be a positive number", name, value)
		}
		return number, nil
	}
	if attrs["block"] == NewBlockValue {
		d.New = true
		for _, name := range []string{"rev", "author", "parent", "timestamp"} {
			if _, ok := attrs[name]; ok {
				return Directive{}, fmt.Errorf("new block directive cannot assign %s", name)
			}
		}
		return d, nil
	}
	if d.BlockNumber, err = positive("block"); err != nil {
		return Directive{}, err
	}
	if d.Revision, err = positive("rev"); err != nil {
		return Directive{}, err
	}
	d.Author = attrs["author"]
	if _, ok := attrs["parent"]; ok {
		parent, err := positive("parent")
		if err != nil {
			return Directive{}, err
		}
		d.Parent = &parent
	}
	if value, ok := attrs["timestamp"]; ok {
		ts, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return Directive{}, fmt.Errorf("directive timestamp %q: %w", value, err)
		}
		d.Timestamp = &ts
	}
	return d, nil
}

var directiveKeys = map[string]bool{"block": true, "rev": true, "type": true, "author": true, "parent": true, "timestamp": true}

// parseAttributes parses space-separated key=value attributes. Values are
// plain tokens or double-quoted with `\\`, `\"`, `\n`, and `\r` escapes.
func parseAttributes(text string) (map[string]string, error) {
	attrs := map[string]string{}
	i := 0
	for {
		for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
			i++
		}
		if i >= len(text) {
			return attrs, nil
		}
		eq := strings.IndexByte(text[i:], '=')
		if eq <= 0 {
			return nil, fmt.Errorf("malformed directive attribute %q", text[i:])
		}
		key := text[i : i+eq]
		if strings.ContainsAny(key, " \t\"") {
			return nil, fmt.Errorf("malformed directive attribute %q", key)
		}
		if !directiveKeys[key] {
			return nil, fmt.Errorf("unsupported directive attribute %q", key)
		}
		if _, dup := attrs[key]; dup {
			return nil, fmt.Errorf("duplicate directive attribute %q", key)
		}
		i += eq + 1
		var value string
		if i < len(text) && text[i] == '"' {
			var out strings.Builder
			i++
			closed := false
			for i < len(text) {
				c := text[i]
				if c == '"' {
					closed = true
					i++
					break
				}
				if c == '\\' {
					if i+1 >= len(text) {
						break
					}
					switch text[i+1] {
					case '\\', '"':
						out.WriteByte(text[i+1])
					case 'n':
						out.WriteByte('\n')
					case 'r':
						out.WriteByte('\r')
					default:
						return nil, fmt.Errorf("directive attribute %q: unsupported escape \\%c", key, text[i+1])
					}
					i += 2
					continue
				}
				out.WriteByte(c)
				i++
			}
			if !closed {
				return nil, fmt.Errorf("directive attribute %q: unterminated quoted value", key)
			}
			if i < len(text) && text[i] != ' ' && text[i] != '\t' {
				return nil, fmt.Errorf("directive attribute %q: unexpected text after quoted value", key)
			}
			value = out.String()
		} else {
			end := strings.IndexAny(text[i:], " \t")
			if end < 0 {
				end = len(text) - i
			}
			value = text[i : i+end]
			if value == "" || strings.ContainsAny(value, `"\{}=`) {
				return nil, fmt.Errorf("directive attribute %q: malformed value %q", key, value)
			}
			i += end
		}
		attrs[key] = value
	}
}

// ---------------------------------------------------------------------------
// Fences and structured headers
// ---------------------------------------------------------------------------

type fenceMarker struct {
	char   byte
	length int
	info   string
}

// openFence recognizes a fenced code opener (up to three spaces of
// indentation, three or more backticks or tildes, optional info string).
func openFence(line string) (fenceMarker, bool) {
	text := strings.TrimRight(line, "\n")
	indent := len(text) - len(strings.TrimLeft(text, " "))
	if indent > 3 {
		return fenceMarker{}, false
	}
	text = text[indent:]
	if text == "" || (text[0] != '`' && text[0] != '~') {
		return fenceMarker{}, false
	}
	char := text[0]
	length := len(text) - len(strings.TrimLeft(text, string(char)))
	if length < 3 {
		return fenceMarker{}, false
	}
	info := strings.TrimSpace(text[length:])
	if char == '`' && strings.Contains(info, "`") {
		return fenceMarker{}, false
	}
	return fenceMarker{char: char, length: length, info: info}, true
}

func closesFence(line string, open fenceMarker) bool {
	text := strings.TrimRight(line, " \t\n")
	indent := len(text) - len(strings.TrimLeft(text, " "))
	if indent > 3 {
		return false
	}
	text = text[indent:]
	return len(text) >= open.length && strings.Trim(text, string(open.char)) == ""
}

// parseHeader parses the structured header fence opened at lines[start],
// returning the index after its closing fence and the fields nested under
// `content:`.
func parseHeader(lines []string, start int, open fenceMarker) (int, content.MarkdownFields, error) {
	for n := start + 1; n < len(lines); n++ {
		if !closesFence(lines[n], open) {
			continue
		}
		fields, err := decodeHeader(strings.Join(lines[start+1:n], ""))
		if err != nil {
			return 0, nil, err
		}
		return n + 1, fields, nil
	}
	return 0, nil, errors.New("unterminated structured header fence")
}

// decodeHeader decodes the header envelope (only a `content` mapping is
// allowed) and normalizes the fields to the JSON-compatible data model.
func decodeHeader(data string) (content.MarkdownFields, error) {
	var envelope map[string]any
	if err := yaml.Unmarshal([]byte(data), &envelope); err != nil {
		return nil, fmt.Errorf("structured header: %w", err)
	}
	for key := range envelope {
		if key != HeaderContentKey {
			return nil, fmt.Errorf("structured header: unsupported field %q (only %q is allowed)", key, HeaderContentKey)
		}
	}
	raw := envelope[HeaderContentKey]
	if raw == nil {
		return nil, nil
	}
	if _, ok := raw.(map[string]any); !ok {
		return nil, fmt.Errorf("structured header: %q must be a mapping", HeaderContentKey)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("structured header: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	var fields content.MarkdownFields
	if err := decoder.Decode(&fields); err != nil {
		return nil, fmt.Errorf("structured header: %w", err)
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return fields, nil
}
