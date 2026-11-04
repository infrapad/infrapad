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
// URL is the absolute HTTP(S) Slack permalink of the thread parent message.
type ThreadReference struct {
	URL string `json:"url"`
}

// Message is the inner content of one Slack/Conversation member:
// a normalized readable message with attribution and source link.
type Message struct {
	// Author is the resolved author name, falling back to the Slack user
	// ID or an explicit unknown-author label.
	Author string `json:"author"`
	// Text is the normalized Markdown body, with readable attachment
	// previews appended.
	Text string `json:"text"`
	// URL is the optional absolute HTTP(S) permalink of the message.
	// It is omitted when unavailable; an empty value is never valid.
	URL string `json:"url,omitempty"`
	// Extras carries additional metadata not anticipated by the schema,
	// such as source-system identifiers.
	Extras map[string]any `json:"extras,omitempty"`
}

// Snapshot is the transient payload of a Slack/Thread expansion: a complete
// enriched snapshot of the thread (parent and all replies) plus the
// per-snapshot Slack user-ID → agent-name mapping. External producers
// build the typed payload (converting their own enriched Slack responses,
// including raw attachments, on their side); the server only validates it.
// Raw Slack attachments never appear here, only their typed readable
// previews.
type Snapshot struct {
	// Agents maps the Slack user IDs of AI-agent messages to the agent
	// names used for attribution. Matching is by exact user ID; unmapped
	// messages, including other bots, remain ordinary conversation members.
	// It is never stored on the thread reference or in server-wide
	// configuration.
	Agents map[string]string `json:"agents,omitempty"`
	// Messages is the complete thread in chronological order. An explicit
	// empty array is valid and means no desired generated children;
	// missing or null arrays are errors.
	Messages []SourceMessage `json:"messages"`
}

// SourceMessage is one enriched Slack message of a Snapshot.
type SourceMessage struct {
	// Ts is the Slack message timestamp; it is the stable source identity.
	Ts string `json:"ts"`
	// User is the raw Slack user ID, empty when the message has no author.
	User string `json:"user,omitempty"`
	// Text is the raw Slack message text, possibly empty for
	// attachment-only messages.
	Text string `json:"text"`
	// UserName is the resolved author name, empty when unresolved.
	UserName string `json:"user_name,omitempty"`
	// ReadableText is Text with user mentions resolved, empty when
	// unavailable.
	ReadableText string `json:"readable_text,omitempty"`
	// Previews are typed readable attachment previews in attachment order.
	Previews []AttachmentPreview `json:"previews,omitempty"`
}

// AttachmentPreview is a typed readable preview of one message attachment
// (an app unfurl or Work Object preview such as a PagerDuty incident card).
// It reflects what Slack stored at unfurl time and may be stale; it is not
// authoritative external state. Zero values mean the piece is unavailable
// in the stored attachment.
type AttachmentPreview struct {
	// Title is the displayed title.
	Title string `json:"title,omitempty"`
	// TitleLink is the link attached to the title (ordinary attachments).
	TitleLink string `json:"title_link,omitempty"`
	// URL is the external object identity URL (Work Objects).
	URL string `json:"url,omitempty"`
	// EntityType identifies the Work Object entity type.
	EntityType string `json:"entity_type,omitempty"`
	// ProductName is the product that produced the unfurl.
	ProductName string `json:"product_name,omitempty"`
	// UpdatedAt is the Work Object update time, zero when unavailable.
	UpdatedAt time.Time `json:"updated_at"`
	// Text is the preview body or fallback text.
	Text string `json:"text,omitempty"`
	// Fields are labeled readable fields.
	Fields []PreviewField `json:"fields,omitempty"`
}

// PreviewField is one labeled field of an AttachmentPreview.
type PreviewField struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ContentTypes returns the Slack content types. agentType is the registered
// AIAgent/Message implementation used for emitted agent messages; it must
// be the instance registered in the host registry.
func ContentTypes(agentType *aiagent.MessageType) []content.ContentType {
	conversationType := NewConversationType()
	return []content.ContentType{NewThreadType(conversationType, agentType), conversationType}
}
