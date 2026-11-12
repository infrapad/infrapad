package cora

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/aiagent"
)

// ReportType implements Cora/Report, an expandable standalone reference
// to one Cora report.
//
// The Markdown header exposes the report_id/url reference. Initial values
// are accepted when the block is created through Markdown; afterward only
// an unchanged header is accepted. The body is read-only (no
// MarkdownUpdater).
type ReportType struct {
	content.TypedMarkdownRenderer[content.Item[Report]]
	header    content.TypedItemMarkdownHeader[Report, Report]
	agentType *aiagent.MessageType
}

// NewReportType constructs Cora/Report. agentType is the registered
// AIAgent/Message implementation used for the emitted report message.
func NewReportType(agentType *aiagent.MessageType) *ReportType {
	return &ReportType{
		TypedMarkdownRenderer: content.ItemMarkdown(renderReport),
		header: content.ItemMarkdownHeader(
			func(r Report) Report { return r },
			func(_ Report, h Report) Report { return h },
		),
		agentType: agentType,
	}
}

func (*ReportType) Name() string { return ReportTypeName }

func (*ReportType) ContentSchema() content.Schema {
	return content.Schema{Source: schemaCUE, Definition: "#Report"}
}

func (*ReportType) PayloadSchema() content.Schema {
	return content.Schema{Source: schemaCUE, Definition: "#Payload"}
}

func (*ReportType) DecodeJSON(data json.RawMessage) (content.Content, error) {
	return content.DecodeContent[Report](data)
}

func (t *ReportType) Present(record content.Record) (content.Presentation, error) {
	item, err := content.ItemView[Report](record)
	if err != nil {
		return content.Presentation{}, err
	}
	report := item.ContentValue
	card := content.Card{
		Title:  "Cora report",
		Fields: []content.Field{{Label: "Report", Value: report.ReportID}},
	}
	if report.URL != "" {
		card.Links = []content.Link{{Label: "Open in Cora", URL: report.URL}}
	}
	return content.Presentation{Cards: []content.Card{card}}, nil
}

func (t *ReportType) MarkdownHeader(existing content.Record) (content.MarkdownFields, error) {
	return t.header.MarkdownHeader(existing)
}

// UpdateMarkdownHeader applies the report_id/url reference. A blank stored
// report_id identifies the empty seed of a Markdown-created block, which
// accepts initial values; a persisted reference (always with a nonblank
// report_id) accepts only an unchanged header, so Markdown round-trips
// still work. Pointing at another report requires a new block.
func (t *ReportType) UpdateMarkdownHeader(existing content.Record, fields content.MarkdownFields) (content.Record, error) {
	item, err := content.ItemView[Report](existing)
	if err != nil {
		return nil, err
	}
	if current := item.ContentValue; current.ReportID != "" {
		header, err := content.DecodeMarkdownFields[Report](fields)
		if err != nil {
			return nil, &content.ValidationError{ContentType: ReportTypeName, Stage: content.StageContent, Err: err}
		}
		const locked = "the Cora report reference cannot change; create a new Cora/Report block instead"
		var violations []content.FieldError
		if header.ReportID != current.ReportID {
			violations = append(violations, content.FieldError{Path: "report_id", Message: locked})
		}
		if header.URL != current.URL {
			violations = append(violations, content.FieldError{Path: "url", Message: locked})
		}
		if len(violations) > 0 {
			return nil, &content.ValidationError{ContentType: ReportTypeName, Stage: content.StageContent, Fields: violations}
		}
	}
	return t.header.UpdateMarkdownHeader(existing, fields)
}

// MessageKey is the generated key of the AIAgent/Message emitted for a
// report.
func MessageKey(reportID string) string { return "cora-report:" + reportID }

// Expand emits exactly one AIAgent/Message with the verbatim report text,
// attributed to Cora and linked to the reference URL. Both the primary and
// the message take the payload's source timestamp. existing is not
// modified.
func (t *ReportType) Expand(_ context.Context, existing content.Record, payload json.RawMessage) (content.ExpansionResult, error) {
	item, err := content.ItemView[Report](existing)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	input, err := content.DecodePayload[Payload](payload)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	report := item.ContentValue
	if violations := validatePayload(report, input); len(violations) > 0 {
		return content.ExpansionResult{}, &content.ValidationError{ContentType: ReportTypeName, Stage: content.StagePayload, Fields: violations}
	}

	at := input.Timestamp.UTC()
	primary, err := retimestamped(existing, at, report)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	message := content.NewKeyedItem(t.agentType, MessageKey(report.ReportID), at, aiagent.Message{
		Agent: AgentName,
		Text:  input.Text,
		URL:   report.URL,
	})
	return content.ExpansionResult{
		Record: primary,
		Blocks: []content.EmittedBlock{{Record: message}},
	}, nil
}

// validatePayload checks what the payload schema cannot express: the
// report matches the reference and the source timestamp is nonzero.
func validatePayload(report Report, input Payload) []content.FieldError {
	var violations []content.FieldError
	if input.ReportID != report.ReportID {
		violations = append(violations, content.FieldError{Path: "report_id",
			Message: fmt.Sprintf("payload report %q does not match the referenced report %q", input.ReportID, report.ReportID)})
	}
	if input.Timestamp.IsZero() {
		violations = append(violations, content.FieldError{Path: "timestamp", Message: "must be a nonzero time"})
	}
	return violations
}

// retimestamped returns a copy of the primary at the given event time,
// preserving its type, content, and any generated key.
func retimestamped(existing content.Record, at time.Time, report Report) (content.Record, error) {
	if keyed, ok := existing.(content.KeyedRecord); ok {
		return content.NewKeyedItem(existing.ContentType(), keyed.Key(), at, report), nil
	}
	if _, ok := existing.(content.ItemRecord); !ok {
		return nil, fmt.Errorf("expected an individual report record, got %T", existing)
	}
	return content.NewItem(existing.ContentType(), at, report), nil
}

// renderReport renders the report label, linked when a source URL exists.
// The report text is rendered by the generated AIAgent/Message.
func renderReport(item content.Item[Report]) (string, error) {
	report := item.ContentValue
	label := "Cora report " + escapeMarkdownLabel(report.ReportID)
	if report.URL == "" {
		return label + "\n", nil
	}
	return fmt.Sprintf("[%s](%s)\n", label, report.URL), nil
}

// escapeMarkdownLabel escapes square brackets so labels stay inside
// Markdown link text.
func escapeMarkdownLabel(s string) string {
	s = strings.ReplaceAll(s, "[", "\\[")
	return strings.ReplaceAll(s, "]", "\\]")
}
