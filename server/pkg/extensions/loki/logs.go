// Package loki provides the Loki/Logs content type: a stored snapshot of a
// LogQL query's log streams uploaded by external tooling. Neither the
// server nor the browser queries Loki.
package loki

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
)

// LogsTypeName is the canonical name of the Loki logs snapshot type.
const LogsTypeName = "Loki/Logs"

//go:embed schema.cue
var schemaCUE []byte

// ContentTypes returns the content types of the Loki extension.
func ContentTypes() []content.ContentType {
	return []content.ContentType{NewLogsType()}
}

// Logs is the inner content of a Loki/Logs record: a log query definition
// with an optional stored snapshot of its result streams. The query
// direction is not stored; forward is the convention.
type Logs struct {
	// Title is an optional free-form display title.
	Title string `json:"title,omitempty"`
	// Query is the log query the stored results belong to.
	Query LogsQuery `json:"query"`
	// QueryResults is the stored snapshot of the query's results; omitted
	// or null means no snapshot was uploaded (or it was cleared). It is not
	// part of the Markdown header and is cleared when the query changes
	// through Markdown.
	QueryResults *LogsResults `json:"query_results,omitempty"`
}

// LogsQuery is the definition of a Loki log query, including optional
// descriptive provenance (never credentials or connection configuration).
type LogsQuery struct {
	// Expression is the nonblank LogQL expression (not parsed).
	Expression string `json:"expression"`
	// Start is the inclusive query start.
	Start time.Time `json:"start"`
	// End is the inclusive query end; it must be after Start.
	End time.Time `json:"end"`
	// Limit is the requested positive entry limit (not a completeness
	// guarantee).
	Limit int64 `json:"limit"`
	// SourceURL is the optional absolute HTTP(S) URL of the source, stored
	// for provenance only (never contacted).
	SourceURL string `json:"source_url,omitempty"`
	// DatasourceUID is the optional Grafana datasource UID.
	DatasourceUID string `json:"datasource_uid,omitempty"`
	// DatasourceName is the optional Grafana datasource name.
	DatasourceName string `json:"datasource_name,omitempty"`
}

// LogsResults is a stored snapshot of query results. An explicit empty
// Streams means the query succeeded without matching entries.
type LogsResults struct {
	// Streams is an explicit (non-null) array of the returned streams, in
	// submitted order.
	Streams []Stream `json:"streams"`
}

// Stream is one returned log stream.
type Stream struct {
	// Labels is the explicit (non-null) stream label set.
	Labels map[string]string `json:"labels"`
	// Entries is an explicit (non-null) array of entries in submitted
	// order; duplicates are retained and no ordering is enforced.
	Entries []Entry `json:"entries"`
}

// Severity is the canonical per-entry log severity. An empty severity means
// no severity evidence was submitted; unrecognized values map to unknown.
type Severity string

const (
	SeverityTrace    Severity = "trace"
	SeverityDebug    Severity = "debug"
	SeverityInfo     Severity = "info"
	SeverityWarn     Severity = "warn"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
	SeverityFatal    Severity = "fatal"
	SeverityUnknown  Severity = "unknown"
)

// Entry is one log entry.
type Entry struct {
	// Timestamp is the decimal Unix-nanosecond timestamp string, kept
	// verbatim for lossless precision.
	Timestamp string `json:"timestamp"`
	// Line is the original log message (may be empty).
	Line string `json:"line"`
	// Severity is normalized on decoding. When omitted, it is derived from
	// entry metadata, JSON message fields, then stream labels, in that order.
	Severity Severity `json:"severity,omitempty"`
	// StructuredMetadata is optional per-entry structured metadata.
	StructuredMetadata map[string]string `json:"structured_metadata,omitempty"`
}

// LogsType implements Loki/Logs. It renders an empty Markdown body; the
// query and title form the structured Markdown header, while the results
// stay hidden. Metadata changes create revisions, results-only changes
// replace the latest revision in place (content.RevisionPolicy).
type LogsType struct {
	content.TypedMarkdownRenderer[content.Item[Logs]]
	content.TypedItemMarkdownHeader[Logs, logsMdHeader]
}

// NewLogsType constructs the Loki/Logs implementation.
func NewLogsType() *LogsType {
	return &LogsType{
		TypedMarkdownRenderer:   content.ItemMarkdown(func(content.Item[Logs]) (string, error) { return "", nil }),
		TypedItemMarkdownHeader: content.ItemMarkdownHeader(projectLogsMdHeader, replaceLogsMdHeader),
	}
}

func (*LogsType) Name() string { return LogsTypeName }

func (*LogsType) ContentSchema() content.Schema {
	return content.Schema{Source: schemaCUE, Definition: "#Logs"}
}

func (*LogsType) DecodeJSON(data json.RawMessage) (content.Content, error) {
	var logs Logs
	if err := json.Unmarshal(data, &logs); err != nil {
		return nil, err
	}
	if logs.QueryResults != nil {
		for i := range logs.QueryResults.Streams {
			stream := &logs.QueryResults.Streams[i]
			for j := range stream.Entries {
				entry := &stream.Entries[j]
				entry.Severity = entrySeverity(*entry, stream.Labels)
			}
		}
	}
	return logs, nil
}

// ValidateContent implements content.ContentValidator (see Validate).
func (*LogsType) ValidateContent(value content.Content) []content.FieldError {
	logs, err := content.ContentValue[Logs](value)
	if err != nil {
		return []content.FieldError{{Message: err.Error()}}
	}
	return logs.Validate()
}

// ShouldCreateRevision implements content.RevisionPolicy: changes of the
// query or title create a revision; results-only changes (including adding
// or clearing the snapshot) are stored in place.
func (*LogsType) ShouldCreateRevision(previous, next content.Record) (bool, error) {
	before, err := content.ItemView[Logs](previous)
	if err != nil {
		return false, err
	}
	after, err := content.ItemView[Logs](next)
	if err != nil {
		return false, err
	}
	return !before.ContentValue.SameMetadata(after.ContentValue), nil
}

// SameMetadata reports whether l and other have the same revision-worthy
// metadata: query and title.
func (l Logs) SameMetadata(other Logs) bool {
	return l.Query.Same(other.Query) && l.Title == other.Title
}

// Same reports whether q and other define the same query: strings equal as
// supplied (no normalization), bounds equal as instants, and equal limits.
func (q LogsQuery) Same(other LogsQuery) bool {
	return q.Expression == other.Expression && q.Start.Equal(other.Start) && q.End.Equal(other.End) &&
		q.Limit == other.Limit && q.SourceURL == other.SourceURL &&
		q.DatasourceUID == other.DatasourceUID && q.DatasourceName == other.DatasourceName
}

// decimalInteger matches a base-10 integer without a plus sign or spaces.
var decimalInteger = regexp.MustCompile(`^-?[0-9]+$`)

// Validate checks what the schema cannot express, with paths following the
// content JSON layout: a nonblank expression, start < end, a positive
// limit, an absolute HTTP(S) source URL (if given), and for a present
// snapshot explicit streams/labels/entries and int64 Unix-nanosecond
// timestamps within the inclusive query bounds. Order and duplicates are
// not checked.
func (l Logs) Validate() []content.FieldError {
	var errs []content.FieldError
	add := func(path, format string, args ...any) {
		errs = append(errs, content.FieldError{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	q := l.Query
	if strings.TrimSpace(q.Expression) == "" {
		add("query.expression", "must not be blank")
	}
	if !q.Start.Before(q.End) {
		add("query.end", "must be after start")
	}
	if q.Limit <= 0 {
		add("query.limit", "must be a positive integer")
	}
	if q.SourceURL != "" && !isHTTPURL(q.SourceURL) {
		add("query.source_url", "must be an absolute HTTP(S) URL")
	}
	if l.QueryResults == nil {
		return errs
	}
	if l.QueryResults.Streams == nil {
		add("query_results.streams", "must be an explicit array; missing or null arrays are invalid")
	}
	for i, stream := range l.QueryResults.Streams {
		streamPath := fmt.Sprintf("query_results.streams.%d", i)
		if stream.Labels == nil {
			add(streamPath+".labels", "must be an explicit object")
		}
		if stream.Entries == nil {
			add(streamPath+".entries", "must be an explicit array; missing or null arrays are invalid")
		}
		for j, entry := range stream.Entries {
			entryPath := fmt.Sprintf("%s.entries.%d", streamPath, j)
			if entry.Severity != normalizeSeverity(string(entry.Severity)) {
				add(entryPath+".severity", "must be a canonical severity: trace, debug, info, warn, error, critical, fatal, or unknown")
			}
			path := entryPath + ".timestamp"
			ns, err := strconv.ParseInt(entry.Timestamp, 10, 64)
			if err != nil || !decimalInteger.MatchString(entry.Timestamp) {
				add(path, "must be a base-10 integer string of signed 64-bit Unix nanoseconds")
				continue
			}
			// Exact comparison: time.Unix keeps full nanosecond precision.
			if ts := time.Unix(0, ns); ts.Before(q.Start) || ts.After(q.End) {
				add(path, "timestamp %s is outside the query bounds", entry.Timestamp)
			}
		}
	}
	return errs
}

// entrySeverity runs at the JSON boundary, including decoding older stored
// snapshots that lack Severity. It never modifies original evidence fields.
func entrySeverity(entry Entry, labels map[string]string) Severity {
	if severity := normalizeSeverity(string(entry.Severity)); severity != "" {
		return severity
	}
	if severity := mapSeverity(entry.StructuredMetadata); severity != "" {
		return severity
	}
	// Inspect JSON only for severity; do not rewrite or pretty-print Line.
	if strings.HasPrefix(strings.TrimSpace(entry.Line), "{") {
		var fields map[string]json.RawMessage
		if json.Unmarshal([]byte(entry.Line), &fields) == nil {
			for _, key := range severityFields {
				var raw string
				if json.Unmarshal(fields[key], &raw) == nil {
					if severity := normalizeSeverity(raw); severity != "" {
						return severity
					}
				}
			}
		}
	}
	return mapSeverity(labels)
}

var severityFields = []string{"level", "severity_text", "detected_level", "severity"}

func mapSeverity(fields map[string]string) Severity {
	for _, key := range severityFields {
		if severity := normalizeSeverity(fields[key]); severity != "" {
			return severity
		}
	}
	return ""
}

// normalizeSeverity maps source spellings to a bounded set of display values.
// Unknown values are not guessed from message text; originals remain in the
// submitted Line, StructuredMetadata, or Labels for inspection.
func normalizeSeverity(raw string) Severity {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return ""
	case "trace":
		return SeverityTrace
	case "debug":
		return SeverityDebug
	case "info", "information", "informational", "notice":
		return SeverityInfo
	case "warn", "warning":
		return SeverityWarn
	case "error", "err":
		return SeverityError
	case "critical", "crit", "alert":
		return SeverityCritical
	case "fatal", "panic", "emergency", "emerg":
		return SeverityFatal
	default:
		return SeverityUnknown
	}
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// logsMdHeader is the structured Markdown header of Loki/Logs: the query
// and title (the body is empty; the results are not exposed).
type logsMdHeader struct {
	Title string    `json:"title,omitempty"`
	Query LogsQuery `json:"query"`
}

func projectLogsMdHeader(l Logs) logsMdHeader {
	return logsMdHeader{Title: l.Title, Query: l.Query}
}

// replaceLogsMdHeader applies the header, preserving the stored results
// only when the query is unchanged (title-only edits keep them; any query
// change, including provenance, clears them).
func replaceLogsMdHeader(existing Logs, h logsMdHeader) Logs {
	l := Logs{Title: h.Title, Query: h.Query}
	if existing.Query.Same(h.Query) {
		l.QueryResults = existing.QueryResults
	}
	return l
}
