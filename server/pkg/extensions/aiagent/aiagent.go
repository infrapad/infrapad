// Package aiagent implements the shared AIAgent/Message content type, which
// other extensions (e.g. Slack) emit for messages produced by AI agents.
package aiagent

import (
	_ "embed"
	"encoding/json"

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
	// Agent is the required display name of the producing agent.
	Agent string `json:"agent"`
	// Text is the required Markdown body of the message.
	Text string `json:"text"`
	// URL is the optional absolute HTTP(S) source URL of the message.
	// It is omitted when unavailable; an empty value is never valid.
	URL string `json:"url,omitempty"`
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

// UpdateMarkdown replaces the message text with the body while preserving
// the agent, URL, extras, and the record metadata (type association,
// timestamp, key). The body is the message Markdown 1:1: attribution and
// the source link are presentation, rendered by Present, not part of the
// Markdown body. existing is not modified.
func (*MessageType) UpdateMarkdown(existing content.Record, body string) (content.Record, error) {
	item, err := content.ItemView[Message](existing)
	if err != nil {
		return nil, err
	}
	return content.WithContent(existing, Message{
		Agent:  item.ContentValue.Agent,
		Text:   body,
		URL:    item.ContentValue.URL,
		Extras: item.ContentValue.Extras,
	})
}

// Present returns one card titled with the agent name, the message Markdown,
// and an optional source link. Extras and host-owned timestamps are not
// rendered.
func (*MessageType) Present(record content.Record) (content.Presentation, error) {
	item, err := content.ItemView[Message](record)
	if err != nil {
		return content.Presentation{}, err
	}
	message := item.ContentValue
	card := content.Card{Title: message.Agent, Markdown: message.Text}
	if message.URL != "" {
		card.Links = []content.Link{{Label: "Open source", URL: message.URL}}
	}
	return content.Presentation{Cards: []content.Card{card}}, nil
}

// renderMessage returns the original message Markdown: attribution and
// the source link are presentation (see Present), not body content.
func renderMessage(item content.Item[Message]) (string, error) {
	return item.ContentValue.Text, nil
}
