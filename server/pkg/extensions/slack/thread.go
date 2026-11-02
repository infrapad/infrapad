package slack

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/aiagent"
)

// ThreadType implements Slack/Thread, an expandable standalone reference.
type ThreadType struct {
	content.TypedMarkdownRenderer[content.Item[ThreadReference]]
	conversationType *ConversationType
	agentType        *aiagent.MessageType
}

func NewThreadType(conversationType *ConversationType, agentType *aiagent.MessageType) *ThreadType {
	return &ThreadType{
		TypedMarkdownRenderer: content.ItemMarkdown(renderThread),
		conversationType:      conversationType,
		agentType:             agentType,
	}
}

func (*ThreadType) Name() string { return ThreadTypeName }

func (*ThreadType) ContentSchema() content.Schema {
	return content.Schema{Source: schemaCUE, Definition: "#ThreadReference"}
}

func (*ThreadType) PayloadSchema() content.Schema {
	return content.Schema{Source: schemaCUE, Definition: "#Snapshot"}
}

func (*ThreadType) DecodeJSON(data json.RawMessage) (content.Content, error) {
	return content.DecodeContent[ThreadReference](data)
}

func renderThread(item content.Item[ThreadReference]) (string, error) {
	return fmt.Sprintf("[Slack thread](%s)\n", item.ContentValue.URL), nil
}

func (*ThreadType) Present(record content.Record) (content.Presentation, error) {
	item, err := content.ItemView[ThreadReference](record)
	if err != nil {
		return content.Presentation{}, err
	}
	return content.Presentation{Cards: []content.Card{{
		Title: "Slack thread",
		Links: []content.Link{{Label: "Open in Slack", URL: item.ContentValue.URL}},
	}}}, nil
}

// MessageKey is the generated key of the record emitted for a source message.
func MessageKey(id string) string { return "slack-message:" + id }

// Expand emits the complete desired output for a thread snapshot: human
// messages as one Slack/Conversation collection and agent messages as
// standalone AIAgent/Message records. Final conversation splitting around
// interleaved records is up to the host.
func (t *ThreadType) Expand(ctx context.Context, existing content.Record, payload json.RawMessage) (content.ExpansionResult, error) {
	if _, err := content.ItemView[ThreadReference](existing); err != nil {
		return content.ExpansionResult{}, err
	}
	snapshot, err := content.DecodePayload[Snapshot](payload)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	// The primary state is unchanged; returning existing keeps its metadata.
	result := content.ExpansionResult{Record: existing}
	var conversation []content.KeyedRecord
	for _, message := range snapshot.Messages {
		if err := ctx.Err(); err != nil {
			return content.ExpansionResult{}, err
		}
		key := MessageKey(message.ID)
		if message.Agent {
			result.Blocks = append(result.Blocks, content.EmittedBlock{
				Record: content.NewKeyedItem(t.agentType, key, message.Timestamp, aiagent.Message{
					Agent:  message.Author,
					Text:   message.Text,
					Extras: map[string]any{"slack_message_id": message.ID},
				}),
			})
			continue
		}
		conversation = append(conversation, content.NewKeyedItem(t.conversationType, key, message.Timestamp, Message{
			Author: message.Author,
			Text:   message.Text,
		}))
	}
	if len(conversation) > 0 {
		collection, err := t.conversationType.NewCollection(conversation)
		if err != nil {
			return content.ExpansionResult{}, err
		}
		result.Blocks = append(result.Blocks, content.EmittedBlock{Record: collection})
	}
	return result, nil
}
