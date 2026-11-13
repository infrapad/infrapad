package prometheus_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/prometheus"
)

// TestAlertsMatcherScenario exercises Prometheus/AlertsMatcher through the
// registry: record round-trips over the generated schema and host codecs
// with normalized field names, open-ended intervals, empty rendering, and
// the absence of a Markdown updater capability.
func TestAlertsMatcherScenario(t *testing.T) {
	registry, err := content.NewRegistry(prometheus.ContentTypes())
	testutil.NoErr(t, err)
	matcher, err := registry.Lookup(prometheus.AlertsMatcherTypeName)
	testutil.NoErr(t, err)

	value := prometheus.AlertsMatcher{
		// Alternatives across entries, AND across label names, OR across
		// the values of one label.
		LabelsMatchers: []map[string][]string{
			{"alertname": {"NodeDown", "NodeNotReady"}, "severity": {"critical"}},
			{"cluster": {"production"}},
		},
		Since:  at("09:00"),
		Until:  at("12:00"),
		Extras: map[string]any{"dashboard": "https://grafana.example/x"},
	}
	reloaded := testutil.RoundTripRecord(t, registry, content.NewItem(matcher, at("10:00"), value))
	view, err := content.ItemView[prometheus.AlertsMatcher](reloaded)
	testutil.NoErr(t, err)
	if !view.ContentValue.Since.Equal(at("09:00")) || !view.ContentValue.Until.Equal(at("12:00")) ||
		len(view.ContentValue.LabelsMatchers) != 2 || view.ContentValue.Extras["dashboard"] != "https://grafana.example/x" {
		t.Fatalf("unexpected content: %#v", view.ContentValue)
	}

	// A zero Until means an open-ended interval and survives the round trip.
	open := prometheus.AlertsMatcher{LabelsMatchers: value.LabelsMatchers, Since: value.Since}
	view, err = content.ItemView[prometheus.AlertsMatcher](testutil.RoundTripRecord(t, registry, content.NewItem(matcher, at("10:00"), open)))
	testutil.NoErr(t, err)
	if !view.ContentValue.Until.IsZero() {
		t.Fatalf("expected an open-ended interval, got %v", view.ContentValue.Until)
	}

	// Omitting until also means an open-ended interval.
	decoded, err := registry.DecodeRecord(prometheus.AlertsMatcherTypeName, item(`{
		"labels_matchers": [{"severity": ["critical"]}],
		"since": "2026-10-25T09:00:00Z"
	}`))
	testutil.NoErr(t, err)
	view, err = content.ItemView[prometheus.AlertsMatcher](decoded)
	testutil.NoErr(t, err)
	if !view.ContentValue.Until.IsZero() || !view.ContentValue.Since.Equal(at("09:00")) {
		t.Fatalf("unexpected decoded content: %#v", view.ContentValue)
	}

	// The matcher renders no Markdown body of its own.
	body, err := matcher.RenderMarkdown(reloaded)
	testutil.NoErr(t, err)
	if body != "" {
		t.Fatalf("expected an empty body, got %q", body)
	}

	// It is not editable through Markdown body updates.
	if _, ok := matcher.(content.MarkdownUpdater); ok {
		t.Fatal("Prometheus/AlertsMatcher must not implement MarkdownUpdater")
	}
}

// TestAlertsMatcherValidation checks the schema boundaries of
// Prometheus/AlertsMatcher: normalized snake_case fields only, no legacy
// uppercase aliases, no unknown fields outside extras.
func TestAlertsMatcherValidation(t *testing.T) {
	registry, err := content.NewRegistry(prometheus.ContentTypes())
	testutil.NoErr(t, err)

	cases := []struct {
		name    string
		content string
		field   string
	}{
		{"legacy uppercase fields", `{
			"LabelsMatchers": [{"severity": ["critical"]}],
			"Since": "2026-10-25T09:00:00Z",
			"Until": "2026-10-25T12:00:00Z"
		}`, "LabelsMatchers"},
		{"legacy since alias", `{"labels_matchers": [], "since": "2026-10-25T09:00:00Z", "Since": "2026-10-25T09:00:00Z"}`, "Since"},
		{"unknown field", `{"labels_matchers": [], "since": "2026-10-25T09:00:00Z", "window": "1h"}`, "window"},
		{"missing since", `{"labels_matchers": []}`, "since"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := registry.DecodeRecord(prometheus.AlertsMatcherTypeName, item(tc.content))
			expectField(t, err, tc.field)
		})
	}
}

// item wraps inner content in a host item record envelope.
func item(content string) json.RawMessage {
	return json.RawMessage(`{"timestamp": "2026-10-25T10:00:00Z", "content": ` + content + `}`)
}

// expectField asserts a content-stage validation error reporting path.
func expectField(t *testing.T, err error, path string) {
	t.Helper()
	var validation *content.ValidationError
	if !errors.As(err, &validation) || validation.Stage != content.StageContent {
		t.Fatalf("expected a content validation error, got %v", err)
	}
	for _, field := range validation.Fields {
		if field.Path == path {
			return
		}
	}
	t.Fatalf("expected field %q in %v", path, validation)
}

func at(clock string) time.Time {
	value, err := time.Parse(time.RFC3339, "2026-10-25T"+clock+":00Z")
	if err != nil {
		panic(err)
	}
	return value
}

// TestAlertsMatcherQueryResultsLifecycle covers payload ingestion of compact
// query results: complete replacement and clearing, preserved record
// metadata, a compact JSON round trip, and Markdown header preservation
// versus invalidation on matcher changes.
func TestAlertsMatcherQueryResultsLifecycle(t *testing.T) {
	registry, err := content.NewRegistry(prometheus.ContentTypes())
	testutil.NoErr(t, err)
	matcherType, err := registry.Lookup(prometheus.AlertsMatcherTypeName)
	testutil.NoErr(t, err)
	existing := content.NewKeyedItem(matcherType, "matcher", at("10:00"), prometheus.AlertsMatcher{
		LabelsMatchers: []map[string][]string{{"alertname": {"HighLatency"}}},
		Since:          at("10:00"),
		Extras:         map[string]any{"team": "sre"},
	})

	result := expandMatcher(t, registry, existing, `{"query_results": {
		"start": "2026-10-25T09:58:00Z", "end": "2026-10-25T10:12:00Z", "step_seconds": 15,
		"series": [{"labels": {"alertname": "HighLatency", "alertstate": "firing"},
			"samples": [[1792922400.5, 1.25], [1792922415, 0]]}]
	}}`)
	if len(result.Blocks) != 0 {
		t.Fatalf("expected no generated children, got %d", len(result.Blocks))
	}
	keyed, ok := result.Record.(content.KeyedRecord)
	if !ok || keyed.Key() != "matcher" || !keyed.Timestamp().Equal(at("10:00")) {
		t.Fatalf("expected preserved key and timestamp, got %#v", result.Record)
	}
	if view := matcherView(t, existing); view.QueryResults != nil {
		t.Fatal("expansion mutated the existing record")
	}

	// Compact numeric pairs survive the stored record round trip.
	reloaded := testutil.RoundTripRecord(t, registry, result.Record)
	stored := matcherView(t, reloaded)
	q := stored.QueryResults
	if q == nil || q.StepSeconds != 15 || len(q.Series) != 1 || q.Series[0].Labels["alertstate"] != "firing" ||
		q.Series[0].Samples[0] != (prometheus.Sample{Timestamp: 1792922400.5, Value: 1.25}) || stored.Extras["team"] != "sre" {
		t.Fatalf("unexpected stored content: %#v", stored)
	}
	encoded, err := content.EncodeRecord(reloaded)
	testutil.NoErr(t, err)
	if !strings.Contains(string(encoded), `"samples":[[1792922400.5,1.25],[1792922415,0]]`) {
		t.Fatalf("expected compact sample pairs, got %s", encoded)
	}

	// Query results stay out of the header; an unchanged header or an
	// extras-only change preserves them, a matcher change clears them.
	header := matcherType.(content.MarkdownHeader)
	fields, err := header.MarkdownHeader(reloaded)
	testutil.NoErr(t, err)
	if _, exposed := fields["query_results"]; exposed {
		t.Fatalf("query results leaked into the header: %v", fields)
	}
	updated, err := header.UpdateMarkdownHeader(reloaded, fields)
	testutil.NoErr(t, err)
	if matcherView(t, updated).QueryResults == nil {
		t.Fatal("unchanged header dropped the query results")
	}
	fields["extras"] = map[string]any{"team": "platform"}
	updated, err = header.UpdateMarkdownHeader(reloaded, fields)
	testutil.NoErr(t, err)
	if matcherView(t, updated).QueryResults == nil {
		t.Fatal("extras-only change dropped the query results")
	}
	fields["until"] = "2026-10-25T11:00:00Z"
	updated, err = header.UpdateMarkdownHeader(reloaded, fields)
	testutil.NoErr(t, err)
	if matcherView(t, updated).QueryResults != nil {
		t.Fatal("incident bound change kept stale query results")
	}

	// An explicit empty series is a successful empty result; {} clears.
	empty := matcherView(t, expandMatcher(t, registry, reloaded, `{"query_results": {
		"start": "2026-10-25T09:58:00Z", "end": "2026-10-25T10:12:00Z", "step_seconds": 0.5, "series": []
	}}`).Record)
	if empty.QueryResults == nil || empty.QueryResults.Series == nil || len(empty.QueryResults.Series) != 0 {
		t.Fatalf("expected explicit empty series, got %#v", empty.QueryResults)
	}
	cleared := matcherView(t, expandMatcher(t, registry, reloaded, `{}`).Record)
	if cleared.QueryResults != nil || len(cleared.LabelsMatchers) != 1 || cleared.Extras["team"] != "sre" {
		t.Fatalf("expected a metadata-only matcher, got %#v", cleared)
	}
}

// TestAlertsMatcherPayloadValidation checks schema and semantic rejection
// of malformed query results with field-specific paths.
func TestAlertsMatcherPayloadValidation(t *testing.T) {
	registry, err := content.NewRegistry(prometheus.ContentTypes())
	testutil.NoErr(t, err)
	matcherType, err := registry.Lookup(prometheus.AlertsMatcherTypeName)
	testutil.NoErr(t, err)
	existing := content.NewItem(matcherType, at("10:00"), prometheus.AlertsMatcher{Since: at("10:00")})

	wrap := func(series string) string {
		return `{"query_results": {"start": "2026-10-25T10:00:00Z", "end": "2026-10-25T10:10:00Z", "step_seconds": 15, "series": ` + series + `}}`
	}
	cases := []struct {
		name, payload, field string
	}{
		{"unknown field", `{"query_results": null, "labels_matchers": []}`, "labels_matchers"},
		{"reversed bounds", `{"query_results": {"start": "2026-10-25T10:10:00Z", "end": "2026-10-25T10:00:00Z", "step_seconds": 15, "series": []}}`, "query_results.end"},
		{"zero step", `{"query_results": {"start": "2026-10-25T10:00:00Z", "end": "2026-10-25T10:10:00Z", "step_seconds": 0, "series": []}}`, "query_results.step_seconds"},
		{"missing series", `{"query_results": {"start": "2026-10-25T10:00:00Z", "end": "2026-10-25T10:10:00Z", "step_seconds": 15}}`, "query_results.series"},
		{"null series", wrap(`null`), "query_results.series"},
		{"missing samples", wrap(`[{"labels": {}}]`), "query_results.series.0.samples"},
		{"missing labels", wrap(`[{"samples": []}]`), "query_results.series.0.labels"},
		{"string value", wrap(`[{"labels": {}, "samples": [[1792922400, "NaN"]]}]`), "query_results.series.0.samples.0.1"},
		{"triple", wrap(`[{"labels": {}, "samples": [[1792922400, 1, 2]]}]`), "query_results.series.0.samples.0"},
		{"out of bounds", wrap(`[{"labels": {}, "samples": [[1761386399.9, 1]]}]`), "query_results.series.0.samples.0.0"},
		{"not increasing", wrap(`[{"labels": {}, "samples": [[1792922460, 1], [1792922460, 0]]}]`), "query_results.series.0.samples.1.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runExpansion(registry, existing, tc.payload)
			var validation *content.ValidationError
			if !errors.As(err, &validation) || validation.Stage != content.StagePayload {
				t.Fatalf("expected a payload validation error, got %v", err)
			}
			for _, field := range validation.Fields {
				if field.Path == tc.field {
					return
				}
			}
			t.Fatalf("expected field %q in %v", tc.field, validation)
		})
	}
}

// expandMatcher runs a successful host-style expansion.
func expandMatcher(t *testing.T, registry *content.Registry, existing content.Record, payload string) content.ExpansionResult {
	t.Helper()
	result, err := runExpansion(registry, existing, payload)
	testutil.NoErr(t, err)
	return result
}

// runExpansion runs the host sequence: validate the payload, expand, and
// validate the result.
func runExpansion(registry *content.Registry, existing content.Record, payload string) (content.ExpansionResult, error) {
	if err := registry.ValidatePayload(prometheus.AlertsMatcherTypeName, json.RawMessage(payload)); err != nil {
		return content.ExpansionResult{}, err
	}
	result, err := existing.ContentType().(content.Expander).Expand(context.Background(), existing, json.RawMessage(payload))
	if err != nil {
		return content.ExpansionResult{}, err
	}
	return result, registry.ValidateExpansion(existing, result)
}

func matcherView(t *testing.T, record content.Record) prometheus.AlertsMatcher {
	t.Helper()
	view, err := content.ItemView[prometheus.AlertsMatcher](record)
	testutil.NoErr(t, err)
	return view.ContentValue
}
