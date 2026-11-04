package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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

// MessageKey is the generated key of the record emitted for a source
// message, identified by its Slack timestamp.
func MessageKey(ts string) string { return "slack-message:" + ts }

// Expand emits the complete desired output for a thread snapshot: ordinary
// messages as one Slack/Conversation collection and mapped agent messages as
// standalone AIAgent/Message records. Final conversation splitting around
// interleaved records is up to the host.  The primary state is unchanged;
// returning existing keeps its metadata.
func (t *ThreadType) Expand(ctx context.Context, existing content.Record, payload json.RawMessage) (content.ExpansionResult, error) {
	item, err := content.ItemView[ThreadReference](existing)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	location, err := parseThreadURL(item.ContentValue.URL)
	if err != nil {
		return content.ExpansionResult{}, &content.ValidationError{
			ContentType: ThreadTypeName,
			Stage:       content.StageContent,
			Fields:      []content.FieldError{{Path: "url", Message: err.Error()}},
		}
	}
	snapshot, err := content.DecodePayload[Snapshot](payload)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	// An explicit empty array is valid; missing or null arrays are not
	// (CUE cannot distinguish them from an empty open list).
	if snapshot.Messages == nil {
		return content.ExpansionResult{}, payloadError("messages", "must be an explicit array; missing or null message arrays are invalid")
	}
	if err := validateAgentMap(snapshot.Agents); err != nil {
		return content.ExpansionResult{}, err
	}
	times, err := parseMessages(snapshot.Messages)
	if err != nil {
		return content.ExpansionResult{}, err
	}

	result := content.ExpansionResult{Record: existing}
	var conversation []content.KeyedRecord
	for i, message := range snapshot.Messages {
		if err := ctx.Err(); err != nil {
			return content.ExpansionResult{}, err
		}
		key := MessageKey(message.Ts)
		body := messageBody(message)
		permalink := location.permalink(message.Ts)
		extras := map[string]any{"slack_ts": message.Ts}
		if message.User != "" {
			extras["slack_user_id"] = message.User
		}
		if agent, mapped := snapshot.Agents[message.User]; mapped {
			result.Blocks = append(result.Blocks, content.EmittedBlock{
				Record: content.NewKeyedItem(t.agentType, key, times[i], aiagent.Message{
					Agent:  agent,
					Text:   body,
					URL:    permalink,
					Extras: extras,
				}),
			})
			continue
		}
		conversation = append(conversation, content.NewKeyedItem(t.conversationType, key, times[i], Message{
			Author: messageAuthor(message),
			Text:   body,
			URL:    permalink,
			Extras: extras,
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

// payloadError builds a structured validation error of the Slack/Thread
// payload stage identifying the offending message/field.
func payloadError(path, format string, args ...any) *content.ValidationError {
	return &content.ValidationError{
		ContentType: ThreadTypeName,
		Stage:       content.StagePayload,
		Fields:      []content.FieldError{{Path: path, Message: fmt.Sprintf(format, args...)}},
	}
}

// validateAgentMap rejects blank user-ID keys or agent names.
func validateAgentMap(agents map[string]string) error {
	for id, name := range agents {
		if strings.TrimSpace(id) == "" || strings.TrimSpace(name) == "" {
			return payloadError("agents."+id, "agent mapping keys and values must be nonblank")
		}
	}
	return nil
}

// parseMessages validates and parses the source messages of a snapshot:
// Slack timestamps must be well formed and unique within it. It returns the
// parsed UTC event times aligned with the input.
func parseMessages(messages []SourceMessage) ([]time.Time, error) {
	times := make([]time.Time, len(messages))
	seen := make(map[string]struct{}, len(messages))
	for i, message := range messages {
		at, err := parseSlackTimestamp(message.Ts)
		if err != nil {
			return nil, payloadError(fmt.Sprintf("messages.%d.ts", i), "%v", err)
		}
		if _, duplicate := seen[message.Ts]; duplicate {
			return nil, payloadError(fmt.Sprintf("messages.%d.ts", i), "duplicate Slack message timestamp %q", message.Ts)
		}
		seen[message.Ts] = struct{}{}
		times[i] = at
	}
	return times, nil
}
