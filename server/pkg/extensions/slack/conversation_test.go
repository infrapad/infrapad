package slack_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/aiagent"
	"github.com/infrapad/infrapad/server/pkg/extensions/slack"
)

func TestConversationPresentationGroupsConsecutiveAuthors(t *testing.T) {
	conversation := slack.NewConversationType()
	messages := []slack.Message{
		{Author: "Alice", Body: "First line\nSecond line", URL: threadURL},
		{Author: "Alice", Body: "Follow-up\n\n- A detail"},
		{Author: "Alice", Body: "Another follow-up"},
		{Author: "Bob", Body: "Reply"},
		{Author: "Alice", Body: "Back again"},
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
		return slack.Message{Author: author, Body: "Message", Extras: map[string]any{"slack_user_id": id}}
	}
	for _, tc := range []struct {
		name     string
		previous slack.Message
		current  slack.Message
		groups   int
	}{
		{"same ID", withID("Alice", "U1"), withID("Alice renamed", "U1"), 1},
		{"different IDs, same name", withID("Alice", "U1"), withID("Alice", "U2"), 2},
		{"missing ID", withID("Alice", "U1"), slack.Message{Author: "Alice", Body: "Message"}, 2},
		{"names only", slack.Message{Author: "Alice", Body: "Message"}, slack.Message{Author: "Alice", Body: "Message"}, 1},
		{"unknown authors", slack.Message{Author: "Unknown author", Body: "Message"}, slack.Message{Author: "Unknown author", Body: "Message"}, 2},
		{"empty authors", slack.Message{Body: "Message"}, slack.Message{Body: "Message"}, 2},
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

// TestConversationAttachments covers the canonical body/attachment shape:
// URL-derived keys that are stable across preview detail and order changes
// and intentionally shared by duplicate URLs, compact presentation versus
// complete export evidence, and verbatim legacy "text" decoding.
func TestConversationAttachments(t *testing.T) {
	agent := aiagent.NewMessageType()
	registry, err := content.NewRegistry([]content.ContentType{agent}, slack.ContentTypes(agent))
	testutil.NoErr(t, err)
	thread, err := registry.Lookup(slack.ThreadTypeName)
	testutil.NoErr(t, err)
	root := content.NewItem(thread, at("10:00"), slack.ThreadReference{URL: threadURL})

	// Attachments shaped after the PagerDuty incident unfurl of data/4.json.
	const incidentURL = "https://redhat.pagerduty.com/incidents/Q388WQASEWQZYC"
	incident := slack.AttachmentPreview{
		Title: "[#3255254] API SLO Burn", TitleLink: incidentURL, URL: incidentURL, ProductName: "PagerDuty",
		Text: "Incident in PagerDuty", Fields: []slack.PreviewField{{Label: "Status", Value: "Triggered"}},
	}
	docs := slack.AttachmentPreview{Title: "Troubleshooting", TitleLink: "https://docs.example.com/troubleshooting"}
	expandMessage := func(previews ...slack.AttachmentPreview) slack.Message {
		t.Helper()
		payload, err := json.Marshal(slack.Payload{Messages: []slack.SourceMessage{
			{Ts: "1794576255.355919", Text: "Related PD Alert - <" + incidentURL + ">", Previews: previews},
		}})
		testutil.NoErr(t, err)
		members, err := expand(registry, root, payload)
		testutil.NoErr(t, err)
		view, err := content.KeyedItemView[slack.Message](members[0])
		testutil.NoErr(t, err)
		return view.ContentValue
	}

	first := expandMessage(incident, docs)
	if first.Body != "Related PD Alert - <"+incidentURL+">" || len(first.Attachments) != 2 {
		t.Fatalf("unexpected canonical message: %#v", first)
	}
	wantIncident := slack.Attachment{
		Key:     slack.AttachmentKey(incidentURL),
		Link:    "[\\[#3255254\\] API SLO Burn](" + incidentURL + ")",
		Details: "**Product:** PagerDuty\n\nIncident in PagerDuty\n\n**Status:** Triggered",
	}
	if first.Attachments[0] != wantIncident {
		t.Fatalf("incident attachment = %#v, want %#v", first.Attachments[0], wantIncident)
	}

	// Refreshed details and reordering keep keys; duplicate URLs share one.
	resolved := incident
	resolved.Fields = []slack.PreviewField{{Label: "Status", Value: "Resolved"}}
	second := expandMessage(docs, resolved, incident)
	if second.Attachments[0].Key != first.Attachments[1].Key ||
		second.Attachments[1].Key != wantIncident.Key || second.Attachments[2].Key != wantIncident.Key ||
		second.Attachments[1].Details == second.Attachments[2].Details {
		t.Fatalf("unstable attachment keys: %#v vs %#v", first.Attachments, second.Attachments)
	}

	// Presentation shows only links; exports keep complete details.
	conversation := slack.NewConversationType()
	collection, err := conversation.NewCollection([]content.KeyedRecord{
		content.NewKeyedItem(conversation, "m", at("10:00"), first),
	})
	testutil.NoErr(t, err)
	reloaded := testutil.RoundTripRecord(t, registry, collection)
	presentation, err := reloaded.ContentType().(content.Presenter).Present(reloaded)
	testutil.NoErr(t, err)
	wantBody := "  Related PD Alert - <" + incidentURL + ">\n\n"
	if got := presentation.Cards[0].Markdown; !strings.HasSuffix(got, wantBody+
		"  - "+wantIncident.Link+"\n  - [Troubleshooting](https://docs.example.com/troubleshooting)\n") {
		t.Fatalf("unexpected presentation: %q", got)
	}
	markdown, err := reloaded.ContentType().RenderMarkdown(reloaded)
	testutil.NoErr(t, err)
	if !strings.HasSuffix(markdown, wantBody+
		"  > Attachment: "+wantIncident.Link+"\n  >\n  > **Product:** PagerDuty\n  >\n  > Incident in PagerDuty\n  >\n  > **Status:** Triggered\n\n"+
		"  > Attachment: [Troubleshooting](https://docs.example.com/troubleshooting)\n") {
		t.Fatalf("export lost attachment evidence: %q", markdown)
	}

	// Legacy "text" is read verbatim as the body, without extracting
	// previews, and is re-encoded in the new shape.
	legacyText := "Investigating\n\n> Attachment: **[Incident](" + incidentURL + ")** · PagerDuty\n> **Status:** Resolved"
	legacy, err := registry.DecodeRecord(slack.ConversationTypeName, json.RawMessage(
		`{"items":[{"key":"slack-message:1","timestamp":"2026-11-13T13:24:15Z","content":{"author":"A","text":`+
			string(must(json.Marshal(legacyText)))+`}}]}`))
	testutil.NoErr(t, err)
	legacyView, err := content.CollectionView[slack.Message](legacy)
	testutil.NoErr(t, err)
	if message := legacyView.Items[0].ContentValue; message.Body != legacyText || len(message.Attachments) != 0 {
		t.Fatalf("legacy message not read verbatim: %#v", message)
	}
	if encoded := string(must(json.Marshal(legacy))); strings.Contains(encoded, `"text"`) || !strings.Contains(encoded, `"body"`) {
		t.Fatalf("legacy message re-encoded in old shape: %s", encoded)
	}
	_, err = registry.DecodeRecord(slack.ConversationTypeName, json.RawMessage(
		`{"items":[{"key":"k","timestamp":"2026-11-13T13:24:15Z","content":{"author":"A","text":"x","body":"y"}}]}`))
	if err == nil {
		t.Fatal("expected legacy text combined with body to be rejected")
	}
}

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}
