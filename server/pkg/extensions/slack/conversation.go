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

// conversationTimestampFormat is the full UTC event timestamp rendered in
// each conversation member header.
const conversationTimestampFormat = "2006-01-02 15:04:05 MST"

func (*ConversationType) Present(record content.Record) (content.Presentation, error) {
	collection, err := content.CollectionView[Message](record)
	if err != nil {
		return content.Presentation{}, err
	}
	markdown, err := renderConversationMarkdown(collection, true)
	if err != nil {
		return content.Presentation{}, err
	}
	return content.Presentation{Cards: []content.Card{{
		Title:    "Slack conversation",
		Markdown: markdown,
	}}}, nil
}

// renderConversation preserves each message's attribution in Markdown exports.
func renderConversation(collection content.GroupableCollection[Message]) (string, error) {
	return renderConversationMarkdown(collection, false)
}

// renderConversationMarkdown groups consecutive authors and simplifies
// attachment previews for display only. Exports retain the full message text.
func renderConversationMarkdown(collection content.GroupableCollection[Message], forPresentation bool) (string, error) {
	var out strings.Builder
	for i, item := range collection.Items {
		message := item.ContentValue
		if forPresentation && i > 0 && sameConversationAuthor(collection.Items[i-1].ContentValue, message) {
			out.WriteString("\n")
		} else {
			header := fmt.Sprintf("- **%s** · %s", message.Author, item.Timestamp().UTC().Format(conversationTimestampFormat))
			if message.URL != "" {
				header += fmt.Sprintf(" · [source](%s)", message.URL)
			}
			out.WriteString(header)
			// A soft newline is collapsed into a space by Markdown renderers.
			out.WriteString("  \n")
		}
		body := message.Text
		if forPresentation {
			body = presentMessageBody(body)
		}
		for _, line := range strings.Split(body, "\n") {
			if line != "" {
				out.WriteString("  ")
				out.WriteString(line)
			}
			out.WriteString("\n")
		}
	}
	return out.String(), nil
}

// Prefer Slack identity over display names so users with the same name are
// not merged. Older records without identity metadata fall back to the name.
func sameConversationAuthor(previous, current Message) bool {
	previousID, _ := previous.Extras["slack_user_id"].(string)
	currentID, _ := current.Extras["slack_user_id"].(string)
	if previousID != "" || currentID != "" {
		return previousID != "" && previousID == currentID
	}
	return previous.Author != "" && previous.Author != "Unknown author" && previous.Author == current.Author
}
