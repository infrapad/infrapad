// Package prometheus implements Prometheus/AlertsMatcher, matching
// Prometheus alerts by labels and time interval.
package prometheus

import (
	"context"
	_ "embed"
	"encoding/json"
	"maps"
	"slices"
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
	// QueryResults are the retained producer-submitted range-query
	// results, replaced through payload submission (see Payload). They are
	// not part of the Markdown header and are cleared when the matcher
	// labels or incident bounds change.
	QueryResults *QueryResults `json:"query_results,omitempty"`
}

// AlertsMatcherType implements Prometheus/AlertsMatcher. It renders no
// Markdown body of its own: its matcher metadata belongs to the
// host-managed structured header of a block (MarkdownHeader). It is
// therefore not editable through Markdown body updates. Query results are
// ingested through payload submission (Expander).
type AlertsMatcherType struct {
	content.TypedMarkdownRenderer[content.Item[AlertsMatcher]]
	content.TypedItemMarkdownHeader[AlertsMatcher, alertsMatcherMdHeader]
}

// alertsMatcherMdHeader is the structured Markdown header of
// Prometheus/AlertsMatcher: all matcher metadata (the body is empty;
// query results are not exposed). An
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

// replaceAlertsMatcherMdHeader applies the header, preserving the query
// results only when the labels matchers and incident bounds are unchanged
// (plain structural comparison; changes of extras alone preserve them).
func replaceAlertsMatcherMdHeader(existing AlertsMatcher, h alertsMatcherMdHeader) AlertsMatcher {
	m := AlertsMatcher{LabelsMatchers: h.LabelsMatchers, Since: h.Since, Extras: h.Extras}
	if h.Until != nil {
		m.Until = *h.Until
	}
	if sameMatcherQuery(existing, m) {
		m.QueryResults = existing.QueryResults
	}
	return m
}

// sameMatcherQuery reports whether a and b select the same alerts: equal
// labels matchers (nil and empty are equal) and equal incident bounds.
func sameMatcherQuery(a, b AlertsMatcher) bool {
	return a.Since.Equal(b.Since) && a.Until.Equal(b.Until) &&
		slices.EqualFunc(a.LabelsMatchers, b.LabelsMatchers, func(x, y map[string][]string) bool {
			return maps.EqualFunc(x, y, slices.Equal[[]string])
		})
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

func (*AlertsMatcherType) PayloadSchema() content.Schema {
	return content.Schema{Source: schemaCUE, Definition: "#Payload"}
}

// Expand replaces the retained query results with the payload's (clearing
// them when omitted), leaving matcher metadata, extras, the timestamp, and
// any generated key unchanged. It emits no children and does not mutate
// existing.
func (*AlertsMatcherType) Expand(_ context.Context, existing content.Record, payload json.RawMessage) (content.ExpansionResult, error) {
	item, err := content.ItemView[AlertsMatcher](existing)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	input, err := content.DecodePayload[Payload](payload)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	if input.QueryResults != nil {
		if errs := input.QueryResults.Validate("query_results"); len(errs) > 0 {
			return content.ExpansionResult{}, &content.ValidationError{ContentType: AlertsMatcherTypeName, Stage: content.StagePayload, Fields: errs}
		}
	}
	matcher := item.ContentValue
	matcher.QueryResults = input.QueryResults
	primary, err := content.WithContent(existing, matcher)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	return content.ExpansionResult{Record: primary}, nil
}

func renderAlertsMatcher(item content.Item[AlertsMatcher]) (string, error) {
	return "", nil
}
