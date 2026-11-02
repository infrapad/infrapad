// Package core implements Core/Markdown, the ordinary editable Markdown
// text of InfraPad documents.
package core

import (
	_ "embed"
	"encoding/json"

	"github.com/infrapad/infrapad/server/pkg/content"
)

// ContentTypes returns the content types of the core extension.
func ContentTypes() []content.ContentType {
	return []content.ContentType{NewMarkdownType()}
}

//go:embed schema.cue
var schemaCUE []byte

// MarkdownTypeName is the canonical name of the Markdown content type.
const MarkdownTypeName = "Core/Markdown"

// Markdown is the inner content of a Core/Markdown record.
type Markdown struct {
	Text string `json:"text"`
	// Extras carries additional metadata not anticipated by the schema.
	Extras map[string]any `json:"extras,omitempty"`
}

// MarkdownType implements Core/Markdown.
type MarkdownType struct {
	content.TypedMarkdownRenderer[content.Item[Markdown]]
}

// NewMarkdownType constructs the Core/Markdown implementation.
func NewMarkdownType() *MarkdownType {
	return &MarkdownType{TypedMarkdownRenderer: content.ItemMarkdown(renderMarkdown)}
}

func (*MarkdownType) Name() string { return MarkdownTypeName }

func (*MarkdownType) ContentSchema() content.Schema {
	return content.Schema{Source: schemaCUE, Definition: "#Markdown"}
}

func (*MarkdownType) DecodeJSON(data json.RawMessage) (content.Content, error) {
	return content.DecodeContent[Markdown](data)
}

// UpdateMarkdown replaces the text with the body while preserving extras
// and the record metadata (type association, timestamp, key). The body is
// ordinary Markdown text: host-managed structured headers never reach this
// method, and YAML fences inside the body are not interpreted. existing is
// not modified.
func (*MarkdownType) UpdateMarkdown(existing content.Record, body string) (content.Record, error) {
	item, err := content.ItemView[Markdown](existing)
	if err != nil {
		return nil, err
	}
	return content.WithContent(existing, Markdown{Text: body, Extras: item.ContentValue.Extras})
}

func renderMarkdown(item content.Item[Markdown]) (string, error) {
	return item.ContentValue.Text, nil
}
