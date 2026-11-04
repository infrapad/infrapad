package slack_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/aiagent"
	"github.com/infrapad/infrapad/server/pkg/extensions/slack"
)

// threadURL is a Slack permalink of the snapshot's parent message
// (ts 1788873542.372739), with anonymized workspace and channel.
const threadURL = "https://workspace.example.test/archives/C0EXAMPLE/p1788873542372739"

// fence is a Slack inline triple-backtick code fence.
const fence = "```"

// workObjectPreview is a Work Object preview (e.g. an incident card)
// as a producer extracts it from the raw unfurl, with anonymized values.
var workObjectPreview = slack.AttachmentPreview{
	Title:       "[#123456] Example critical incident (1)",
	TitleLink:   "https://service.example.test/incidents/I0EXAMPLE",
	URL:         "https://service.example.test/incidents/I0EXAMPLE",
	EntityType:  "slack#/entities/incident",
	ProductName: "Example Service",
	Text:        "fallback text",
	UpdatedAt:   time.Unix(1793119327, 0).UTC(),
	Fields: []slack.PreviewField{
		{Label: "Status", Value: "Resolved"},
		{Label: "Urgency", Value: "High"},
		{Label: "Created", Value: "2026-09-08 13:03:43 UTC"},
	},
}

// plainPreview is an ordinary link-unfurl preview.
var plainPreview = slack.AttachmentPreview{
	Title:       "Classic unfurl",
	TitleLink:   "https://example.com/doc",
	ProductName: "Example",
	Text:        "A classic link preview",
	Fields:      []slack.PreviewField{{Label: "Kind", Value: "doc"}},
}

// fallbackPreview is a preview with only fallback text.
var fallbackPreview = slack.AttachmentPreview{
	ProductName: "Example",
	Text:        "Fallback only preview",
}

// threadSnapshot is the typed thread snapshot a producer submits: human
// and mapped-agent messages with microsecond timestamps, multiline text
// with a link and an inline code fence, and Work Object/plain/fallback
// attachment previews.
func threadSnapshot() slack.Snapshot {
	const multiline = "Hi <@U2>, see <https://example.com/doc|the doc> and run" + fence + "ps aux" + fence + "now"
	const readable = "Hi @Example Member, see <https://example.com/doc|the doc> and run" + fence + "ps aux" + fence + "now"
	return slack.Snapshot{
		Agents: map[string]string{"U3": "investigation-agent"},
		Messages: []slack.SourceMessage{
			{Ts: "1788873542.372739", User: "U1", Text: multiline, UserName: "Example Author", ReadableText: readable,
				Previews: []slack.AttachmentPreview{workObjectPreview, plainPreview}},
			{Ts: "1788873600.000100", User: "U2", Text: "done", UserName: "Example Member", ReadableText: "done",
				Previews: []slack.AttachmentPreview{fallbackPreview}},
			{Ts: "1788873700.000200", User: "U3", Text: "Node unhealthy.", UserName: "investigation-agent", ReadableText: "Node unhealthy."},
			{Ts: "1788873800.000300", User: "U4", Text: "Replacement started.", ReadableText: "Replacement started."},
		},
	}
}

// TestThreadSnapshotScenario exercises the Slack integration end to end
// through the registry: typed snapshot payload validation, expansion with
// an interleaved agent message, output validation, grouping (a minimal
// stand-in for the host's grouping engine), record round-trips, rendering,
// and presentation.
func TestThreadSnapshotScenario(t *testing.T) {
	agent := aiagent.NewMessageType()
	registry, err := content.NewRegistry([]content.ContentType{agent}, slack.ContentTypes(agent))
	testutil.NoErr(t, err)

	thread, err := registry.Lookup(slack.ThreadTypeName)
	testutil.NoErr(t, err)
	root := content.NewItem(thread, at("10:00"), slack.ThreadReference{URL: threadURL})
	reloadedRoot := testutil.RoundTripRecord(t, registry, root)
	if reloadedRoot.ContentType() != thread || !reloadedRoot.Timestamp().Equal(root.Timestamp()) {
		t.Fatalf("root metadata not restored: %#v", reloadedRoot)
	}

	// The typed snapshot (with the per-snapshot agent mapping) is the
	// producer's payload; expansion validates it strictly.
	payload, err := json.Marshal(threadSnapshot())
	testutil.NoErr(t, err)
	members, err := expand(registry, reloadedRoot, payload)
	testutil.NoErr(t, err)
	if len(members) != 4 {
		t.Fatalf("expected 4 emitted members, got %d", len(members))
	}

	blocks := group(t, members)
	var markdown []string
	for _, block := range blocks {
		reloaded := testutil.RoundTripRecord(t, registry, block)
		body, err := reloaded.ContentType().RenderMarkdown(reloaded)
		testutil.NoErr(t, err)
		markdown = append(markdown, reloaded.ContentType().Name()+"\n"+body)
	}

	// The human conversation renders with author, full UTC timestamp,
	// source link, normalized text, and appended readable previews.
	first := markdown[0]
	for _, want := range []string{
		"- **Example Author** · 2026-09-08 13:19:02 UTC · " +
			"[source](https://workspace.example.test/archives/C0EXAMPLE/p1788873542372739)",
		"Hi @Example Member, see [the doc](https://example.com/doc) and run\n" +
			"  ```\n" +
			"  ps aux\n" +
			"  ```\n" +
			"  now",
		"> Attachment: **[\\[#123456\\] Example critical incident (1)]" +
			"(https://service.example.test/incidents/I0EXAMPLE)** · Example Service",
		"> **Status:** Resolved",
		"> **Created:** 2026-09-08 13:03:43 UTC",
		"> Attachment: **[Classic unfurl](https://example.com/doc)** · Example",
		"- **Example Member** · 2026-09-08 13:20:00 UTC · " +
			"[source](https://workspace.example.test/archives/C0EXAMPLE/p1788873600000100)",
		`> Attachment: **Example**
  > Fallback only preview`,
	} {
		if !strings.Contains(first, want) {
			t.Fatalf("conversation markdown missing %q:\n%s", want, first)
		}
	}
	want := []string{
		markdown[0],
		"AIAgent/Message\nNode unhealthy.",
		"Slack/Conversation\n",
	}
	if !strings.HasPrefix(markdown[2], want[2]) || markdown[1] != want[1] {
		t.Fatalf("unexpected rendering:\n%s", strings.Join(markdown, "\n---\n"))
	}
	if !strings.Contains(markdown[2], `- **U4** · 2026-09-08 13:23:20 UTC · [source](https://workspace.example.test/archives/C0EXAMPLE/p1788873800000300)
  Replacement started.`) {
		t.Fatalf("unexpected trailing conversation:\n%s", markdown[2])
	}

	// The mapped agent message is a standalone AIAgent/Message record
	// with the source link and Slack identity metadata.
	agentItem, err := content.KeyedItemView[aiagent.Message](blocks[1])
	testutil.NoErr(t, err)
	if agentItem.Key() != slack.MessageKey("1788873700.000200") ||
		agentItem.ContentValue.Agent != "investigation-agent" ||
		agentItem.ContentValue.URL != "https://workspace.example.test/archives/C0EXAMPLE/p1788873700000200" ||
		agentItem.ContentValue.Extras["slack_user_id"] != "U3" ||
		agentItem.ContentValue.Extras["slack_ts"] != "1788873700.000200" {
		t.Fatalf("unexpected agent record: %#v", agentItem)
	}

	// Regroup the first conversation from stored constituents without the
	// original payload: dropping the first member moves the anchor.
	conversation := blocks[0].(content.GroupableRecord)
	rest, err := conversation.ContentType().(content.GroupableContentType).NewCollection(conversation.KeyedItems()[1:])
	testutil.NoErr(t, err)
	if conversation.Key() != slack.MessageKey("1788873542.372739") ||
		rest.Key() != slack.MessageKey("1788873600.000100") ||
		!rest.Timestamp().Equal(time.Unix(1788873600, 100000).UTC()) {
		t.Fatalf("unexpected anchors: %s -> %s", conversation.Key(), rest.Key())
	}
	if len(conversation.KeyedItems()) != 2 {
		t.Fatal("regrouping modified the original collection")
	}

	presentation, err := conversation.ContentType().(content.Presenter).Present(conversation)
	testutil.NoErr(t, err)
	if len(presentation.Cards) != 1 || presentation.Cards[0].Fields[0].Value != "2" {
		t.Fatalf("unexpected presentation: %#v", presentation)
	}

	// A generated (keyed) thread reference keeps its key through expansion.
	generated := content.NewKeyedItem(thread, "investigation-thread", at("10:00"), slack.ThreadReference{URL: threadURL})
	if _, err := expand(registry, testutil.RoundTripRecord(t, registry, generated), payload); err != nil {
		t.Fatal(err)
	}

	// Agent records must use the registered AIAgent/Message instance.
	unwired, err := content.NewRegistry(aiagent.ContentTypes(), slack.ContentTypes(aiagent.NewMessageType()))
	testutil.NoErr(t, err)
	thread, err = unwired.Lookup(slack.ThreadTypeName)
	testutil.NoErr(t, err)
	root = content.NewItem(thread, at("10:00"), slack.ThreadReference{URL: threadURL})
	if _, err := expand(unwired, root, payload); err == nil {
		t.Fatal("expected an unregistered emitted type instance to be rejected")
	}
}

// TestThreadSnapshotEdges covers the focused rejection and fallback cases:
// malformed and duplicate timestamps, invalid thread references, explicit
// empty snapshots, missing message arrays, unreadable-message fallback,
// and invalid agent mappings.
func TestThreadSnapshotEdges(t *testing.T) {
	agent := aiagent.NewMessageType()
	registry, err := content.NewRegistry([]content.ContentType{agent}, slack.ContentTypes(agent))
	testutil.NoErr(t, err)
	thread, err := registry.Lookup(slack.ThreadTypeName)
	testutil.NoErr(t, err)
	threadType := slack.ContentTypes(agent)[0].(*slack.ThreadType)
	root := content.NewItem(thread, at("10:00"), slack.ThreadReference{URL: threadURL})
	snapshotPayload := func(messages ...slack.SourceMessage) json.RawMessage {
		payload, err := json.Marshal(slack.Snapshot{Messages: messages})
		testutil.NoErr(t, err)
		return payload
	}

	// An explicit empty snapshot is valid and means no desired children.
	members, err := expand(registry, root, json.RawMessage(`{"messages":[]}`))
	testutil.NoErr(t, err)
	if len(members) != 0 {
		t.Fatalf("expected no members, got %d", len(members))
	}

	// Missing and null message arrays are errors, not empty snapshots.
	for _, payload := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`{"messages":null}`)} {
		_, err = expand(registry, root, payload)
		expectField(t, err, content.StagePayload, "messages")
	}

	// Malformed and duplicate timestamps are rejected with the offending
	// message location.
	_, err = expand(registry, root, snapshotPayload(slack.SourceMessage{Ts: "yesterday"}))
	expectField(t, err, content.StagePayload, "messages.0.ts")
	_, err = expand(registry, root, snapshotPayload(
		slack.SourceMessage{Ts: "1788873542.000000"},
		slack.SourceMessage{Ts: "1788873542.000000"},
	))
	expectField(t, err, content.StagePayload, "messages.1.ts")

	// Invalid thread references are rejected through the content schema
	// and by the expander itself.
	_, err = registry.DecodeRecord(slack.ThreadTypeName, json.RawMessage(
		`{"timestamp":"2026-10-25T10:00:00Z","content":{"url":"https://workspace.example.test/x"}}`))
	expectField(t, err, content.StageContent, "url")
	_, err = threadType.Expand(context.Background(),
		content.NewItem(thread, at("10:00"), slack.ThreadReference{URL: "https://workspace.example.test/x"}),
		snapshotPayload())
	expectField(t, err, content.StageContent, "url")

	// An entirely unreadable message keeps its identity with a visible
	// placeholder instead of failing the snapshot.
	members, err = expand(registry, root, snapshotPayload(slack.SourceMessage{Ts: "1788873542.000000"}))
	testutil.NoErr(t, err)
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members))
	}
	view, err := content.KeyedItemView[slack.Message](members[0])
	testutil.NoErr(t, err)
	if view.ContentValue.Author != "Unknown author" || view.ContentValue.Text != "No readable message content" {
		t.Fatalf("unexpected fallback content: %#v", view.ContentValue)
	}

	// Blank agent-map keys and values are rejected.
	_, err = expand(registry, root, json.RawMessage(`{"messages":[],"agents":{"U1":" "}}`))
	expectField(t, err, content.StagePayload, "agents.U1")
}

// TestThreadValidation checks the runtime schema boundaries with the Slack
// and agent types: actionable failures on raw input and cross-extension
// output, and open nested extras.
func TestThreadValidation(t *testing.T) {
	agent := aiagent.NewMessageType()
	registry, err := content.NewRegistry([]content.ContentType{agent}, slack.ContentTypes(agent))
	testutil.NoErr(t, err)
	thread, err := registry.Lookup(slack.ThreadTypeName)
	testutil.NoErr(t, err)
	root := content.NewItem(thread, at("10:00"), slack.ThreadReference{URL: threadURL})

	// Nested unknown payload fields are rejected before invocation.
	payload := json.RawMessage(`{"messages":[{"ts":"1788873542.000000","text":"b","channel":"C1"}]}`)
	_, err = expand(registry, root, payload)
	expectField(t, err, content.StagePayload, "messages.0.channel")

	// Unknown stored fields do not disappear through Go decoding.
	_, err = registry.DecodeRecord(slack.ThreadTypeName, json.RawMessage(
		`{"timestamp":"2026-10-25T10:00:00Z","content":{"url":"`+threadURL+`","channel":"C1"}}`))
	expectField(t, err, content.StageContent, "channel")

	// Arbitrary nested metadata is allowed inside declared extras.
	_, err = registry.DecodeRecord(aiagent.MessageTypeName, json.RawMessage(`{"timestamp":"2026-10-25T10:00:00Z","key":"k","content":{"agent":"a","text":"b","extras":{"slack":{"ids":[1,"2"],"thread":{"ts":"1.2"}}}}}`))
	testutil.NoErr(t, err)

	// Cross-extension output with the wrong inner value is rejected with
	// its emitted location.
	result := content.ExpansionResult{Record: root, Blocks: []content.EmittedBlock{
		{Record: content.NewKeyedItem(agent, "k", at("10:00"), slack.Message{Author: "a", Text: "b"})},
	}}
	err = registry.ValidateExpansion(root, result)
	validation := expectField(t, err, content.StageContent, "")
	if validation.ContentType != aiagent.MessageTypeName || validation.Location.String() != "block 0" {
		t.Fatalf("unexpected location: %v", validation)
	}
}

// expand runs the host sequence around a direct expander invocation:
// validate the existing record and the payload, invoke the expander, then
// check the result. It returns the emitted constituents.
func expand(registry *content.Registry, existing content.Record, payload json.RawMessage) ([]content.KeyedRecord, error) {
	if err := registry.ValidateRecord(existing); err != nil {
		return nil, err
	}
	name := existing.ContentType().Name()
	if err := registry.ValidatePayload(name, payload); err != nil {
		return nil, err
	}
	expander, err := registry.Lookup(name)
	if err != nil {
		return nil, err
	}
	result, err := expander.(content.Expander).Expand(context.Background(), existing, payload)
	if err != nil {
		return nil, err
	}
	if err := registry.ValidateExpansion(existing, result); err != nil {
		return nil, err
	}
	return result.Members(), nil
}

// expectField asserts a structured validation error of the given stage
// that reports path among its field errors (any field when path is empty).
func expectField(t *testing.T, err error, stage content.ValidationStage, path string) *content.ValidationError {
	t.Helper()
	var validation *content.ValidationError
	if !errors.As(err, &validation) || validation.Stage != stage || len(validation.Fields) == 0 {
		t.Fatalf("expected a %s validation error, got %v", stage, err)
	}
	t.Log(validation)
	for _, field := range validation.Fields {
		if path == "" || field.Path == path {
			return validation
		}
	}
	t.Fatalf("expected field %q in %v", path, validation)
	return nil
}

// group orders constituents chronologically and groups adjacent members of
// the same groupable type. It is a test stand-in, not the host algorithm.
func group(t *testing.T, members []content.KeyedRecord) []content.KeyedRecord {
	t.Helper()
	sort.SliceStable(members, func(i, j int) bool { return members[i].Timestamp().Before(members[j].Timestamp()) })
	var groups [][]content.KeyedRecord
	for _, member := range members {
		_, groupable := member.ContentType().(content.GroupableContentType)
		if n := len(groups); groupable && n > 0 && groups[n-1][0].ContentType() == member.ContentType() {
			groups[n-1] = append(groups[n-1], member)
			continue
		}
		groups = append(groups, []content.KeyedRecord{member})
	}
	var blocks []content.KeyedRecord
	for _, items := range groups {
		if groupable, ok := items[0].ContentType().(content.GroupableContentType); ok {
			collection, err := groupable.NewCollection(items)
			testutil.NoErr(t, err)
			blocks = append(blocks, collection)
			continue
		}
		blocks = append(blocks, items[0])
	}
	return blocks
}

func at(clock string) time.Time {
	value, err := time.Parse(time.RFC3339, "2026-10-25T"+clock+":00Z")
	if err != nil {
		panic(err)
	}
	return value
}
