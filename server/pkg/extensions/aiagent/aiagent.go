// Package aiagent implements the shared AIAgent/Message content type, which
// other extensions (e.g. Slack) emit for messages produced by AI agents.
package aiagent

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/infrapad/infrapad/server/pkg/content"
)

// ContentTypes returns the content types of the AIAgent extension.
func ContentTypes() []content.ContentType {
	return []content.ContentType{NewMessageType()}
}

//go:embed schema.cue
var schemaCUE []byte

// MessageTypeName is the canonical name of the message content type.
const MessageTypeName = "AIAgent/Message"

// Message is the inner content of an AIAgent/Message record.
type Message struct {
	Agent string `json:"agent"`
	Text  string `json:"text"`
	// Extras carries additional metadata not anticipated by the schema,
	// such as source-system identifiers.
	Extras map[string]any `json:"extras,omitempty"`
}

// MessageType implements AIAgent/Message.
type MessageType struct {
	content.TypedMarkdownRenderer[content.Item[Message]]
}

// NewMessageType constructs the AIAgent/Message implementation. Other
// extensions emitting agent messages must use the registered instance.
func NewMessageType() *MessageType {
	return &MessageType{TypedMarkdownRenderer: content.ItemMarkdown(renderMessage)}
}

func (*MessageType) Name() string { return MessageTypeName }

func (*MessageType) ContentSchema() content.Schema {
	return content.Schema{Source: schemaCUE, Definition: "#Message"}
}

func (*MessageType) DecodeJSON(data json.RawMessage) (content.Content, error) {
	return content.DecodeContent[Message](data)
}

func renderMessage(item content.Item[Message]) (string, error) {
	return fmt.Sprintf("### %s\n\n%s\n", item.ContentValue.Agent, item.ContentValue.Text), nil
}

func (t *MessageType) Present(record content.Record) (content.Presentation, error) {
	item, err := content.ItemView[Message](record)
	if err != nil {
		return content.Presentation{}, err
	}
	return content.Presentation{Cards: []content.Card{{
		Title:    item.ContentValue.Agent,
		Markdown: item.ContentValue.Text,
	}}}, nil
}
