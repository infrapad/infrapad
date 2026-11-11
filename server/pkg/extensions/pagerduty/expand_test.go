package pagerduty_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/pagerduty"
)

// incidentPayload is a complete synthetic incident payload with alerts,
// notes, and log entries, deliberately not in chronological order.
func incidentPayload() pagerduty.Payload {
	return pagerduty.Payload{
		Incident: pagerduty.SourceIncident{
			ID: "Q0EXAMPLE", URL: incidentURL, Title: "Node down", Status: "resolved",
			Urgency: "high", CreatedAt: at("13:03"), ServiceName: "Cluster",
		},
		Alerts: []pagerduty.SourceAlert{
			{ID: "A2", Summary: "NodeDown node-2", Status: "resolved", CreatedAt: at("13:02"),
				Details: json.RawMessage(`{"firing": "Labels:\n - alertname = NodeDown"}`)},
			{ID: "A1", Summary: "NodeDown node-1", Status: "resolved", CreatedAt: at("13:02")},
		},
		Notes: []pagerduty.SourceNote{
			{ID: "N1", CreatedAt: at("13:10"), AuthorName: "Alice", Content: "Restarting."},
		},
		LogEntries: []pagerduty.SourceLogEntry{
			{ID: "L2", CreatedAt: at("13:20"), Type: "resolve_log_entry", Summary: "Resolved.", ActorID: "P1"},
			// IDs may overlap across collections.
			{ID: "N1", CreatedAt: at("13:03"), Type: "trigger_log_entry", Summary: "Triggered."},
		},
	}
}

// TestIncidentExpansion checks enrichment, source timestamps, stable keys,
// timeline attribution, and order independence through the registry.
func TestIncidentExpansion(t *testing.T) {
	registry, err := content.NewRegistry(pagerduty.ContentTypes())
	testutil.NoErr(t, err)
	incidentType, err := registry.Lookup(pagerduty.IncidentTypeName)
	testutil.NoErr(t, err)
	reference := content.NewKeyedItem(incidentType, "generated-incident", at("09:00"),
		pagerduty.Incident{ID: "Q0EXAMPLE", URL: incidentURL})

	result, err := expand(registry, reference, incidentPayload())
	testutil.NoErr(t, err)

	// The primary keeps its key and reference, gains the summary with
	// canonically ordered alerts, and moves to the incident creation time.
	primary, err := content.KeyedItemView[pagerduty.Incident](testutil.RoundTripRecord(t, registry, result.Record))
	testutil.NoErr(t, err)
	if primary.Key() != "generated-incident" || !primary.Timestamp().Equal(at("13:03")) ||
		primary.ContentValue.ID != "Q0EXAMPLE" || primary.ContentValue.URL != incidentURL {
		t.Fatalf("unexpected primary: %#v", primary)
	}
	text := primary.ContentValue.Text
	if a1, a2 := strings.Index(text, "**A1**"), strings.Index(text, "**A2**"); a1 < 0 || a2 < a1 ||
		strings.Contains(text, "alertname") {
		t.Fatalf("unexpected summary:\n%s", text)
	}
	if original, _ := content.ItemView[pagerduty.Incident](reference); original.ContentValue.Text != "" {
		t.Fatal("expansion mutated the input record")
	}

	// One timeline collection with stable keys, source times, attribution.
	if len(result.Blocks) != 1 {
		t.Fatalf("expected one timeline block, got %d", len(result.Blocks))
	}
	timeline := testutil.RoundTripRecord(t, registry, result.Blocks[0].Record)
	body, err := timeline.ContentType().RenderMarkdown(timeline)
	testutil.NoErr(t, err)
	members := content.OrderMembers(timeline)
	var keys []string
	for _, member := range members {
		keys = append(keys, member.Key())
	}
	if want := []string{pagerduty.LogEntryKey("N1"), pagerduty.NoteKey("N1"), pagerduty.LogEntryKey("L2")}; !slices.Equal(keys, want) {
		t.Fatalf("unexpected keys: %v", keys)
	}
	for _, want := range []string{
		"- **Alice** · 2026-10-25 13:10:00 UTC · note · [source](" + incidentURL + ")\n  Restarting.",
		"- **P1** · 2026-10-25 13:20:00 UTC · `resolve_log_entry`",
		"- **Unknown author** · 2026-10-25 13:03:00 UTC · `trigger_log_entry`",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("timeline missing %q:\n%s", want, body)
		}
	}

	// Reordering payload arrays alone yields equivalent output.
	reordered := incidentPayload()
	slices.Reverse(reordered.Alerts)
	slices.Reverse(reordered.LogEntries)
	again, err := expand(registry, reference, reordered)
	testutil.NoErr(t, err)
	if !content.EqualRecords(again.Record, result.Record) {
		t.Fatal("alert reordering changed the primary")
	}
	reorderedMembers := content.OrderMembers(again.Blocks[0].Record)
	for i := range members {
		if !content.EqualRecords(reorderedMembers[i], members[i]) {
			t.Fatalf("reordering changed member %d", i)
		}
	}

	// Explicit empty collections enrich the primary without timeline output.
	empty := incidentPayload()
	empty.Alerts, empty.Notes, empty.LogEntries = []pagerduty.SourceAlert{}, []pagerduty.SourceNote{}, []pagerduty.SourceLogEntry{}
	result, err = expand(registry, reference, empty)
	testutil.NoErr(t, err)
	if view, _ := content.ItemView[pagerduty.Incident](result.Record); !view.ContentValue.Enriched() || len(result.Blocks) != 0 {
		t.Fatalf("unexpected empty expansion: %#v", result)
	}
}

// TestIncidentExpansionValidation checks the semantic payload errors and
// their field paths.
func TestIncidentExpansionValidation(t *testing.T) {
	registry, err := content.NewRegistry(pagerduty.ContentTypes())
	testutil.NoErr(t, err)
	incidentType, err := registry.Lookup(pagerduty.IncidentTypeName)
	testutil.NoErr(t, err)
	reference := content.NewItem(incidentType, at("09:00"), pagerduty.Incident{ID: "Q0EXAMPLE", URL: incidentURL})

	cases := []struct {
		name   string
		mutate func(*pagerduty.Payload)
		paths  []string
	}{
		{"mismatched reference", func(p *pagerduty.Payload) {
			p.Incident.ID, p.Incident.URL = "Q1", "https://other.example.test/incidents/Q1"
		}, []string{"incident.id", "incident.url"}},
		{"missing collections", func(p *pagerduty.Payload) {
			p.Alerts, p.Notes, p.LogEntries = nil, nil, nil
		}, []string{"alerts", "notes", "log_entries"}},
		{"duplicate ids", func(p *pagerduty.Payload) {
			p.Alerts = append(p.Alerts, p.Alerts[0])
			p.Notes = append(p.Notes, p.Notes[0])
			p.LogEntries = append(p.LogEntries, p.LogEntries[0])
		}, []string{"alerts.2.id", "notes.1.id", "log_entries.2.id"}},
		{"zero times", func(p *pagerduty.Payload) {
			p.Incident.CreatedAt = time.Time{}
			p.Alerts[1].CreatedAt = p.Incident.CreatedAt
			p.Notes[0].CreatedAt = p.Incident.CreatedAt
			p.LogEntries[1].CreatedAt = p.Incident.CreatedAt
		}, []string{"incident.created_at", "alerts.1.created_at", "notes.0.created_at", "log_entries.1.created_at"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := incidentPayload()
			tc.mutate(&payload)
			_, err := expand(registry, reference, payload)
			var validation *content.ValidationError
			if !errors.As(err, &validation) || validation.Stage != content.StagePayload {
				t.Fatalf("expected a payload validation error, got %v", err)
			}
			var paths []string
			for _, field := range validation.Fields {
				paths = append(paths, field.Path)
			}
			if !slices.Equal(paths, tc.paths) {
				t.Fatalf("unexpected paths %v, want %v", paths, tc.paths)
			}
		})
	}
}

// expand runs the host sequence around a direct expander invocation:
// validate the existing record and the payload, invoke the expander, then
// check the result.
func expand(registry *content.Registry, existing content.Record, input pagerduty.Payload) (content.ExpansionResult, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	if err := registry.ValidateRecord(existing); err != nil {
		return content.ExpansionResult{}, err
	}
	name := existing.ContentType().Name()
	if err := registry.ValidatePayload(name, payload); err != nil {
		return content.ExpansionResult{}, err
	}
	ct, err := registry.Lookup(name)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	result, err := ct.(content.Expander).Expand(context.Background(), existing, payload)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	return result, registry.ValidateExpansion(existing, result)
}
