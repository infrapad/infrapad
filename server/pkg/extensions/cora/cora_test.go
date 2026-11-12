package cora_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/aiagent"
	"github.com/infrapad/infrapad/server/pkg/extensions/cora"
)

const reportURL = "https://cora.example.test/reports/r-1"

// reportText is report Markdown with embedded headings and a code fence,
// which must be preserved verbatim.
const reportText = "# Root cause\n\nPool exhaustion.\n\n```sh\nkubectl get pods\n```\n"

// TestReportExpansion checks the mapping to one AIAgent/Message, source
// timestamps, and payload validation through the registry.
func TestReportExpansion(t *testing.T) {
	registry, reportType := newRegistry(t)
	at := time.Date(2026, 1, 1, 10, 5, 0, 0, time.FixedZone("CET", 3600))
	reference := content.NewItem(reportType, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		cora.Report{ReportID: "r-1", URL: reportURL})

	result, err := expand(registry, reference, map[string]any{"report_id": "r-1", "timestamp": at, "text": reportText})
	testutil.NoErr(t, err)
	primary, err := content.ItemView[cora.Report](result.Record)
	testutil.NoErr(t, err)
	if !primary.Timestamp().Equal(at) || primary.ContentValue != (cora.Report{ReportID: "r-1", URL: reportURL}) {
		t.Fatalf("unexpected primary: %#v", primary)
	}
	if len(result.Blocks) != 1 {
		t.Fatalf("expected one emitted block, got %d", len(result.Blocks))
	}
	message, err := content.KeyedItemView[aiagent.Message](testutil.RoundTripRecord(t, registry, result.Blocks[0].Record))
	testutil.NoErr(t, err)
	want := aiagent.Message{Agent: "Cora", Text: reportText, URL: reportURL}
	if message.Key() != cora.MessageKey("r-1") || !message.Timestamp().Equal(at) ||
		message.ContentValue.Agent != want.Agent || message.ContentValue.Text != want.Text ||
		message.ContentValue.URL != want.URL || message.ContentValue.Extras != nil {
		t.Fatalf("unexpected message: %#v", message)
	}

	for name, tc := range map[string]struct {
		payload map[string]any
		field   string
	}{
		"mismatched id": {map[string]any{"report_id": "r-2", "timestamp": at, "text": "x"}, "report_id"},
		"zero time":     {map[string]any{"report_id": "r-1", "timestamp": time.Time{}, "text": "x"}, "timestamp"},
		"blank text":    {map[string]any{"report_id": "r-1", "timestamp": at, "text": " \n"}, "text"},
		"unknown field": {map[string]any{"report_id": "r-1", "timestamp": at, "text": "x", "sections": []any{}}, "sections"},
	} {
		_, err := expand(registry, reference, tc.payload)
		var validation *content.ValidationError
		if !errors.As(err, &validation) || validation.Stage != content.StagePayload {
			t.Fatalf("%s: expected payload validation error, got %v", name, err)
		}
		if validation.Fields != nil && validation.Fields[0].Path != tc.field {
			t.Fatalf("%s: expected field %s, got %v", name, tc.field, validation.Fields)
		}
	}
}

// TestReportTimestampRefresh checks that every valid payload sets the
// primary and message timestamps exactly to the payload source time,
// moving them earlier or later across sequential refreshes.
func TestReportTimestampRefresh(t *testing.T) {
	registry, reportType := newRegistry(t)
	base := time.Date(2026, 1, 1, 10, 5, 0, 0, time.FixedZone("CET", 3600))
	reference := content.NewItem(reportType, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		cora.Report{ReportID: "r-1", URL: reportURL})

	// Later, then earlier, then a fractional non-UTC time; each refresh is
	// authoritative and keeps the exact source time, normalized to UTC.
	fractional := time.Date(2026, 1, 1, 7, 5, 6, 123456789, time.FixedZone("EST", -5*3600))
	for i, want := range []time.Time{base.Add(time.Hour), base.Add(-90 * time.Minute), fractional} {
		result, err := expand(registry, reference, map[string]any{"report_id": "r-1", "timestamp": want, "text": reportText})
		testutil.NoErr(t, err)
		primary, err := content.ItemView[cora.Report](result.Record)
		testutil.NoErr(t, err)
		if !primary.Timestamp().Equal(want) || primary.Timestamp().Location() != time.UTC ||
			primary.ContentValue != (cora.Report{ReportID: "r-1", URL: reportURL}) {
			t.Fatalf("step %d: unexpected primary: %#v", i, primary)
		}
		message, err := content.KeyedItemView[aiagent.Message](result.Blocks[0].Record)
		testutil.NoErr(t, err)
		if !message.Timestamp().Equal(want) || message.Key() != cora.MessageKey("r-1") {
			t.Fatalf("step %d: unexpected message: %#v", i, message)
		}
	}
	if original, _ := content.ItemView[cora.Report](reference); !original.Timestamp().Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("expansion mutated the input record")
	}

	// A keyed primary is copied with its key preserved.
	keyed := content.NewKeyedItem(reportType, "generated-report", base, cora.Report{ReportID: "r-1", URL: reportURL})
	result, err := expand(registry, keyed, map[string]any{"report_id": "r-1", "timestamp": fractional, "text": reportText})
	testutil.NoErr(t, err)
	view, err := content.KeyedItemView[cora.Report](result.Record)
	testutil.NoErr(t, err)
	if view.Key() != "generated-report" || !view.Timestamp().Equal(fractional) ||
		view.ContentValue != (cora.Report{ReportID: "r-1", URL: reportURL}) {
		t.Fatalf("unexpected keyed primary: %#v", view)
	}
}

// TestReportMarkdown checks rendering, presentation, reference validation,
// and the header rules: initial values on a seed, then immutability.
func TestReportMarkdown(t *testing.T) {
	registry, reportType := newRegistry(t)
	if _, editable := any(reportType).(content.MarkdownUpdater); editable {
		t.Fatal("report body must be read-only")
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Markdown creation starts from an empty seed.
	seed := content.NewItem(reportType, now, cora.Report{})
	created, err := reportType.UpdateMarkdownHeader(seed, content.MarkdownFields{"report_id": "r-[1]"})
	testutil.NoErr(t, err)
	testutil.NoErr(t, registry.ValidateRecord(created))
	body, err := reportType.RenderMarkdown(created)
	testutil.NoErr(t, err)
	if body != "Cora report r-\\[1\\]\n" {
		t.Fatalf("unexpected body: %q", body)
	}

	stored := content.NewItem(reportType, now, cora.Report{ReportID: "r-1", URL: reportURL})
	body, err = reportType.RenderMarkdown(stored)
	testutil.NoErr(t, err)
	if body != "[Cora report r-1]("+reportURL+")\n" {
		t.Fatalf("unexpected body: %q", body)
	}
	presentation, err := reportType.Present(stored)
	testutil.NoErr(t, err)
	if card := presentation.Cards[0]; card.Fields[0].Value != "r-1" || card.Links[0].URL != reportURL {
		t.Fatalf("unexpected presentation: %#v", card)
	}

	fields, err := reportType.MarkdownHeader(stored)
	testutil.NoErr(t, err)
	if _, err := reportType.UpdateMarkdownHeader(stored, fields); err != nil {
		t.Fatalf("unchanged header rejected: %v", err)
	}
	_, err = reportType.UpdateMarkdownHeader(stored, content.MarkdownFields{"report_id": "r-1"})
	var validation *content.ValidationError
	if !errors.As(err, &validation) || len(validation.Fields) != 1 || validation.Fields[0].Path != "url" {
		t.Fatalf("expected locked url error, got %v", err)
	}

	for name, value := range map[string]cora.Report{
		"blank id":     {ReportID: " "},
		"relative url": {ReportID: "r-1", URL: "/reports/r-1"},
	} {
		if err := registry.ValidateRecord(content.NewItem(reportType, now, value)); err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
}

func newRegistry(t *testing.T) (*content.Registry, *cora.ReportType) {
	t.Helper()
	agentType := aiagent.NewMessageType()
	registry, err := content.NewRegistry([]content.ContentType{agentType}, cora.ContentTypes(agentType))
	testutil.NoErr(t, err)
	ct, err := registry.Lookup(cora.ReportTypeName)
	testutil.NoErr(t, err)
	return registry, ct.(*cora.ReportType)
}

// expand validates and expands a payload as the host does.
func expand(registry *content.Registry, existing content.Record, input map[string]any) (content.ExpansionResult, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	if err := registry.ValidateRecord(existing); err != nil {
		return content.ExpansionResult{}, err
	}
	if err := registry.ValidatePayload(cora.ReportTypeName, payload); err != nil {
		return content.ExpansionResult{}, err
	}
	result, err := existing.ContentType().(content.Expander).Expand(context.Background(), existing, payload)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	return result, registry.ValidateExpansion(existing, result)
}
