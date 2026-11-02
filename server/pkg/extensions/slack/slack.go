// Package slack implements the Slack integration: Slack/Thread references
// expanded from transient thread snapshots into Slack/Conversation messages
// and shared AIAgent/Message records.
package slack

import (
	_ "embed"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/aiagent"
)

//go:embed schema.cue
var schemaCUE []byte

const (
	ThreadTypeName       = "Slack/Thread"
	ConversationTypeName = "Slack/Conversation"
)

// ThreadReference is the inner content of a Slack/Thread primary record.
type ThreadReference struct {
	URL string `json:"url"`
}

// Message is the inner content of one Slack/Conversation member.
type Message struct {
	Author string `json:"author"`
	Text   string `json:"text"`
}

// Snapshot is the transient payload of a Slack/Thread expansion: a complete
// snapshot of the thread. This is a minimal representative format, not a
// Slack API response.
type Snapshot struct {
	Messages []SourceMessage `json:"messages"`
}

// SourceMessage is one message in a Snapshot.
type SourceMessage struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Author    string    `json:"author"`
	Text      string    `json:"text"`
	// Agent marks messages produced by an AI agent (representative fixture
	// flag, not a finalized detection mechanism).
	Agent bool `json:"agent,omitempty"`
}

// ContentTypes returns the Slack content types. agentType is the registered
// AIAgent/Message implementation used for emitted agent messages.
func ContentTypes(agentType *aiagent.MessageType) []content.ContentType {
	conversationType := NewConversationType()
	return []content.ContentType{NewThreadType(conversationType, agentType), conversationType}
}
