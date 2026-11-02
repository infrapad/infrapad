package slack_test

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/aiagent"
	"github.com/infrapad/infrapad/server/pkg/extensions/slack"
)

// TestThreadSnapshotScenario exercises the contracts end to end through the
// registry: expansion of a snapshot with an interleaved agent message,
// host-side checks, grouping (a minimal stand-in for the host's grouping
// engine), record round-trips, rendering, and presentation.
func TestThreadSnapshotScenario(t *testing.T) {
	agent := aiagent.NewMessageType()
	registry, err := content.NewRegistry([]content.ContentType{agent}, slack.ContentTypes(agent))
	must(t, err)

	thread, err := registry.Lookup(slack.ThreadTypeName)
	must(t, err)
	root := content.NewItem(thread, at("10:00"), slack.ThreadReference{URL: "https://slack.example/archives/C1/p1"})
	reloadedRoot := roundTrip(t, registry, root)
	if reloadedRoot.ContentType() != thread || !reloadedRoot.Timestamp().Equal(root.Timestamp()) {
		t.Fatalf("root metadata not restored: %#v", reloadedRoot)
	}

	snapshot := slack.Snapshot{Messages: []slack.SourceMessage{
		{ID: "1", Timestamp: at("10:00"), Author: "Alice", Text: "Investigating."},
		{ID: "2", Timestamp: at("10:01"), Author: "Bob", Text: "Checking the node."},
		{ID: "3", Timestamp: at("10:02"), Author: "investigation-agent", Text: "Node unhealthy.", Agent: true},
		{ID: "4", Timestamp: at("10:03"), Author: "Alice", Text: "Replacement started."},
	}}
	payload, err := json.Marshal(snapshot)
	must(t, err)
	result, err := thread.(content.Expander).Expand(context.Background(), reloadedRoot, payload)
	must(t, err)
	members, err := registry.CheckExpansion(reloadedRoot, result)
	must(t, err)
	if len(members) != 4 {
		t.Fatalf("expected 4 emitted members, got %d", len(members))
	}

	blocks := group(t, members)
	var markdown []string
	for _, block := range blocks {
		reloaded := roundTrip(t, registry, block)
		body, err := reloaded.ContentType().RenderMarkdown(reloaded)
		must(t, err)
		markdown = append(markdown, reloaded.ContentType().Name()+"\n"+body)
	}
	want := []string{
		"Slack/Conversation\n- 10:00 **Alice:** Investigating.\n- 10:01 **Bob:** Checking the node.\n",
		"AIAgent/Message\n### investigation-agent\n\nNode unhealthy.\n",
		"Slack/Conversation\n- 10:03 **Alice:** Replacement started.\n",
	}
	if strings.Join(markdown, "|") != strings.Join(want, "|") {
		t.Fatalf("unexpected rendering:\n%s", strings.Join(markdown, "\n---\n"))
	}
	agentItem, err := content.KeyedItemView[aiagent.Message](blocks[1])
	must(t, err)
	if agentItem.Key() != slack.MessageKey("3") || agentItem.ContentValue.Extras["slack_message_id"] != "3" {
		t.Fatalf("unexpected agent record: %#v", agentItem)
	}

	// Regroup the first conversation from stored constituents without the
	// original payload: dropping the first member moves the anchor.
	first := blocks[0].(content.GroupableRecord)
	rest, err := first.ContentType().(content.GroupableContentType).NewCollection(first.KeyedItems()[1:])
	must(t, err)
	if first.Key() != slack.MessageKey("1") || rest.Key() != slack.MessageKey("2") || !rest.Timestamp().Equal(at("10:01")) {
		t.Fatalf("unexpected anchors: %s -> %s", first.Key(), rest.Key())
	}
	if len(first.KeyedItems()) != 2 {
		t.Fatal("regrouping modified the original collection")
	}

	presentation, err := first.ContentType().(content.Presenter).Present(first)
	must(t, err)
	if len(presentation.Cards) != 1 || presentation.Cards[0].Fields[0].Value != "2" {
		t.Fatalf("unexpected presentation: %#v", presentation)
	}

	// A generated (keyed) thread reference keeps its key through expansion.
	generated := content.NewKeyedItem(thread, "investigation-thread", at("10:00"), slack.ThreadReference{URL: "https://slack.example/x"})
	result, err = thread.(content.Expander).Expand(context.Background(), roundTrip(t, registry, generated), payload)
	must(t, err)
	if _, err := registry.CheckExpansion(generated, result); err != nil {
		t.Fatal(err)
	}

	// Agent records must use the registered AIAgent/Message instance.
	unwired, err := content.NewRegistry(aiagent.ContentTypes(), slack.ContentTypes(aiagent.NewMessageType()))
	must(t, err)
	thread, err = unwired.Lookup(slack.ThreadTypeName)
	must(t, err)
	root = content.NewItem(thread, at("10:00"), slack.ThreadReference{URL: "https://slack.example/x"})
	result, err = thread.(content.Expander).Expand(context.Background(), root, payload)
	must(t, err)
	if _, err := unwired.CheckExpansion(root, result); err == nil {
		t.Fatal("expected an unregistered emitted type instance to be rejected")
	}
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
			must(t, err)
			blocks = append(blocks, collection)
			continue
		}
		blocks = append(blocks, items[0])
	}
	return blocks
}

// roundTrip encodes and decodes a record, checking the encoding is stable.
func roundTrip[R content.Record](t *testing.T, registry *content.Registry, record R) content.Record {
	t.Helper()
	data, err := content.EncodeRecord(record)
	must(t, err)
	decoded, err := registry.DecodeRecord(record.ContentType().Name(), data)
	must(t, err)
	again, err := content.EncodeRecord(decoded)
	must(t, err)
	if !bytes.Equal(data, again) {
		t.Fatalf("record changed through codec:\n%s\n%s", data, again)
	}
	return decoded
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
