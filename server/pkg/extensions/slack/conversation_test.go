package slack_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/slack"
)

func TestConversationPresentationGroupsConsecutiveAuthors(t *testing.T) {
	conversation := slack.NewConversationType()
	messages := []slack.Message{
		{Author: "Alice", Text: "First line\nSecond line", URL: threadURL},
		{Author: "Alice", Text: "Follow-up\n\n- A detail"},
		{Author: "Alice", Text: "Another follow-up"},
		{Author: "Bob", Text: "Reply"},
		{Author: "Alice", Text: "Back again"},
	}
	items := make([]content.KeyedRecord, len(messages))
	for i, message := range messages {
		items[i] = content.NewKeyedItem(conversation, fmt.Sprint(i), at(fmt.Sprintf("10:%02d", i)), message)
	}
	collection, err := conversation.NewCollection(items)
	testutil.NoErr(t, err)
	before, err := json.Marshal(collection)
	testutil.NoErr(t, err)
	markdown, err := conversation.RenderMarkdown(collection)
	testutil.NoErr(t, err)

	presentation, err := conversation.Present(collection)
	testutil.NoErr(t, err)
	want := "- **Alice** · 2026-10-25 10:00:00 UTC · [source](" + threadURL + ")  \n" +
		"  First line\n  Second line\n\n  Follow-up\n\n  - A detail\n\n  Another follow-up\n" +
		"- **Bob** · 2026-10-25 10:03:00 UTC  \n  Reply\n" +
		"- **Alice** · 2026-10-25 10:04:00 UTC  \n  Back again\n"
	if len(presentation.Cards) != 1 || presentation.Cards[0].Markdown != want {
		t.Fatalf("unexpected grouped presentation: %#v, want %q", presentation, want)
	}
	if len(presentation.Cards[0].Fields) != 0 {
		t.Fatalf("unexpected conversation fields: %#v", presentation.Cards[0].Fields)
	}
	// Grouping is presentation-only: exports retain every message header,
	// and the record (including all individual messages) is untouched.
	if strings.Count(markdown, "- **Alice**") != 4 || strings.Count(markdown, "- **Bob**") != 1 {
		t.Fatalf("export lost individual message headers: %q", markdown)
	}
	after, err := json.Marshal(collection)
	testutil.NoErr(t, err)
	if string(before) != string(after) || len(collection.KeyedItems()) != 5 {
		t.Fatal("presentation changed the record")
	}
}

func TestConversationPresentationAuthorIdentity(t *testing.T) {
	withID := func(author, id string) slack.Message {
		return slack.Message{Author: author, Text: "Message", Extras: map[string]any{"slack_user_id": id}}
	}
	for _, tc := range []struct {
		name     string
		previous slack.Message
		current  slack.Message
		groups   int
	}{
		{"same ID", withID("Alice", "U1"), withID("Alice renamed", "U1"), 1},
		{"different IDs, same name", withID("Alice", "U1"), withID("Alice", "U2"), 2},
		{"missing ID", withID("Alice", "U1"), slack.Message{Author: "Alice", Text: "Message"}, 2},
		{"names only", slack.Message{Author: "Alice", Text: "Message"}, slack.Message{Author: "Alice", Text: "Message"}, 1},
		{"unknown authors", slack.Message{Author: "Unknown author", Text: "Message"}, slack.Message{Author: "Unknown author", Text: "Message"}, 2},
		{"empty authors", slack.Message{Text: "Message"}, slack.Message{Text: "Message"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conversation := slack.NewConversationType()
			collection, err := conversation.NewCollection([]content.KeyedRecord{
				content.NewKeyedItem(conversation, "first", at("10:00"), tc.previous),
				content.NewKeyedItem(conversation, "second", at("10:01"), tc.current),
			})
			testutil.NoErr(t, err)
			presentation, err := conversation.Present(collection)
			testutil.NoErr(t, err)
			if got := strings.Count(presentation.Cards[0].Markdown, "- **"); got != tc.groups {
				t.Fatalf("got %d author groups, want %d: %q", got, tc.groups, presentation.Cards[0].Markdown)
			}
		})
	}
}
