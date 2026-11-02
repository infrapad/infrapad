package prometheus_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/prometheus"
)

// TestAlertsMatcherScenario exercises Prometheus/AlertsMatcher through the
// registry: record round-trips over the generated schema and host codecs
// with normalized field names, open-ended intervals, empty rendering, and
// the absence of a Markdown updater capability.
func TestAlertsMatcherScenario(t *testing.T) {
	registry, err := content.NewRegistry(prometheus.ContentTypes())
	must(t, err)
	matcher, err := registry.Lookup(prometheus.AlertsMatcherTypeName)
	must(t, err)

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
	reloaded := roundTrip(t, registry, content.NewItem(matcher, at("10:00"), value))
	view, err := content.ItemView[prometheus.AlertsMatcher](reloaded)
	must(t, err)
	if !view.ContentValue.Since.Equal(at("09:00")) || !view.ContentValue.Until.Equal(at("12:00")) ||
		len(view.ContentValue.LabelsMatchers) != 2 || view.ContentValue.Extras["dashboard"] != "https://grafana.example/x" {
		t.Fatalf("unexpected content: %#v", view.ContentValue)
	}

	// A zero Until means an open-ended interval and survives the round trip.
	open := prometheus.AlertsMatcher{LabelsMatchers: value.LabelsMatchers, Since: value.Since}
	view, err = content.ItemView[prometheus.AlertsMatcher](roundTrip(t, registry, content.NewItem(matcher, at("10:00"), open)))
	must(t, err)
	if !view.ContentValue.Until.IsZero() {
		t.Fatalf("expected an open-ended interval, got %v", view.ContentValue.Until)
	}

	// Omitting until also means an open-ended interval.
	decoded, err := registry.DecodeRecord(prometheus.AlertsMatcherTypeName, item(`{
		"labels_matchers": [{"severity": ["critical"]}],
		"since": "2026-10-25T09:00:00Z"
	}`))
	must(t, err)
	view, err = content.ItemView[prometheus.AlertsMatcher](decoded)
	must(t, err)
	if !view.ContentValue.Until.IsZero() || !view.ContentValue.Since.Equal(at("09:00")) {
		t.Fatalf("unexpected decoded content: %#v", view.ContentValue)
	}

	// The matcher renders no Markdown body of its own.
	body, err := matcher.RenderMarkdown(reloaded)
	must(t, err)
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
	must(t, err)

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

// roundTrip encodes and decodes a record, checking the encoding is stable.
func roundTrip[R content.Record](t *testing.T, registry *content.Registry, record R) content.Record {
	t.Helper()
	data, err := content.EncodeRecord(record)
	must(t, err)
	decoded, err := registry.DecodeRecord(record.ContentType().Name(), data)
	must(t, err)
	again, err := content.EncodeRecord(decoded)
	must(t, err)
	if !bytes.Equal(data, again) {
		t.Fatalf("record changed through codec:\n%s\n%s", data, again)
	}
	return decoded
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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
