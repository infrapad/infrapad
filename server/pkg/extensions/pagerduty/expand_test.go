package pagerduty_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/pagerduty"
	"github.com/infrapad/infrapad/server/pkg/extensions/prometheus"
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
				Details: json.RawMessage(`{"firing": "Labels:\n - alertname = NodeDown\n - node = n2\nAnnotations:\n - summary = down"}`)},
			{ID: "A1", Summary: "NodeDown node-1", Status: "resolved", CreatedAt: at("13:02"),
				// Mixed sections, duplicate label sets with different bounds,
				// scalar labels, and an earlier record without labels.
				Details: json.RawMessage(`{
					"firing": [
						{"labels": {"alertname": "NodeDown", "node": "n1", "port": 9100, "critical": true},
						 "startsAt": "2026-10-25T14:00:00+01:00", "endsAt": "0001-01-01T00:00:00Z"},
						{"labels": {"node": "n1", "alertname": "NodeDown", "critical": true, "port": 9100},
						 "startsAt": "2026-10-25T12:30:00Z", "endsAt": "2026-10-25T13:30:00Z"},
						{"startsAt": "2026-10-25T11:00:00Z"}
					],
					"resolved": "Labels:\n - alertname = NodeDown\n - node = n2"
				}`)},
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
// timeline attribution, matcher derivation, and order independence through the registry.
func TestIncidentExpansion(t *testing.T) {
	registry, err := newRegistry()
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

	// One timeline collection with stable keys, source times, attribution,
	// followed by the matcher derived from structured and text details.
	if len(result.Blocks) != 2 {
		t.Fatalf("expected timeline and matcher blocks, got %d", len(result.Blocks))
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

	// Matcher alternatives preserve complete label sets, deduplicate
	// occurrences, stringify scalar values, and exclude annotations.
	matcher := matcherRecord(t, registry, result)
	if matcher == nil {
		t.Fatal("expected a matcher child")
	}
	want := prometheus.AlertsMatcher{
		LabelsMatchers: []map[string][]string{
			{"alertname": {"NodeDown"}, "critical": {"true"}, "node": {"n1"}, "port": {"9100"}},
			{"alertname": {"NodeDown"}, "node": {"n2"}},
		},
		// Only label-bearing records affect bounds. Text and zero endsAt
		// keep the matcher open-ended even though another occurrence ended.
		Since: at("12:30"),
	}
	if !reflect.DeepEqual(matcher.ContentValue, want) || matcher.Key() != pagerduty.AlertsMatcherKey ||
		!matcher.Timestamp().Equal(want.Since) {
		t.Fatalf("unexpected matcher: %#v", matcher)
	}

	// Reordering alerts, timeline entries, and structured records yields
	// equivalent output.
	reordered := incidentPayload()
	slices.Reverse(reordered.Alerts)
	slices.Reverse(reordered.LogEntries)
	var details map[string]json.RawMessage
	testutil.NoErr(t, json.Unmarshal(reordered.Alerts[0].Details, &details))
	var records []json.RawMessage
	testutil.NoErr(t, json.Unmarshal(details["firing"], &records))
	slices.Reverse(records)
	details["firing"], err = json.Marshal(records)
	testutil.NoErr(t, err)
	reordered.Alerts[0].Details, err = json.Marshal(details)
	testutil.NoErr(t, err)
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

	if !content.EqualRecords(matcherRecord(t, registry, again), matcher) {
		t.Fatal("reordering changed the matcher")
	}

	// Until is set only when every represented record has a known end.
	bounded := incidentPayload()
	bounded.Alerts = []pagerduty.SourceAlert{{ID: "A1", CreatedAt: at("13:02"), Details: json.RawMessage(`{"resolved": [
		{"labels": {"a": "1"}, "startsAt": "2026-10-25T12:00:00Z", "endsAt": "2026-10-25T12:10:00Z"},
		{"labels": {"a": "2"}, "startsAt": "2026-10-25T12:05:00Z", "endsAt": "2026-10-25T12:20:00Z"}]}`)}}
	result, err = expand(registry, reference, bounded)
	testutil.NoErr(t, err)
	if m := matcherRecord(t, registry, result).ContentValue; !m.Since.Equal(at("12:00")) || !m.Until.Equal(at("12:20")) {
		t.Fatalf("unexpected bounds: %v - %v", m.Since, m.Until)
	}

	// Unsupported details leave the timeline but remove the matcher.
	unlabeled := incidentPayload()
	unlabeled.Alerts[0].Details = json.RawMessage(`{"severity": "critical"}`)
	unlabeled.Alerts[1].Details = json.RawMessage(`"free text"`)
	result, err = expand(registry, reference, unlabeled)
	testutil.NoErr(t, err)
	if len(result.Blocks) != 1 || matcherRecord(t, registry, result) != nil {
		t.Fatal("expected only the timeline without usable labels")
	}

	// Explicit empty collections enrich the primary without generated output.
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
	registry, err := newRegistry()
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
		{"section type", func(p *pagerduty.Payload) {
			p.Alerts[0].Details = json.RawMessage(`{"firing": 1, "resolved": {}}`)
		}, []string{"alerts.0.details.firing", "alerts.0.details.resolved"}},
		{"record type", func(p *pagerduty.Payload) {
			p.Alerts[0].Details = json.RawMessage(`{"firing": ["x"]}`)
		}, []string{"alerts.0.details.firing.0"}},
		{"missing start without labels", func(p *pagerduty.Payload) {
			p.Alerts[0].Details = json.RawMessage(`{"firing": [{"labels": {}}]}`)
		}, []string{"alerts.0.details.firing.0.startsAt"}},
		{"zero start", func(p *pagerduty.Payload) {
			p.Alerts[0].Details = json.RawMessage(`{"firing": [{"startsAt": "0001-01-01T00:00:00Z", "labels": {"a": "b"}}]}`)
		}, []string{"alerts.0.details.firing.0.startsAt"}},
		{"bad detail times collected with semantic errors", func(p *pagerduty.Payload) {
			p.Notes[0].CreatedAt = time.Time{}
			p.Alerts[0].Details = json.RawMessage(`{"resolved": [{"startsAt": "yesterday", "endsAt": 5}]}`)
		}, []string{"notes.0.created_at", "alerts.0.details.resolved.0.startsAt", "alerts.0.details.resolved.0.endsAt"}},
		{"label values", func(p *pagerduty.Payload) {
			p.Alerts[0].Details = json.RawMessage(`{"firing": [{"startsAt": "2026-10-25T12:00:00Z", "labels": {"a": null, "b": [], "c": {}}}]}`)
		}, []string{"alerts.0.details.firing.0.labels.a", "alerts.0.details.firing.0.labels.b", "alerts.0.details.firing.0.labels.c"}},
		{"label container", func(p *pagerduty.Payload) {
			p.Alerts[0].Details = json.RawMessage(`{"firing": [{"startsAt": "2026-10-25T12:00:00Z", "labels": []}]}`)
		}, []string{"alerts.0.details.firing.0.labels"}},
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

// matcherRecord returns the round-tripped matcher child of an expansion, or
// nil when there is none.
func matcherRecord(t *testing.T, registry *content.Registry, result content.ExpansionResult) *content.KeyedItem[prometheus.AlertsMatcher] {
	t.Helper()
	for _, block := range result.Blocks {
		if block.Record.ContentType().Name() != prometheus.AlertsMatcherTypeName {
			continue
		}
		view, err := content.KeyedItemView[prometheus.AlertsMatcher](testutil.RoundTripRecord(t, registry, block.Record))
		testutil.NoErr(t, err)
		return &view
	}
	return nil
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
