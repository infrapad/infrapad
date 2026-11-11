// Package prometheus implements Prometheus/AlertsMatcher, matching
// Prometheus alerts by labels and time interval.
package prometheus

import (
	_ "embed"
	"encoding/json"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
)

// ContentTypes returns the content types of the Prometheus extension.
func ContentTypes() []content.ContentType {
	return []content.ContentType{NewAlertsMatcherType()}
}

//go:embed schema.cue
var schemaCUE []byte

// AlertsMatcherTypeName is the canonical name of the alert-matcher type.
const AlertsMatcherTypeName = "Prometheus/AlertsMatcher"

// AlertsMatcher is the inner content of a Prometheus/AlertsMatcher record.
//
// LabelsMatchers semantics, preserved from the legacy model implementation:
// alternatives across list entries, AND across label names within one
// entry, OR across the values of one label. A zero Until (or its omission)
// means an open-ended interval.
type AlertsMatcher struct {
	LabelsMatchers []map[string][]string `json:"labels_matchers,omitempty"`
	Since          time.Time             `json:"since"`
	Until          time.Time             `json:"until,omitempty"`
	// Extras carries additional metadata not anticipated by the schema.
	Extras map[string]any `json:"extras,omitempty"`
}

// AlertsMatcherType implements Prometheus/AlertsMatcher. It renders no
// Markdown body of its own: all its fields belong to the host-managed
// structured header of a block (MarkdownHeader). It is therefore not
// editable through Markdown body updates.
type AlertsMatcherType struct {
	content.TypedMarkdownRenderer[content.Item[AlertsMatcher]]
	content.TypedItemMarkdownHeader[AlertsMatcher, alertsMatcherMdHeader]
}

// alertsMatcherMdHeader is the structured Markdown header of
// Prometheus/AlertsMatcher: all content fields (the body is empty). An
// open-ended interval omits until instead of exposing a zero time.
type alertsMatcherMdHeader struct {
	LabelsMatchers []map[string][]string `json:"labels_matchers,omitempty"`
	Since          time.Time             `json:"since"`
	Until          *time.Time            `json:"until,omitempty"`
	Extras         map[string]any        `json:"extras,omitempty"`
}

func projectAlertsMatcherMdHeader(m AlertsMatcher) alertsMatcherMdHeader {
	header := alertsMatcherMdHeader{LabelsMatchers: m.LabelsMatchers, Since: m.Since, Extras: m.Extras}
	if !m.Until.IsZero() {
		until := m.Until
		header.Until = &until
	}
	return header
}

func replaceAlertsMatcherMdHeader(_ AlertsMatcher, h alertsMatcherMdHeader) AlertsMatcher {
	m := AlertsMatcher{LabelsMatchers: h.LabelsMatchers, Since: h.Since, Extras: h.Extras}
	if h.Until != nil {
		m.Until = *h.Until
	}
	return m
}

// NewAlertsMatcherType constructs the Prometheus/AlertsMatcher implementation.
func NewAlertsMatcherType() *AlertsMatcherType {
	return &AlertsMatcherType{
		TypedMarkdownRenderer:   content.ItemMarkdown(renderAlertsMatcher),
		TypedItemMarkdownHeader: content.ItemMarkdownHeader(projectAlertsMatcherMdHeader, replaceAlertsMatcherMdHeader),
	}
}

func (*AlertsMatcherType) Name() string { return AlertsMatcherTypeName }

func (*AlertsMatcherType) ContentSchema() content.Schema {
	return content.Schema{Source: schemaCUE, Definition: "#AlertsMatcher"}
}

func (*AlertsMatcherType) DecodeJSON(data json.RawMessage) (content.Content, error) {
	return content.DecodeContent[AlertsMatcher](data)
}

func renderAlertsMatcher(item content.Item[AlertsMatcher]) (string, error) {
	return "", nil
}
