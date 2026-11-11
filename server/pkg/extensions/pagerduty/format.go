package pagerduty

import (
	"fmt"
	"strings"
)

// This file holds the deterministic Markdown formatting of incident
// payloads: the incident summary stored in Incident.Text and the timeline
// attribution helpers used by expansion.

// timestampFormat is the readable UTC format of rendered timestamps.
const timestampFormat = "2006-01-02 15:04:05 MST"

// unknownValue is the readable placeholder of missing display values.
const unknownValue = "_unknown_"

// unknownAuthor labels timeline entries without any resolvable author.
const unknownAuthor = "Unknown author"

// FormatIncidentSummary renders the nonblank Markdown incident summary
// from typed incident metadata and alert summaries: incident link, title,
// service, status, urgency, creation time, the description, and attached
// alerts in the given order. Missing display values get readable
// placeholders instead of failing.
func FormatIncidentSummary(incident SourceIncident, alerts []SourceAlert) string {
	var out strings.Builder
	link := escapeMarkdownLabel(incident.ID)
	if incident.URL != "" {
		link = fmt.Sprintf("[%s](%s)", link, incident.URL)
	}
	fmt.Fprintf(&out, "- **Incident**: %s\n", link)
	fmt.Fprintf(&out, "- **Title**: %s\n", orUnknown(incident.Title))
	fmt.Fprintf(&out, "- **Service**: %s\n", service(incident))
	fmt.Fprintf(&out, "- **Status**: %s\n", orUnknown(incident.Status))
	fmt.Fprintf(&out, "- **Urgency**: %s\n", orUnknown(incident.Urgency))
	created := unknownValue
	if !incident.CreatedAt.IsZero() {
		created = incident.CreatedAt.UTC().Format(timestampFormat)
	}
	fmt.Fprintf(&out, "- **Created**: %s\n", created)
	if description := strings.TrimSpace(incident.Description); description != "" && description != strings.TrimSpace(incident.Title) {
		fmt.Fprintf(&out, "\n**Description**\n\n%s\n", description)
	}
	if len(alerts) > 0 {
		out.WriteString("\n**Alerts**\n\n")
		for _, alert := range alerts {
			fmt.Fprintf(&out, "- **%s** (%s, created %s): %s\n",
				orUnknown(alert.ID), orUnknown(alert.Status),
				alert.CreatedAt.UTC().Format(timestampFormat), orUnknown(singleLine(alert.Summary)))
		}
	}
	return out.String()
}

// service renders "name (id)", either part alone, or a placeholder.
func service(incident SourceIncident) string {
	switch {
	case incident.ServiceName != "" && incident.ServiceID != "":
		return fmt.Sprintf("%s (%s)", incident.ServiceName, incident.ServiceID)
	case incident.ServiceName != "":
		return incident.ServiceName
	case incident.ServiceID != "":
		return incident.ServiceID
	default:
		return unknownValue
	}
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return unknownValue
	}
	return s
}

// singleLine keeps multiline values inside their list item.
func singleLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// attribution returns the display author: the resolved name, then the
// source ID, then an explicit unknown-author label.
func attribution(name, id string) string {
	switch {
	case strings.TrimSpace(name) != "":
		return name
	case strings.TrimSpace(id) != "":
		return id
	default:
		return unknownAuthor
	}
}

// NoteTimelineEntry normalizes a source note into a timeline entry linking to the
// incident URL.
func NoteTimelineEntry(note SourceNote, incidentURL string) TimelineEntry {
	return TimelineEntry{
		Text:   note.Content,
		Author: attribution(note.AuthorName, note.AuthorID),
		Kind:   TimelineKindNote,
		URL:    incidentURL,
	}
}

// LogTimelineEntry normalizes a source log entry into a timeline entry
// linking to the incident URL, preserving its original type.
func LogTimelineEntry(entry SourceLogEntry, incidentURL string) TimelineEntry {
	return TimelineEntry{
		Text:      entry.Summary,
		Author:    attribution(entry.ActorName, entry.ActorID),
		Kind:      TimelineKindLogEntry,
		EntryType: entry.Type,
		URL:       incidentURL,
	}
}

// escapeMarkdownLabel escapes square brackets so labels stay inside
// Markdown link text.
func escapeMarkdownLabel(s string) string {
	s = strings.ReplaceAll(s, "[", "\\[")
	return strings.ReplaceAll(s, "]", "\\]")
}
