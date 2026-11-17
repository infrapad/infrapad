package prometheus_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/prometheus"
)

const chartQuery = `"query": {"expression": "rate(x[5m])", "source_url": "https://prom.example.com",
	"start": "2026-10-25T10:00:00Z", "end": "2026-10-25T10:10:00Z", "step_seconds": 60}`

// TestTimeSeriesChartSnapshots checks host-style ingestion (schema plus
// semantic validation) of absent, empty, and populated snapshots and their
// stored-record round trip.
func TestTimeSeriesChartSnapshots(t *testing.T) {
	registry, err := content.NewRegistry(prometheus.ContentTypes())
	testutil.NoErr(t, err)

	absent := chartView(t, ingestChart(t, registry, `{`+chartQuery+`, "title": "Rate", "unit": "req/s"}`))
	if absent.QueryResults != nil || absent.Title != "Rate" || absent.Unit != "req/s" || absent.Query.StepSeconds != 60 {
		t.Fatalf("unexpected chart without snapshot: %#v", absent)
	}
	cleared := chartView(t, ingestChart(t, registry, `{`+chartQuery+`, "query_results": null}`))
	if cleared.QueryResults != nil {
		t.Fatalf("expected a null snapshot to mean none, got %#v", cleared.QueryResults)
	}
	empty := chartView(t, ingestChart(t, registry, `{`+chartQuery+`, "query_results": {"series": []}}`))
	if empty.QueryResults == nil || empty.QueryResults.Series == nil || len(empty.QueryResults.Series) != 0 {
		t.Fatalf("expected an explicit empty snapshot, got %#v", empty.QueryResults)
	}
	record := ingestChart(t, registry, `{`+chartQuery+`, "query_results": {"series": [
		{"labels": {"pod": "a"}, "samples": [[1792922400, 1.5], [1792922460, null], [1792922999.5, 0]]}]}}`)
	stored := chartView(t, testutil.RoundTripRecord(t, registry, record))
	want := []prometheus.Sample{
		{Timestamp: 1792922400, Value: prometheus.Value(1.5)},
		{Timestamp: 1792922460},
		{Timestamp: 1792922999.5, Value: prometheus.Value(0)},
	}
	if stored.QueryResults == nil || len(stored.QueryResults.Series) != 1 ||
		!reflect.DeepEqual(stored.QueryResults.Series[0].Samples, want) {
		t.Fatalf("unexpected stored snapshot: %#v", stored.QueryResults)
	}
}

// TestTimeSeriesChartValidation checks schema and semantic rejection with
// field paths following the chart JSON layout.
func TestTimeSeriesChartValidation(t *testing.T) {
	registry, err := content.NewRegistry(prometheus.ContentTypes())
	testutil.NoErr(t, err)
	query := func(fields string) string {
		return `{"query": {` + fields + `}}`
	}
	const bounds = `"start": "2026-10-25T10:00:00Z", "end": "2026-10-25T10:10:00Z"`
	withSeries := func(series string) string {
		return `{` + chartQuery + `, "query_results": {"series": ` + series + `}}`
	}
	cases := []struct{ name, content, field string }{
		{"missing query", `{"title": "x"}`, "query.expression"},
		{"blank expression", query(`"expression": "  ", ` + bounds + `, "step_seconds": 60`), "query.expression"},
		{"relative source url", query(`"expression": "up", "source_url": "/prom", ` + bounds + `, "step_seconds": 60`), "query.source_url"},
		{"ftp source url", query(`"expression": "up", "source_url": "ftp://prom", ` + bounds + `, "step_seconds": 60`), "query.source_url"},
		{"reversed bounds", query(`"expression": "up", "start": "2026-10-25T10:10:00Z", "end": "2026-10-25T10:00:00Z", "step_seconds": 60`), "query.end"},
		{"zero step", query(`"expression": "up", ` + bounds + `, "step_seconds": 0`), "query.step_seconds"},
		{"bounds in results", `{` + chartQuery + `, "query_results": {"series": [], "step_seconds": 60}}`, "query_results.step_seconds"},
		{"missing series", `{` + chartQuery + `, "query_results": {}}`, "query_results.series"},
		{"null series", withSeries(`null`), "query_results.series"},
		{"out of bounds", withSeries(`[{"labels": {}, "samples": [[1792922399, 1]]}]`), "query_results.series.0.samples.0.0"},
		{"not increasing", withSeries(`[{"labels": {}, "samples": [[1792922460, 1], [1792922460, null]]}]`), "query_results.series.0.samples.1.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateChart(registry, tc.content)
			expectField(t, err, tc.field)
		})
	}
}

// TestTimeSeriesChartMarkdownAndRevisions checks the data-excluding
// Markdown header (preservation versus clearing of the snapshot) and the
// metadata-versus-results revision decisions.
func TestTimeSeriesChartMarkdownAndRevisions(t *testing.T) {
	registry, err := content.NewRegistry(prometheus.ContentTypes())
	testutil.NoErr(t, err)
	record := ingestChart(t, registry, `{`+chartQuery+`, "title": "Rate", "query_results": {"series": [
		{"labels": {}, "samples": [[1792922400, 1]]}]}}`)
	chartType := record.ContentType()
	header := chartType.(content.MarkdownHeader)
	policy := chartType.(content.RevisionPolicy)
	if _, ok := chartType.(content.MarkdownUpdater); ok {
		t.Fatal("Prometheus/TimeSeriesChart must not support Markdown body edits")
	}
	if body, err := chartType.RenderMarkdown(record); err != nil || body != "" {
		t.Fatalf("expected an empty body, got %q (%v)", body, err)
	}

	fields, err := header.MarkdownHeader(record)
	testutil.NoErr(t, err)
	if _, leaked := fields["query_results"]; leaked || fields["title"] != "Rate" || fields["query"] == nil {
		t.Fatalf("unexpected header: %v", fields)
	}
	edit := func(change func(fields, query map[string]any)) prometheus.TimeSeriesChart {
		t.Helper()
		copied := cloneFields(t, fields)
		change(copied, copied["query"].(map[string]any))
		updated, err := header.UpdateMarkdownHeader(record, copied)
		testutil.NoErr(t, err)
		testutil.NoErr(t, registry.ValidateRecord(updated))
		return chartView(t, updated)
	}
	if edit(func(map[string]any, map[string]any) {}).QueryResults == nil {
		t.Fatal("unchanged header dropped the snapshot")
	}
	if c := edit(func(f, _ map[string]any) { f["title"] = "Renamed"; f["unit"] = "req/s" }); c.QueryResults == nil || c.Unit != "req/s" {
		t.Fatalf("title/unit edit dropped the snapshot: %#v", c)
	}
	if edit(func(_, q map[string]any) { q["start"] = "2026-10-25T12:00:00+02:00" }).QueryResults == nil {
		t.Fatal("equivalent start instant dropped the snapshot")
	}
	if c := edit(func(_, q map[string]any) { q["step_seconds"] = 30 }); c.QueryResults != nil || c.Query.StepSeconds != 30 {
		t.Fatalf("query edit kept the stale snapshot: %#v", c)
	}

	decide := func(next string) bool {
		t.Helper()
		create, err := policy.ShouldCreateRevision(record, ingestChart(t, registry, next))
		testutil.NoErr(t, err)
		return create
	}
	cases := []struct {
		name, next string
		revision   bool
	}{
		{"results refresh", `{` + chartQuery + `, "title": "Rate", "query_results": {"series": []}}`, false},
		{"results cleared", `{` + chartQuery + `, "title": "Rate"}`, false},
		{"title change", `{` + chartQuery + `, "title": "Other"}`, true},
		{"unit added", `{` + chartQuery + `, "title": "Rate", "unit": "s"}`, true},
		{"expression change", `{"query": {"expression": "up", "source_url": "https://prom.example.com",
			"start": "2026-10-25T10:00:00Z", "end": "2026-10-25T10:10:00Z", "step_seconds": 60}, "title": "Rate"}`, true},
		{"equivalent bounds", `{"query": {"expression": "rate(x[5m])", "source_url": "https://prom.example.com",
			"start": "2026-10-25T12:00:00+02:00", "end": "2026-10-25T10:10:00Z", "step_seconds": 60}, "title": "Rate"}`, false},
	}
	for _, tc := range cases {
		if got := decide(tc.next); got != tc.revision {
			t.Errorf("%s: expected revision=%v, got %v", tc.name, tc.revision, got)
		}
	}
}

// validateChart mirrors host JSON ingestion: schema-checked decoding, then
// record validation including the type's semantic checks.
func validateChart(registry *content.Registry, inner string) (content.Record, error) {
	value, err := registry.DecodeContent(prometheus.TimeSeriesChartTypeName, json.RawMessage(inner))
	if err != nil {
		return nil, err
	}
	ct, err := registry.Lookup(prometheus.TimeSeriesChartTypeName)
	if err != nil {
		return nil, err
	}
	record := content.NewItem(ct, at("10:00"), value)
	return record, registry.ValidateRecord(record)
}

func ingestChart(t *testing.T, registry *content.Registry, inner string) content.Record {
	t.Helper()
	record, err := validateChart(registry, inner)
	testutil.NoErr(t, err)
	return record
}

func chartView(t *testing.T, record content.Record) prometheus.TimeSeriesChart {
	t.Helper()
	view, err := content.ItemView[prometheus.TimeSeriesChart](record)
	testutil.NoErr(t, err)
	return view.ContentValue
}

func cloneFields(t *testing.T, fields content.MarkdownFields) content.MarkdownFields {
	t.Helper()
	data, err := json.Marshal(fields)
	testutil.NoErr(t, err)
	var copied content.MarkdownFields
	testutil.NoErr(t, json.Unmarshal(data, &copied))
	return copied
}
