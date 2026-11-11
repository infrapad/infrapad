// Package pagerduty implements the PagerDuty integration: PagerDuty/Incident
// references enriched from transient incident payloads with a Markdown
// summary, generated PagerDuty/Timeline notes and log entries, and an
// optional Prometheus/AlertsMatcher derived from the alert details.
package pagerduty

import (
	_ "embed"
	"encoding/json"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/prometheus"
)

//go:embed schema.cue
var schemaCUE []byte

const (
	IncidentTypeName = "PagerDuty/Incident"
	TimelineTypeName = "PagerDuty/Timeline"
)

// Incident is the inner content of a PagerDuty/Incident primary record.
type Incident struct {
	// ID is the PagerDuty incident ID.
	ID string `json:"id"`
	// URL is the absolute HTTP(S) incident link; the hostname is not fixed.
	URL string `json:"url"`
	// Text is the Markdown incident summary produced by the last
	// successful payload submission (see FormatIncidentSummary). It is
	// omitted before the first successful submission; a present value is
	// nonblank and locks the ID/URL reference against changes.
	Text string `json:"text,omitempty"`
}

// Enriched reports whether the incident has a summary from a successful
// payload submission.
func (i Incident) Enriched() bool { return i.Text != "" }

// TimelineKind is the source category of a timeline member.
type TimelineKind string

const (
	TimelineKindNote     TimelineKind = "note"
	TimelineKindLogEntry TimelineKind = "log_entry"
)

// TimelineEntry is the inner content of one PagerDuty/Timeline member: a
// normalized incident note or log entry. Its stable key and event time
// belong to the host record.
type TimelineEntry struct {
	// Text is the note content or log-entry summary, as Markdown.
	Text string `json:"text"`
	// Author is the resolved author/actor name, falling back to the source
	// author/actor ID and then an explicit unknown-author label.
	Author string `json:"author"`
	// Kind is the source category of the entry.
	Kind TimelineKind `json:"kind"`
	// EntryType is the original log-entry type (e.g.
	// "trigger_log_entry"), preserved as supplied, including unfamiliar
	// values; omitted when unavailable.
	EntryType string `json:"entry_type,omitempty"`
	// URL is the absolute HTTP(S) incident source link.
	URL string `json:"url"`
}

// Payload is the transient input of a PagerDuty/Incident expansion: the
// complete source state of one incident, modeled on the demo's IncidentBundle.
// External producers fetch and map the source data; the server only
// validates and interprets it. It is never persisted.
//
// Collections must be explicit arrays: an empty array is valid, while a
// missing or null array is invalid (checked semantically by expansion).
type Payload struct {
	Incident SourceIncident `json:"incident"`
	Alerts   []SourceAlert  `json:"alerts"`
	Notes    []SourceNote   `json:"notes"`
	// LogEntries are the incident log entries in chronological order.
	LogEntries []SourceLogEntry `json:"log_entries"`
}

// SourceIncident holds the core details of the incident. Display fields
// may be empty; the summary renders readable placeholders for them.
type SourceIncident struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	Title       string    `json:"title,omitempty"`
	Description string    `json:"description,omitempty"`
	Status      string    `json:"status,omitempty"`
	Urgency     string    `json:"urgency,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ServiceID   string    `json:"service_id,omitempty"`
	ServiceName string    `json:"service_name,omitempty"`
}

// SourceAlert is one alert attached to the incident.
type SourceAlert struct {
	ID        string    `json:"id"`
	Summary   string    `json:"summary,omitempty"`
	Status    string    `json:"status,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Details is the alert body's arbitrary "details" JSON, interpreted
	// server-side by matcher derivation. It is the only open part of the
	// payload schema and is never persisted.
	Details json.RawMessage `json:"details,omitempty"`
}

// SourceNote is one human-written incident note.
type SourceNote struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	AuthorID   string    `json:"author_id,omitempty"`
	AuthorName string    `json:"author_name,omitempty"`
	Content    string    `json:"content,omitempty"`
}

// SourceLogEntry is one incident log entry.
type SourceLogEntry struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	// Type is the original log-entry type, preserved as supplied.
	Type      string `json:"type,omitempty"`
	Summary   string `json:"summary,omitempty"`
	ActorID   string `json:"actor_id,omitempty"`
	ActorName string `json:"actor_name,omitempty"`
}

// ContentTypes returns the PagerDuty content types. matcherType is the
// registered Prometheus/AlertsMatcher implementation used for emitted
// matcher children; it must be the instance registered in the host
// registry (it is not included in the returned types).
func ContentTypes(matcherType *prometheus.AlertsMatcherType) []content.ContentType {
	timelineType := NewTimelineType()
	return []content.ContentType{NewIncidentType(timelineType, matcherType), timelineType}
}
