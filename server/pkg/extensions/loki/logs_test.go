package loki_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/loki"
)

const logsQuery = `"query": {"expression": "{app=\"etcd\"}", "start": "2026-11-13T10:57:00Z",
	"end": "2026-11-13T11:03:00Z", "limit": 50, "datasource_uid": "P19E", "datasource_name": "production-logs"}`

// TestLogsSnapshots checks host-style ingestion of absent, empty, and
// populated snapshots and a lossless stored-record round trip.
func TestLogsSnapshots(t *testing.T) {
	registry, err := content.NewRegistry(loki.ContentTypes())
	testutil.NoErr(t, err)

	absent := logsView(t, ingest(t, registry, `{`+logsQuery+`, "title": "etcd"}`))
	if absent.QueryResults != nil || absent.Title != "etcd" || absent.Query.Limit != 50 || absent.Query.DatasourceUID != "P19E" {
		t.Fatalf("unexpected logs without snapshot: %#v", absent)
	}
	if cleared := logsView(t, ingest(t, registry, `{`+logsQuery+`, "query_results": null}`)); cleared.QueryResults != nil {
		t.Fatalf("expected a null snapshot to mean none, got %#v", cleared.QueryResults)
	}
	empty := logsView(t, ingest(t, registry, `{`+logsQuery+`, "query_results": {"streams": []}}`))
	if empty.QueryResults == nil || empty.QueryResults.Streams == nil || len(empty.QueryResults.Streams) != 0 {
		t.Fatalf("expected an explicit empty snapshot, got %#v", empty.QueryResults)
	}

	record := ingest(t, registry, `{`+logsQuery+`, "query_results": {"streams": [
		{"labels": {"pod": "etcd-1", "node": "n1"}, "entries": [
			{"timestamp": "1794567686759710823", "line": "{\"msg\": \"b\"}", "structured_metadata": {"trace_id": "abc"}},
			{"timestamp": "1794567420000000000", "line": ""},
			{"timestamp": "1794567420000000000", "line": ""}]},
		{"labels": {}, "entries": []}]}}`)
	stored := logsView(t, testutil.RoundTripRecord(t, registry, record))
	want := []loki.Stream{
		{Labels: map[string]string{"pod": "etcd-1", "node": "n1"}, Entries: []loki.Entry{
			{Timestamp: "1794567686759710823", Line: `{"msg": "b"}`, StructuredMetadata: map[string]string{"trace_id": "abc"}},
			{Timestamp: "1794567420000000000"},
			{Timestamp: "1794567420000000000"},
		}},
		{Labels: map[string]string{}, Entries: []loki.Entry{}},
	}
	if stored.QueryResults == nil || !reflect.DeepEqual(stored.QueryResults.Streams, want) {
		t.Fatalf("unexpected stored snapshot: %#v", stored.QueryResults)
	}
}

// TestLogsValidation checks schema and semantic rejection with field paths
// following the content JSON layout.
func TestLogsValidation(t *testing.T) {
	registry, err := content.NewRegistry(loki.ContentTypes())
	testutil.NoErr(t, err)
	query := func(fields string) string { return `{"query": {` + fields + `}}` }
	const bounds = `"start": "2026-11-13T10:57:00Z", "end": "2026-11-13T11:03:00Z"`
	withStreams := func(streams string) string {
		return `{` + logsQuery + `, "query_results": {"streams": ` + streams + `}}`
	}
	entry := func(ts string) string {
		return withStreams(`[{"labels": {}, "entries": [{"timestamp": ` + ts + `, "line": "x"}]}]`)
	}
	cases := []struct{ name, content, field string }{
		{"missing query", `{"title": "x"}`, "query.expression"},
		{"blank expression", query(`"expression": " ", ` + bounds + `, "limit": 10`), "query.expression"},
		{"reversed bounds", query(`"expression": "{a=\"b\"}", "start": "2026-11-13T11:03:00Z", "end": "2026-11-13T10:57:00Z", "limit": 10`), "query.end"},
		{"zero limit", query(`"expression": "{a=\"b\"}", ` + bounds + `, "limit": 0`), "query.limit"},
		{"fractional limit", query(`"expression": "{a=\"b\"}", ` + bounds + `, "limit": 1.5`), "query.limit"},
		{"relative source url", query(`"expression": "{a=\"b\"}", ` + bounds + `, "limit": 10, "source_url": "/loki"`), "query.source_url"},
		{"direction", query(`"expression": "{a=\"b\"}", ` + bounds + `, "limit": 10, "direction": "forward"`), "query.direction"},
		{"null streams", withStreams(`null`), "query_results.streams"},
		{"missing entries", withStreams(`[{"labels": {}}]`), "query_results.streams.0.entries"},
		{"null labels", withStreams(`[{"labels": null, "entries": []}]`), "query_results.streams.0.labels"},
		{"numeric label", withStreams(`[{"labels": {"a": 1}, "entries": []}]`), "query_results.streams.0.labels.a"},
		{"numeric timestamp", entry(`1794567686759710823`), "query_results.streams.0.entries.0.timestamp"},
		{"fractional timestamp", entry(`"1794567686.5"`), "query_results.streams.0.entries.0.timestamp"},
		{"int64 overflow", entry(`"99999999999999999999"`), "query_results.streams.0.entries.0.timestamp"},
		{"before start", entry(`"1794567419999999999"`), "query_results.streams.0.entries.0.timestamp"},
		{"after end", entry(`"1794567780000000001"`), "query_results.streams.0.entries.0.timestamp"},
		{"null line", withStreams(`[{"labels": {}, "entries": [{"timestamp": "1794567420000000000", "line": null}]}]`),
			"query_results.streams.0.entries.0.line"},
		{"null metadata", withStreams(`[{"labels": {}, "entries": [{"timestamp": "1794567420000000000", "line": "", "structured_metadata": null}]}]`),
			"query_results.streams.0.entries.0.structured_metadata"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validate(registry, tc.content)
			expectField(t, err, tc.field)
		})
	}
	// Inclusive bounds.
	ingest(t, registry, withStreams(`[{"labels": {}, "entries": [
		{"timestamp": "1794567420000000000", "line": "start"}, {"timestamp": "1794567780000000000", "line": "end"}]}]`))
}

// TestLogsMarkdownAndRevisions checks the results-excluding Markdown header
// (preservation versus clearing of the snapshot) and the metadata-versus-
// results revision decisions.
func TestLogsMarkdownAndRevisions(t *testing.T) {
	registry, err := content.NewRegistry(loki.ContentTypes())
	testutil.NoErr(t, err)
	record := ingest(t, registry, `{`+logsQuery+`, "title": "etcd", "query_results": {"streams": [
		{"labels": {}, "entries": [{"timestamp": "1794567686759710823", "line": "x"}]}]}}`)
	logsType := record.ContentType()
	header := logsType.(content.MarkdownHeader)
	policy := logsType.(content.RevisionPolicy)
	if _, ok := logsType.(content.MarkdownUpdater); ok {
		t.Fatal("Loki/Logs must not support Markdown body edits")
	}
	if body, err := logsType.RenderMarkdown(record); err != nil || body != "" {
		t.Fatalf("expected an empty body, got %q (%v)", body, err)
	}

	fields, err := header.MarkdownHeader(record)
	testutil.NoErr(t, err)
	if _, leaked := fields["query_results"]; leaked || fields["title"] != "etcd" || fields["query"] == nil || len(fields) != 2 {
		t.Fatalf("unexpected header: %v", fields)
	}
	edit := func(change func(fields, query map[string]any)) loki.Logs {
		t.Helper()
		copied := cloneFields(t, fields)
		change(copied, copied["query"].(map[string]any))
		updated, err := header.UpdateMarkdownHeader(record, copied)
		testutil.NoErr(t, err)
		testutil.NoErr(t, registry.ValidateRecord(updated))
		return logsView(t, updated)
	}
	if edit(func(map[string]any, map[string]any) {}).QueryResults == nil {
		t.Fatal("unchanged header dropped the snapshot")
	}
	if l := edit(func(f, _ map[string]any) { f["title"] = "Renamed" }); l.QueryResults == nil || l.Title != "Renamed" {
		t.Fatalf("title edit dropped the snapshot: %#v", l)
	}
	if edit(func(_, q map[string]any) { q["start"] = "2026-11-13T11:57:00+01:00" }).QueryResults == nil {
		t.Fatal("equivalent start instant dropped the snapshot")
	}
	if l := edit(func(_, q map[string]any) { q["datasource_name"] = "other" }); l.QueryResults != nil {
		t.Fatalf("provenance edit kept the stale snapshot: %#v", l)
	}
	if l := edit(func(_, q map[string]any) { q["limit"] = 100 }); l.QueryResults != nil || l.Query.Limit != 100 {
		t.Fatalf("limit edit kept the stale snapshot: %#v", l)
	}

	const otherQuery = `"query": {"expression": "{app=\"etcd\"}", "start": "2026-11-13T11:57:00+01:00",
		"end": "2026-11-13T11:03:00Z", "limit": 50, "datasource_uid": "P19E", "datasource_name": "production-logs"`
	cases := []struct {
		name, next string
		revision   bool
	}{
		{"results refresh", `{` + logsQuery + `, "title": "etcd", "query_results": {"streams": []}}`, false},
		{"results cleared", `{` + logsQuery + `, "title": "etcd"}`, false},
		{"equivalent bounds", `{` + otherQuery + `}, "title": "etcd"}`, false},
		{"title change", `{` + logsQuery + `, "title": "other"}`, true},
		{"provenance change", `{"query": {"expression": "{app=\"etcd\"}", "start": "2026-11-13T10:57:00Z",
			"end": "2026-11-13T11:03:00Z", "limit": 50, "datasource_uid": "P19E"}, "title": "etcd"}`, true},
	}
	for _, tc := range cases {
		create, err := policy.ShouldCreateRevision(record, ingest(t, registry, tc.next))
		testutil.NoErr(t, err)
		if create != tc.revision {
			t.Errorf("%s: expected revision=%v, got %v", tc.name, tc.revision, create)
		}
	}
}

// validate mirrors host JSON ingestion: schema-checked decoding, then
// record validation including the type's semantic checks.
func validate(registry *content.Registry, inner string) (content.Record, error) {
	value, err := registry.DecodeContent(loki.LogsTypeName, json.RawMessage(inner))
	if err != nil {
		return nil, err
	}
	ct, err := registry.Lookup(loki.LogsTypeName)
	if err != nil {
		return nil, err
	}
	record := content.NewItem(ct, time.Date(2026, 11, 13, 11, 0, 0, 0, time.UTC), value)
	return record, registry.ValidateRecord(record)
}

func ingest(t *testing.T, registry *content.Registry, inner string) content.Record {
	t.Helper()
	record, err := validate(registry, inner)
	testutil.NoErr(t, err)
	return record
}

func logsView(t *testing.T, record content.Record) loki.Logs {
	t.Helper()
	view, err := content.ItemView[loki.Logs](record)
	testutil.NoErr(t, err)
	return view.ContentValue
}

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

func cloneFields(t *testing.T, fields content.MarkdownFields) content.MarkdownFields {
	t.Helper()
	data, err := json.Marshal(fields)
	testutil.NoErr(t, err)
	var copied content.MarkdownFields
	testutil.NoErr(t, json.Unmarshal(data, &copied))
	return copied
}
