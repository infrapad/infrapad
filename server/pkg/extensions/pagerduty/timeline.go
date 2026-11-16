package pagerduty

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/infrapad/infrapad/server/pkg/content"
)

// TimelineType implements PagerDuty/Timeline, a groupable type whose
// records are host-selected collections of keyed incident notes and log
// entries.
type TimelineType struct {
	content.TypedMarkdownRenderer[content.GroupableCollection[TimelineEntry]]
}

func NewTimelineType() *TimelineType {
	return &TimelineType{TypedMarkdownRenderer: content.CollectionMarkdown(renderTimeline)}
}

func (*TimelineType) Name() string { return TimelineTypeName }

// ContentSchema describes a single timeline entry, not the collection.
func (*TimelineType) ContentSchema() content.Schema {
	return content.Schema{Source: schemaCUE, Definition: "#TimelineEntry"}
}

func (*TimelineType) DecodeJSON(data json.RawMessage) (content.Content, error) {
	return content.DecodeContent[TimelineEntry](data)
}

func (t *TimelineType) NewCollection(items []content.KeyedRecord) (content.GroupableRecord, error) {
	return content.NewCollection[TimelineEntry](t, items)
}

// NoteKey is the generated key of the timeline member emitted for a source
// note.
func NoteKey(id string) string { return "pagerduty-note:" + id }

// LogEntryKey is the generated key of the timeline member emitted for a
// source log entry.
func LogEntryKey(id string) string { return "pagerduty-log-entry:" + id }

// renderTimeline renders each member with author, full UTC timestamp,
// entry type, source link, and the multiline body on separate indented
// lines (following Slack/Conversation).
func renderTimeline(collection content.GroupableCollection[TimelineEntry]) (string, error) {
	var out strings.Builder
	for _, item := range collection.Items {
		entry := item.ContentValue
		fmt.Fprintf(&out, "- **%s** · %s · %s · [source](%s)\n",
			entry.Author, item.Timestamp().UTC().Format(timestampFormat), entryLabel(entry), entry.URL)
		for _, line := range strings.Split(entry.Text, "\n") {
			if line != "" {
				out.WriteString("  ")
				out.WriteString(line)
			}
			out.WriteString("\n")
		}
	}
	return out.String(), nil
}

// entryLabel is the displayed type of a timeline entry: "note", or the
// original log-entry type (falling back to "log entry").
func entryLabel(entry TimelineEntry) string {
	switch {
	case entry.Kind == TimelineKindNote:
		return "note"
	case entry.EntryType != "":
		return "`" + entry.EntryType + "`"
	default:
		return "log entry"
	}
}

func (*TimelineType) Present(record content.Record) (content.Presentation, error) {
	collection, err := content.CollectionView[TimelineEntry](record)
	if err != nil {
		return content.Presentation{}, err
	}
	markdown, err := renderTimeline(collection)
	if err != nil {
		return content.Presentation{}, err
	}
	card := content.Card{
		Title:    "PagerDuty timeline",
		Markdown: markdown,
	}
	if len(collection.Items) > 0 {
		card.Links = []content.Link{{Label: "Open in PagerDuty", URL: collection.Items[0].ContentValue.URL}}
	}
	return content.Presentation{Cards: []content.Card{card}}, nil
}
