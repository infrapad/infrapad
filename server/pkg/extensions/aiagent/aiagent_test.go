package aiagent_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/aiagent"
)

// TestMessageScenario exercises AIAgent/Message through the registry:
// record round-trips over the generated schema and host codecs, Markdown
// and card rendering with a source URL, and Markdown updates preserving
// content and metadata.
func TestMessageScenario(t *testing.T) {
	registry, err := content.NewRegistry(aiagent.ContentTypes())
	testutil.NoErr(t, err)
	messageType, err := registry.Lookup(aiagent.MessageTypeName)
	testutil.NoErr(t, err)

	record := content.NewKeyedItem(messageType, "agent-1", at("10:02"), aiagent.Message{
		Agent:  "investigation-agent",
		Text:   "Node unhealthy.",
		URL:    "https://slack.example/archives/C1/p1792942610461489",
		Extras: map[string]any{"slack_message_id": "3"},
	})
	reloaded := testutil.RoundTripRecord(t, registry, record)

	// Markdown rendering returns the message text unchanged, without
	// presentation framing.
	body, err := reloaded.ContentType().RenderMarkdown(reloaded)
	testutil.NoErr(t, err)
	if body != "Node unhealthy." {
		t.Fatalf("unexpected rendering: %q", body)
	}

	// One card: agent title, message Markdown, and the source link.
	presentation, err := messageType.(content.Presenter).Present(reloaded)
	testutil.NoErr(t, err)
	card := presentation.Cards[0]
	if len(presentation.Cards) != 1 || card.Title != "investigation-agent" || card.Markdown != "Node unhealthy." ||
		len(card.Links) != 1 || card.Links[0].Label != "Open source" || card.Links[0].URL != record.ContentValue.URL {
		t.Fatalf("unexpected presentation: %#v", presentation)
	}

	// Updates replace the text 1:1: agent, URL, extras, type, timestamp,
	// and key are preserved and the existing record is not modified.
	updated, err := messageType.(content.MarkdownUpdater).UpdateMarkdown(reloaded, "Node unhealthy; replacement started.")
	testutil.NoErr(t, err)
	view, err := content.KeyedItemView[aiagent.Message](updated)
	testutil.NoErr(t, err)
	if view.ContentValue.Text != "Node unhealthy; replacement started." || view.Key() != "agent-1" ||
		!view.Timestamp().Equal(at("10:02")) || view.ContentValue.URL != record.ContentValue.URL ||
		view.ContentValue.Agent != "investigation-agent" || view.ContentValue.Extras["slack_message_id"] != "3" {
		t.Fatalf("unexpected update: %#v", view)
	}
	if record.ContentValue.Text != "Node unhealthy." {
		t.Fatal("update modified the existing record")
	}
	testutil.NoErr(t, registry.ValidateRecord(updated))
	testutil.RoundTripRecord(t, registry, updated)

	// A render/update round trip preserves the exact original content,
	// including meaningful trailing whitespace.
	original := content.NewKeyedItem(messageType, "agent-2", at("10:03"), aiagent.Message{
		Agent: "reporter",
		Text:  "Draft report.\n\n- item\n",
	})
	rendered, err := messageType.RenderMarkdown(original)
	testutil.NoErr(t, err)
	roundTripped, err := messageType.(content.MarkdownUpdater).UpdateMarkdown(original, rendered)
	testutil.NoErr(t, err)
	if !content.EqualRecords(original, roundTripped) {
		t.Fatalf("round trip changed the record:\n%#v\n%#v", original, roundTripped)
	}

	// A blank body produces a record that fails schema validation.
	blank, err := messageType.(content.MarkdownUpdater).UpdateMarkdown(original, " \n")
	testutil.NoErr(t, err)
	if err := registry.ValidateRecord(blank); err == nil {
		t.Fatal("expected blank text to be rejected")
	}
}

// TestMessageValidation checks the schema boundaries of AIAgent/Message.
func TestMessageValidation(t *testing.T) {
	registry, err := content.NewRegistry(aiagent.ContentTypes())
	testutil.NoErr(t, err)

	for name, content := range map[string]string{
		"blank agent":      `{"agent": "  ", "text": "x"}`,
		"missing agent":    `{"text": "x"}`,
		"blank text":       `{"agent": "a", "text": "\n\t"}`,
		"missing text":     `{"agent": "a"}`,
		"empty url":        `{"agent": "a", "text": "x", "url": ""}`,
		"scheme-less url":  `{"agent": "a", "text": "x", "url": "slack.example/x"}`,
		"wrong scheme url": `{"agent": "a", "text": "x", "url": "ftp://slack.example/x"}`,
		"host-less url":    `{"agent": "a", "text": "x", "url": "https:///x"}`,
	} {
		_, err := registry.DecodeRecord(aiagent.MessageTypeName, record(content))
		if err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
		expectField(t, err, fieldFor(name))
	}

	// Go-constructed records are checked the same way.
	messageType, err := registry.Lookup(aiagent.MessageTypeName)
	testutil.NoErr(t, err)
	if err := registry.ValidateRecord(content.NewItem(messageType, at("10:00"), aiagent.Message{Agent: "a", Text: " "})); err == nil {
		t.Fatal("expected blank text to be rejected")
	}
}

// fieldFor maps a validation case to its expected field path.
func fieldFor(name string) string {
	switch {
	case strings.Contains(name, "agent"):
		return "agent"
	case strings.Contains(name, "text"):
		return "text"
	default:
		return "url"
	}
}

// record wraps inner content in a host item record envelope.
func record(content string) json.RawMessage {
	return json.RawMessage(`{"timestamp": "2026-10-25T10:00:00Z", "content": ` + content + `}`)
}

// expectField asserts a content-stage validation error reporting path.
func expectField(t *testing.T, err error, path string) {
	t.Helper()
	var validation *content.ValidationError
	if !errors.As(err, &validation) || validation.Stage != content.StageContent {
		t.Fatalf("expected a content validation error, got %v", err)
	}
	for _, field := range validation.Fields {
		if field.Path == path {
			return
		}
	}
	t.Fatalf("expected field %q in %v", path, validation)
}

func at(clock string) time.Time {
	value, err := time.Parse(time.RFC3339, "2026-10-25T"+clock+":00Z")
	if err != nil {
		panic(err)
	}
	return value
}
