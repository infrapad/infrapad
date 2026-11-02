package slack

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/infrapad/infrapad/server/pkg/content"
)

// ConversationType implements Slack/Conversation, a groupable type whose
// records are host-selected collections of keyed Slack messages.
type ConversationType struct {
	content.TypedMarkdownRenderer[content.GroupableCollection[Message]]
}

func NewConversationType() *ConversationType {
	return &ConversationType{TypedMarkdownRenderer: content.CollectionMarkdown(renderConversation)}
}

func (*ConversationType) Name() string { return ConversationTypeName }

// ContentSchema describes a single message, not the collection.
func (*ConversationType) ContentSchema() content.Schema {
	return content.Schema{Source: schemaCUE, Definition: "#Message"}
}

func (*ConversationType) DecodeJSON(data json.RawMessage) (content.Content, error) {
	return content.DecodeContent[Message](data)
}

func (t *ConversationType) NewCollection(items []content.KeyedRecord) (content.GroupableRecord, error) {
	return content.NewCollection[Message](t, items)
}

func renderConversation(collection content.GroupableCollection[Message]) (string, error) {
	var out strings.Builder
	for _, item := range collection.Items {
		fmt.Fprintf(&out, "- %s **%s:** %s\n", item.Timestamp().UTC().Format("15:04"), item.ContentValue.Author, item.ContentValue.Text)
	}
	return out.String(), nil
}

func (*ConversationType) Present(record content.Record) (content.Presentation, error) {
	collection, err := content.CollectionView[Message](record)
	if err != nil {
		return content.Presentation{}, err
	}
	markdown, err := renderConversation(collection)
	if err != nil {
		return content.Presentation{}, err
	}
	return content.Presentation{Cards: []content.Card{{
		Title:    "Slack conversation",
		Markdown: markdown,
		Fields:   []content.Field{{Label: "Messages", Value: fmt.Sprint(len(collection.Items))}},
	}}}, nil
}
