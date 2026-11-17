package prometheus

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
)

// TimeSeriesChartTypeName is the canonical name of the time-series chart type.
const TimeSeriesChartTypeName = "Prometheus/TimeSeriesChart"

// TimeSeriesChart is the inner content of a Prometheus/TimeSeriesChart
// record: a range-query definition with an optional stored snapshot of its
// results. Neither the server nor the browser executes the query; producers
// upload results fetched elsewhere.
type TimeSeriesChart struct {
	// Timestamp optionally sets the block's event time. Omit or null to use
	// the block's original server-side creation time.
	// It may lie outside the query window; it never alters sample timestamps.
	Timestamp *time.Time `json:"timestamp,omitempty"`
	// Query is the range query the stored results belong to.
	Query TimeSeriesQuery `json:"query"`
	// Title is an optional free-form display title.
	Title string `json:"title,omitempty"`
	// Unit is an optional free-form Y-axis display label (not a conversion
	// instruction).
	Unit string `json:"unit,omitempty"`
	// QueryResults is the stored snapshot of the query's results; omitted
	// or null means no snapshot was uploaded (or it was cleared). It is not
	// part of the Markdown header and is cleared when the query changes
	// through Markdown.
	QueryResults *TimeSeriesResults `json:"query_results,omitempty"`
}

// TimeSeriesQuery is the definition of a Prometheus range query.
type TimeSeriesQuery struct {
	// Expression is the nonblank PromQL expression (not parsed).
	Expression string `json:"expression"`
	// SourceURL is the optional absolute HTTP(S) URL of the Prometheus
	// server, stored for external tooling only (never contacted).
	SourceURL string `json:"source_url,omitempty"`
	// Start is the inclusive query start.
	Start time.Time `json:"start"`
	// End is the inclusive query end; it must be after Start.
	End time.Time `json:"end"`
	// StepSeconds is the query resolution: a finite positive number of
	// seconds (fractions allowed).
	StepSeconds float64 `json:"step_seconds"`
}

// TimeSeriesResults is a stored snapshot of query results. The bounds and
// step belong to the accompanying query. An explicit empty Series means
// the query succeeded without matching series.
type TimeSeriesResults struct {
	// Series is an explicit (non-null) array of the returned series.
	Series []Series `json:"series"`
}

// TimeSeriesChartType implements Prometheus/TimeSeriesChart. It renders
// an empty Markdown body; the query metadata, title, unit and optional
// timestamp form the structured Markdown header, while results stay hidden.
// Metadata changes create revisions; results-only changes with unchanged
// event time refresh in place (content.RevisionPolicy).
type TimeSeriesChartType struct {
	content.TypedMarkdownRenderer[content.Item[TimeSeriesChart]]
	content.TypedItemMarkdownHeader[TimeSeriesChart, timeSeriesChartMdHeader]
}

// NewTimeSeriesChartType constructs the Prometheus/TimeSeriesChart
// implementation.
func NewTimeSeriesChartType() *TimeSeriesChartType {
	return &TimeSeriesChartType{
		TypedMarkdownRenderer:   content.ItemMarkdown(func(content.Item[TimeSeriesChart]) (string, error) { return "", nil }),
		TypedItemMarkdownHeader: content.ItemMarkdownHeader(projectTimeSeriesChartMdHeader, replaceTimeSeriesChartMdHeader),
	}
}

func (*TimeSeriesChartType) Name() string { return TimeSeriesChartTypeName }

func (*TimeSeriesChartType) ContentSchema() content.Schema {
	return content.Schema{Source: schemaCUE, Definition: "#TimeSeriesChart"}
}

func (*TimeSeriesChartType) DecodeJSON(data json.RawMessage) (content.Content, error) {
	return content.DecodeContent[TimeSeriesChart](data)
}

// ValidateContent implements content.ContentValidator (see Validate).
func (*TimeSeriesChartType) ValidateContent(value content.Content) []content.FieldError {
	chart, err := content.ContentValue[TimeSeriesChart](value)
	if err != nil {
		return []content.FieldError{{Message: err.Error()}}
	}
	return chart.Validate()
}

// ShouldCreateRevision implements content.RevisionPolicy: changes of the
// query, title, unit or explicit timestamp create a revision; results-only
// changes are stored in place unless the host detects a changed event time.
func (*TimeSeriesChartType) ShouldCreateRevision(previous, next content.Record) (bool, error) {
	before, err := content.ItemView[TimeSeriesChart](previous)
	if err != nil {
		return false, err
	}
	after, err := content.ItemView[TimeSeriesChart](next)
	if err != nil {
		return false, err
	}
	return !before.ContentValue.SameMetadata(after.ContentValue), nil
}

// EventTimestamp implements content.EventTimestamper (see
// TimeSeriesChart.EventTimestamp).
func (*TimeSeriesChartType) EventTimestamp(value content.Content) (time.Time, error) {
	chart, err := content.ContentValue[TimeSeriesChart](value)
	if err != nil {
		return time.Time{}, err
	}
	return chart.EventTimestamp(), nil
}

// EventTimestamp returns the explicit Timestamp, or zero to request the
// host's original creation-time fallback. Results never affect placement.
func (c TimeSeriesChart) EventTimestamp() time.Time {
	if c.Timestamp != nil {
		return c.Timestamp.UTC()
	}
	return time.Time{}
}

// SameMetadata reports whether c and other have the same revision-worthy
// metadata: query (bounds compared by instant, strings as supplied),
// title, unit, and explicit timestamp (compared as an instant).
func (c TimeSeriesChart) SameMetadata(other TimeSeriesChart) bool {
	sameTimestamp := c.Timestamp == nil && other.Timestamp == nil ||
		c.Timestamp != nil && other.Timestamp != nil && c.Timestamp.Equal(*other.Timestamp)
	return c.Query.Same(other.Query) && c.Title == other.Title && c.Unit == other.Unit && sameTimestamp
}

// Same reports whether q and other define the same query: equal strings as
// supplied (no URL or PromQL normalization), bounds equal as instants, and
// equal steps.
func (q TimeSeriesQuery) Same(other TimeSeriesQuery) bool {
	return q.Expression == other.Expression && q.SourceURL == other.SourceURL &&
		q.Start.Equal(other.Start) && q.End.Equal(other.End) && q.StepSeconds == other.StepSeconds
}

// Validate checks what the schema cannot express, with paths following the
// chart JSON layout: a nonblank expression, an absolute HTTP(S) source
// URL (if given), start < end and a finite positive step (also without a
// snapshot), and the shared series rules of QueryResults.Validate for a
// present snapshot (explicit arrays/objects, finite timestamps,
// finite-or-null values, strictly increasing timestamps within the
// inclusive query bounds).
func (c TimeSeriesChart) Validate() []content.FieldError {
	var errs []content.FieldError
	if c.Timestamp != nil && c.Timestamp.IsZero() {
		errs = append(errs, content.FieldError{Path: "timestamp", Message: "must not be the zero time; omit or use null to use block creation time"})
	}
	if strings.TrimSpace(c.Query.Expression) == "" {
		errs = append(errs, content.FieldError{Path: "query.expression", Message: "must not be blank"})
	}
	if c.Query.SourceURL != "" && !isHTTPURL(c.Query.SourceURL) {
		errs = append(errs, content.FieldError{Path: "query.source_url", Message: "must be an absolute HTTP(S) URL"})
	}
	results := QueryResults{Start: c.Query.Start, End: c.Query.End, StepSeconds: c.Query.StepSeconds, Series: []Series{}}
	if c.QueryResults != nil {
		results.Series = c.QueryResults.Series
	}
	// Bounds/step errors belong to the query, series errors to the results.
	for _, field := range results.Validate("query") {
		if rest, ok := strings.CutPrefix(field.Path, "query.series"); ok {
			field.Path = "query_results.series" + rest
		}
		errs = append(errs, field)
	}
	return errs
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// timeSeriesChartMdHeader is the structured Markdown header of
// Prometheus/TimeSeriesChart: query metadata, title, unit and optional
// timestamp (the body is empty; results are not exposed).
type timeSeriesChartMdHeader struct {
	Timestamp *time.Time      `json:"timestamp,omitempty"`
	Query     TimeSeriesQuery `json:"query"`
	Title     string          `json:"title,omitempty"`
	Unit      string          `json:"unit,omitempty"`
}

func projectTimeSeriesChartMdHeader(c TimeSeriesChart) timeSeriesChartMdHeader {
	return timeSeriesChartMdHeader{Timestamp: c.Timestamp, Query: c.Query, Title: c.Title, Unit: c.Unit}
}

// replaceTimeSeriesChartMdHeader applies the header, preserving the stored
// results only when the query is unchanged (title/unit/timestamp edits
// keep them; any query change clears them).
func replaceTimeSeriesChartMdHeader(existing TimeSeriesChart, h timeSeriesChartMdHeader) TimeSeriesChart {
	c := TimeSeriesChart{Timestamp: h.Timestamp, Query: h.Query, Title: h.Title, Unit: h.Unit}
	if existing.Query.Same(h.Query) {
		c.QueryResults = existing.QueryResults
	}
	return c
}
