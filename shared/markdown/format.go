// Package markdown holds the structural InfraPad Markdown format shared by
// the server (export/push) and clients (offline inspection, document
// discovery).
//
// The package is purely structural: it separates the document frontmatter,
// ::infrapad_block directive attributes, optional structured header fields,
// and raw body text without interpreting any content type. Rendering,
// extension callbacks, schemas, and push semantics belong to the server.
//
// Format:
//
//	---
//	document: <id>
//	title: <title>
//	namespace: <namespace>
//	status: <document status>
//	---
//	::infrapad_block{block=8 rev=2 type=AIAgent/Message parent=6 timestamp=2026-10-25T15:40:07.088889Z}
//	```yaml infrapad
//	content:
//	  agent: chai-bot
//	```
//	The investigation found a blocked drain.
//
// Only a `yaml infrapad` fence immediately following the directive is a
// header; ordinary YAML fences are part of the body. New blocks use
// `block=new type=…` directives. Quoted directive values use backslash
// escapes (`\\`, `\"`, `\n`, `\r`).
//
// The package must stay lightweight: no server models, extension contracts,
// registry, or schema dependencies.
package markdown

import (
	"fmt"
	"time"
)

const (
	// DirectiveName is the leaf directive introducing each block.
	DirectiveName = "infrapad_block"
	// HeaderInfo is the info string of the structured header fence.
	HeaderInfo = "yaml infrapad"
	// HeaderContentKey wraps the extension-projected header fields.
	HeaderContentKey = "content"
	// NewBlockValue is the `block` directive value of a block to be created.
	NewBlockValue = "new"
)

// Document is a structurally parsed InfraPad Markdown document.
type Document struct {
	Frontmatter Frontmatter
	Blocks      []Block
}

// Frontmatter is the document-level YAML header, in output field order.
type Frontmatter struct {
	DocumentID string `yaml:"document"`
	Title      string `yaml:"title"`
	Namespace  string `yaml:"namespace"`
	Status     string `yaml:"status"`
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
	Header map[string]any
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
