package prometheus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
)

// Payload is the transient input of a Prometheus/AlertsMatcher expansion:
// the complete replacement of the retained query results. Matcher metadata
// is not part of it. Omitting QueryResults clears the retained results.
type Payload struct {
	QueryResults *QueryResults `json:"query_results,omitempty"`
}

// QueryResults are producer-fetched Prometheus range-query results retained
// in an AlertsMatcher record. Start/End/StepSeconds describe the actual
// query chosen by the producer, independent of the matcher's incident
// since/until. An explicit empty Series means the query succeeded without
// matching series; absent QueryResults mean no data was submitted.
type QueryResults struct {
	// Start is the inclusive query start.
	Start time.Time `json:"start"`
	// End is the inclusive query end; it must be after Start.
	End time.Time `json:"end"`
	// StepSeconds is the producer's query resolution: a finite positive
	// number of seconds (fractions allowed).
	StepSeconds float64 `json:"step_seconds"`
	// Series is an explicit (non-null) array of the returned series.
	Series []Series `json:"series"`
}

// Series is one returned time series.
type Series struct {
	// Labels is the series' label set.
	Labels map[string]string `json:"labels"`
	// Samples is an explicit (non-null) array of samples with strictly
	// increasing timestamps inside the query bounds. Gaps are allowed and
	// timestamps need not align to the query step.
	Samples []Sample `json:"samples"`
}

// Sample is one sample, encoded in JSON as a compact [unix_seconds, value]
// pair (see the schema companion constraints). The timestamp is a finite
// number and may include fractional seconds; the value is a finite number
// or null (e.g. a non-finite Prometheus value mapped by the producer).
type Sample struct {
	// Timestamp is the Unix time in seconds.
	Timestamp float64
	// Value is the sample value; nil encodes JSON null, distinct from a
	// pointer to zero.
	Value *float64
}

// Value returns a pointer to v, convenient for building samples.
func Value(v float64) *float64 { return &v }

// MarshalJSON encodes the sample as a [unix_seconds, value|null] pair.
func (s Sample) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]any{s.Timestamp, s.Value})
}

// UnmarshalJSON strictly decodes a [unix_seconds, value|null] pair,
// rejecting any other shape (the schema validates it beforehand) rather
// than coercing nulls or other types to numbers.
func (s *Sample) UnmarshalJSON(data []byte) error {
	var pair []json.RawMessage
	if err := json.Unmarshal(data, &pair); err != nil {
		return fmt.Errorf("decode sample: %w", err)
	}
	if pair == nil || len(pair) != 2 {
		return fmt.Errorf("decode sample: expected a [unix_seconds, value] pair, got %d entries", len(pair))
	}
	timestamp, err := decodeSampleNumber(pair[0])
	if err != nil {
		return fmt.Errorf("decode sample timestamp: %w", err)
	}
	if timestamp == nil {
		return fmt.Errorf("decode sample timestamp: must not be null")
	}
	value, err := decodeSampleNumber(pair[1])
	if err != nil {
		return fmt.Errorf("decode sample value: %w", err)
	}
	*s = Sample{Timestamp: *timestamp, Value: value}
	return nil
}

// decodeSampleNumber decodes a JSON number or null (nil); other JSON types
// are rejected.
func decodeSampleNumber(raw json.RawMessage) (*float64, error) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	if len(trimmed) == 0 || (trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9')) {
		return nil, fmt.Errorf("expected a number or null, got %s", trimmed)
	}
	var v float64
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// Validate checks what the schema cannot express: start < end, a finite
// positive step, explicit arrays, a finite timestamp and finite-or-null
// value per sample, and strictly increasing timestamps (null-valued
// samples included) within the inclusive bounds. Field paths are
// prefixed with path (e.g. "query_results"). It returns the collected
// field errors; callers reusing the contract (PagerDuty forwarding) can
// prefix their own path.
func (q QueryResults) Validate(path string) []content.FieldError {
	var errs []content.FieldError
	add := func(field, format string, args ...any) {
		errs = append(errs, content.FieldError{Path: path + "." + field, Message: fmt.Sprintf(format, args...)})
	}
	if !q.Start.Before(q.End) {
		add("end", "must be after start")
	}
	if math.IsNaN(q.StepSeconds) || math.IsInf(q.StepSeconds, 0) || q.StepSeconds <= 0 {
		add("step_seconds", "must be a finite positive number")
	}
	if q.Series == nil {
		add("series", "must be an explicit array; missing or null arrays are invalid")
	}
	start, end := unixSeconds(q.Start), unixSeconds(q.End)
	for i, series := range q.Series {
		seriesPath := fmt.Sprintf("series.%d", i)
		if series.Labels == nil {
			add(seriesPath+".labels", "must be an explicit object")
		}
		if series.Samples == nil {
			add(seriesPath+".samples", "must be an explicit array; missing or null arrays are invalid")
		}
		previous := math.Inf(-1)
		for j, sample := range series.Samples {
			samplePath := fmt.Sprintf("%s.samples.%d", seriesPath, j)
			ts := sample.Timestamp
			if !finite(ts) || (sample.Value != nil && !finite(*sample.Value)) {
				add(samplePath, "must contain a finite timestamp and a finite number or null value")
				continue
			}
			if ts < start || ts > end {
				add(samplePath+".0", "timestamp %s is outside the query bounds", strconv.FormatFloat(ts, 'f', -1, 64))
			}
			if ts <= previous {
				add(samplePath+".0", "timestamps must be strictly increasing")
			}
			previous = ts
		}
	}
	return errs
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// unixSeconds converts t to fractional Unix seconds.
func unixSeconds(t time.Time) float64 {
	return float64(t.Unix()) + float64(t.Nanosecond())/1e9
}
