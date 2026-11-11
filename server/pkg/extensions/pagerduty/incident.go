package pagerduty

import (
	"encoding/json"
	"fmt"

	"github.com/infrapad/infrapad/server/pkg/content"
)

// IncidentType implements PagerDuty/Incident, an expandable primary
// reference whose content is enriched with a Markdown summary.
//
// The Markdown header exposes only the ID/URL reference, editable before
// the first successful payload submission. The summary body is read-only
// (no MarkdownUpdater).
type IncidentType struct {
	content.TypedMarkdownRenderer[content.Item[Incident]]
	header       content.TypedItemMarkdownHeader[Incident, incidentMdHeader]
	timelineType *TimelineType
}

// incidentMdHeader is the structured Markdown header of PagerDuty/Incident:
// the reference only; the summary text is the body.
type incidentMdHeader struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

func NewIncidentType(timelineType *TimelineType) *IncidentType {
	return &IncidentType{
		TypedMarkdownRenderer: content.ItemMarkdown(renderIncident),
		header: content.ItemMarkdownHeader(
			func(i Incident) incidentMdHeader { return incidentMdHeader{ID: i.ID, URL: i.URL} },
			func(i Incident, h incidentMdHeader) Incident { i.ID, i.URL = h.ID, h.URL; return i },
		),
		timelineType: timelineType,
	}
}

func (*IncidentType) Name() string { return IncidentTypeName }

func (*IncidentType) ContentSchema() content.Schema {
	return content.Schema{Source: schemaCUE, Definition: "#Incident"}
}

func (*IncidentType) PayloadSchema() content.Schema {
	return content.Schema{Source: schemaCUE, Definition: "#Payload"}
}

func (*IncidentType) DecodeJSON(data json.RawMessage) (content.Content, error) {
	return content.DecodeContent[Incident](data)
}

// renderIncident renders the stored summary, or a plain incident link
// before the first successful payload submission.
func renderIncident(item content.Item[Incident]) (string, error) {
	incident := item.ContentValue
	if incident.Enriched() {
		return incident.Text, nil
	}
	return fmt.Sprintf("[PagerDuty incident %s](%s)\n", escapeMarkdownLabel(incident.ID), incident.URL), nil
}

func (t *IncidentType) Present(record content.Record) (content.Presentation, error) {
	item, err := content.ItemView[Incident](record)
	if err != nil {
		return content.Presentation{}, err
	}
	markdown, err := renderIncident(item)
	if err != nil {
		return content.Presentation{}, err
	}
	return content.Presentation{Cards: []content.Card{{
		Title:    "PagerDuty incident",
		Markdown: markdown,
		Fields:   []content.Field{{Label: "Incident", Value: item.ContentValue.ID}},
		Links:    []content.Link{{Label: "Open in PagerDuty", URL: item.ContentValue.URL}},
	}}}, nil
}

func (t *IncidentType) MarkdownHeader(existing content.Record) (content.MarkdownFields, error) {
	return t.header.MarkdownHeader(existing)
}

// UpdateMarkdownHeader applies an edited ID/URL reference. Changes are
// permitted only before the first successful payload submission; afterward
// only an unchanged header is accepted (Markdown round-trips), and targeting
// another incident requires a new primary.
func (t *IncidentType) UpdateMarkdownHeader(existing content.Record, fields content.MarkdownFields) (content.Record, error) {
	item, err := content.ItemView[Incident](existing)
	if err != nil {
		return nil, err
	}
	if item.ContentValue.Enriched() {
		header, err := content.DecodeMarkdownFields[incidentMdHeader](fields)
		if err != nil {
			return nil, &content.ValidationError{ContentType: IncidentTypeName, Stage: content.StageContent, Err: err}
		}
		var violations []content.FieldError
		const locked = "the incident reference cannot change after a successful payload submission; create a new PagerDuty/Incident block instead"
		if header.ID != item.ContentValue.ID {
			violations = append(violations, content.FieldError{Path: "id", Message: locked})
		}
		if header.URL != item.ContentValue.URL {
			violations = append(violations, content.FieldError{Path: "url", Message: locked})
		}
		if len(violations) > 0 {
			return nil, &content.ValidationError{ContentType: IncidentTypeName, Stage: content.StageContent, Fields: violations}
		}
	}
	return t.header.UpdateMarkdownHeader(existing, fields)
}
