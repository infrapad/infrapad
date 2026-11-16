package pagerduty_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/pagerduty"
	"github.com/infrapad/infrapad/server/pkg/extensions/prometheus"
)

const incidentURL = "https://example.pagerduty.test/incidents/Q0EXAMPLE"

// TestSchemas checks the strict generated schemas, including the open
// alert details inside otherwise closed payloads.
func TestSchemas(t *testing.T) {
	types := pagerduty.ContentTypes(nil)
	incident, timeline := types[0].(*pagerduty.IncidentType), types[1]
	const created = `"created_at": "2026-09-08T13:03:43Z"`
	payload := func(alert string) string {
		return `{"incident": {"id": "Q0EXAMPLE", "url": "` + incidentURL + `", "title": "T", ` + created + `},
			"alerts": [` + alert + `], "notes": [{"id": "N1", ` + created + `, "author_name": "A", "content": "c"}],
			"log_entries": [{"id": "L1", ` + created + `, "type": "custom_log_entry", "summary": "s"}]}`
	}

	cases := []struct {
		name   string
		schema content.Schema
		data   string
		err    string // empty when valid
	}{
		{"reference", incident.ContentSchema(), `{"id": "Q1", "url": "` + incidentURL + `"}`, ""},
		{"enriched", incident.ContentSchema(), `{"id": "Q1", "url": "` + incidentURL + `", "text": "summary"}`, ""},
		{"blank text", incident.ContentSchema(), `{"id": "Q1", "url": "` + incidentURL + `", "text": " "}`, "text"},
		{"relative url", incident.ContentSchema(), `{"id": "Q1", "url": "/incidents/Q1"}`, "url"},
		{"blank id", incident.ContentSchema(), `{"id": "", "url": "` + incidentURL + `"}`, "id"},
		{"incident unknown field", incident.ContentSchema(), `{"id": "Q1", "url": "` + incidentURL + `", "title": "x"}`, "title: field not allowed"},
		{"timeline entry", timeline.ContentSchema(), `{"text": "x", "author": "A", "kind": "log_entry", "entry_type": "odd", "url": "` + incidentURL + `"}`, ""},
		{"timeline bad kind", timeline.ContentSchema(), `{"text": "x", "author": "A", "kind": "comment", "url": "` + incidentURL + `"}`, "kind"},
		{"payload", incident.PayloadSchema(), payload(`{"id": "A1", ` + created + `, "details": {"firing": "Labels:\n - a = b", "nested": [1, {"x": null}]}}`), ""},
		{"payload scalar details", incident.PayloadSchema(), payload(`{"id": "A1", ` + created + `, "details": "text"}`), ""},
		{"payload alert unknown field", incident.PayloadSchema(), payload(`{"id": "A1", ` + created + `, "body": {}}`), "alerts.0.body: field not allowed"},
		{"payload alert missing time", incident.PayloadSchema(), payload(`{"id": "A1"}`), "created_at"},
		{"payload invalid time", incident.PayloadSchema(), payload(`{"id": "A1", "created_at": "yesterday"}`), "created_at"},
	}
	ctx := cuecontext.New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			definition := ctx.CompileBytes(tc.schema.Source).LookupPath(cue.ParsePath(tc.schema.Definition))
			err := definition.Unify(ctx.CompileString(tc.data)).Validate(cue.Concrete(true))
			switch {
			case tc.err == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
				t.Fatalf("expected error containing %q, got %v", tc.err, err)
			}
		})
	}
}

// TestIncidentPresentation checks the reference and enriched incident
// rendering, presentation, and reference-edit restrictions.
func TestIncidentPresentation(t *testing.T) {
	registry, err := newRegistry()
	testutil.NoErr(t, err)
	incidentType, err := registry.Lookup(pagerduty.IncidentTypeName)
	testutil.NoErr(t, err)
	header := incidentType.(content.MarkdownHeader)
	if _, editable := incidentType.(content.MarkdownUpdater); editable {
		t.Fatal("incident body must be read-only")
	}

	// Before enrichment: a plain link; the reference is editable.
	reference := content.NewItem(incidentType, at("13:03"), pagerduty.Incident{ID: "Q0EXAMPLE", URL: incidentURL})
	body, err := incidentType.RenderMarkdown(reference)
	testutil.NoErr(t, err)
	if body != "[PagerDuty incident Q0EXAMPLE]("+incidentURL+")\n" {
		t.Fatalf("unexpected reference body: %q", body)
	}
	updated, err := header.UpdateMarkdownHeader(reference, content.MarkdownFields{"id": "Q1", "url": "https://other.example.test/incidents/Q1"})
	testutil.NoErr(t, err)
	testutil.NoErr(t, registry.ValidateRecord(updated))
	if v, _ := content.ItemView[pagerduty.Incident](updated); v.ContentValue.ID != "Q1" || !v.Timestamp().Equal(at("13:03")) {
		t.Fatalf("unexpected header update: %#v", v)
	}

	// After enrichment: the summary body, header excludes text, and only
	// unchanged references are accepted.
	summary := pagerduty.FormatIncidentSummary(pagerduty.SourceIncident{
		ID: "Q0EXAMPLE", URL: incidentURL, Title: "[#42] Node down", Description: "Node down\nfor real",
		Status: "resolved", CreatedAt: at("13:03"), ServiceName: "Cluster",
	}, []pagerduty.SourceAlert{{ID: "A1", Status: "resolved", Summary: "NodeDown\nnode-1", CreatedAt: at("13:02")}})
	if want := "- **Incident**: [Q0EXAMPLE](" + incidentURL + ")\n" +
		"- **Title**: [#42] Node down\n" +
		"- **Service**: Cluster\n" +
		"- **Status**: resolved\n" +
		"- **Urgency**: _unknown_\n" +
		"- **Created**: 2026-10-25 13:03:00 UTC\n" +
		"\n**Description**\n\nNode down\nfor real\n" +
		"\n**Alerts**\n\n" +
		"- **A1** (resolved, created 2026-10-25 13:02:00 UTC): NodeDown node-1\n"; summary != want {
		t.Fatalf("unexpected summary:\n%s", summary)
	}
	enriched := content.NewItem(incidentType, at("13:03"), pagerduty.Incident{ID: "Q0EXAMPLE", URL: incidentURL, Text: summary})
	enrichedRecord := testutil.RoundTripRecord(t, registry, enriched)
	body, err = incidentType.RenderMarkdown(enrichedRecord)
	testutil.NoErr(t, err)
	if body != summary {
		t.Fatalf("unexpected enriched body: %q", body)
	}
	fields, err := header.MarkdownHeader(enrichedRecord)
	testutil.NoErr(t, err)
	if len(fields) != 2 || fields["id"] != "Q0EXAMPLE" || fields["url"] != incidentURL {
		t.Fatalf("unexpected header: %#v", fields)
	}
	unchanged, err := header.UpdateMarkdownHeader(enrichedRecord, fields)
	testutil.NoErr(t, err)
	if v, _ := content.ItemView[pagerduty.Incident](unchanged); v.ContentValue.Text != summary {
		t.Fatal("round-trip lost the summary")
	}
	_, err = header.UpdateMarkdownHeader(enrichedRecord, content.MarkdownFields{"id": "Q1", "url": incidentURL})
	var validation *content.ValidationError
	if !errors.As(err, &validation) || len(validation.Fields) != 1 || validation.Fields[0].Path != "id" {
		t.Fatalf("expected locked reference error, got %v", err)
	}

	presentation, err := incidentType.(content.Presenter).Present(enrichedRecord)
	testutil.NoErr(t, err)
	card := presentation.Cards[0]
	if card.Markdown != summary || card.Links[0].URL != incidentURL || card.Links[0].Label != "Open in PagerDuty" {
		t.Fatalf("unexpected presentation: %#v", card)
	}
}

// TestTimelinePresentation checks grouped timeline rendering and
// attribution fallbacks.
func TestTimelinePresentation(t *testing.T) {
	registry, err := newRegistry()
	testutil.NoErr(t, err)
	ct, err := registry.Lookup(pagerduty.TimelineTypeName)
	testutil.NoErr(t, err)
	timelineType := ct.(content.GroupableContentType)

	members := []content.KeyedRecord{
		content.NewKeyedItem(ct, pagerduty.LogEntryKey("L1"), at("13:03"), pagerduty.LogTimelineEntry(
			pagerduty.SourceLogEntry{ID: "L1", Type: "trigger_log_entry", Summary: "Triggered through the API.", ActorID: "P1"}, incidentURL)),
		content.NewKeyedItem(ct, pagerduty.NoteKey("N1"), at("13:10"), pagerduty.NoteTimelineEntry(
			pagerduty.SourceNote{ID: "N1", AuthorName: "Alice", Content: "Restarting.\n\nDone."}, incidentURL)),
		content.NewKeyedItem(ct, pagerduty.LogEntryKey("L2"), at("13:20"), pagerduty.LogTimelineEntry(
			pagerduty.SourceLogEntry{ID: "L2", Summary: "Resolved."}, incidentURL)),
	}
	collection, err := timelineType.NewCollection(members)
	testutil.NoErr(t, err)
	reloaded := testutil.RoundTripRecord(t, registry, collection)
	body, err := ct.RenderMarkdown(reloaded)
	testutil.NoErr(t, err)
	want := "- **P1** · 2026-10-25 13:03:00 UTC · `trigger_log_entry` · [source](" + incidentURL + ")\n" +
		"  Triggered through the API.\n" +
		"- **Alice** · 2026-10-25 13:10:00 UTC · note · [source](" + incidentURL + ")\n" +
		"  Restarting.\n\n  Done.\n" +
		"- **Unknown author** · 2026-10-25 13:20:00 UTC · log entry · [source](" + incidentURL + ")\n" +
		"  Resolved.\n"
	if body != want {
		t.Fatalf("unexpected timeline:\n%s", body)
	}
	presentation, err := ct.(content.Presenter).Present(reloaded)
	testutil.NoErr(t, err)
	if card := presentation.Cards[0]; card.Markdown != want || len(card.Fields) != 0 {
		t.Fatalf("unexpected presentation: %#v", card)
	}
}

func at(clock string) time.Time {
	value, err := time.Parse(time.RFC3339, "2026-10-25T"+clock+":00Z")
	if err != nil {
		panic(err)
	}
	return value
}

// newRegistry registers the PagerDuty types with the shared
// Prometheus/AlertsMatcher instance, as in production.
func newRegistry() (*content.Registry, error) {
	matcherType := prometheus.NewAlertsMatcherType()
	return content.NewRegistry([]content.ContentType{matcherType}, pagerduty.ContentTypes(matcherType))
}
