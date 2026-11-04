package core_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
)

// TestMarkdownScenario exercises Core/Markdown through the registry:
// record round-trips over the generated schema and host codecs, exact body
// rendering, and immutable Markdown updates preserving metadata and extras.
func TestMarkdownScenario(t *testing.T) {
	registry, err := content.NewRegistry(core.ContentTypes())
	must(t, err)
	markdown, err := registry.Lookup(core.MarkdownTypeName)
	must(t, err)

	text := "# Incident\n\nSomething **happened**.\n"
	record := content.NewItem(markdown, at("10:00"), core.Markdown{
		Text:   text,
		Extras: map[string]any{"origin": map[string]any{"tool": "cli"}},
	})
	reloaded := testutil.RoundTripRecord(t, registry, record)

	// Rendering returns the text unchanged, without host framing.
	body, err := reloaded.ContentType().RenderMarkdown(reloaded)
	must(t, err)
	if body != text {
		t.Fatalf("unexpected rendering: %q", body)
	}

	// Updates replace only the text; extras, type, timestamp, and key are
	// preserved and the existing record is not modified.
	keyed := content.NewKeyedItem(markdown, "summary-1", at("10:05"), core.Markdown{
		Text:   "old body",
		Extras: map[string]any{"origin": "agent"},
	})
	updated, err := markdown.(content.MarkdownUpdater).UpdateMarkdown(testutil.RoundTripRecord(t, registry, keyed), text)
	must(t, err)
	view, err := content.KeyedItemView[core.Markdown](updated)
	must(t, err)
	if view.ContentValue.Text != text || view.Key() != "summary-1" || !view.Timestamp().Equal(at("10:05")) {
		t.Fatalf("unexpected update: %#v", view)
	}
	if view.ContentValue.Extras["origin"] != "agent" {
		t.Fatalf("extras not preserved: %#v", view.ContentValue)
	}
	if keyed.ContentValue.Text != "old body" {
		t.Fatal("update modified the existing record")
	}
	must(t, registry.ValidateRecord(updated))
	testutil.RoundTripRecord(t, registry, updated)
	if _, ok := updated.(content.KeyedRecord); !ok {
		t.Fatal("update dropped the generated key")
	}
}

// TestMarkdownValidation checks the schema boundaries of Core/Markdown.
func TestMarkdownValidation(t *testing.T) {
	registry, err := content.NewRegistry(core.ContentTypes())
	must(t, err)

	// Unknown fields outside extras do not disappear through Go decoding.
	_, err = registry.DecodeRecord(core.MarkdownTypeName, record(`{"text": "x", "Text": "y"}`))
	expectField(t, err, "Text")

	// The text is required.
	_, err = registry.DecodeRecord(core.MarkdownTypeName, record(`{"extras": {"a": 1}}`))
	expectField(t, err, "text")
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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
