package prometheus

import (
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
// pair of numbers (see the schema companion constraints). Both coordinates
// are finite; the timestamp may include fractional seconds.
type Sample struct {
	// Timestamp is the Unix time in seconds.
	Timestamp float64
	// Value is the sample value.
	Value float64
}

// MarshalJSON encodes the sample as a [unix_seconds, value] pair.
func (s Sample) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]float64{s.Timestamp, s.Value})
}

// UnmarshalJSON decodes a [unix_seconds, value] pair, rejecting any other
// shape (the schema validates it beforehand).
func (s *Sample) UnmarshalJSON(data []byte) error {
	var pair []float64
	if err := json.Unmarshal(data, &pair); err != nil {
		return fmt.Errorf("decode sample: %w", err)
	}
	if len(pair) != 2 {
		return fmt.Errorf("decode sample: expected a [unix_seconds, value] pair, got %d entries", len(pair))
	}
	s.Timestamp, s.Value = pair[0], pair[1]
	return nil
}

// Validate checks what the schema cannot express: start < end, a finite
// positive step, explicit arrays, finite sample coordinates, and strictly
// increasing timestamps within the inclusive bounds. Field paths are
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
			ts, value := sample.Timestamp, sample.Value
			if !finite(ts) || !finite(value) {
				add(samplePath, "must contain two finite numbers")
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
