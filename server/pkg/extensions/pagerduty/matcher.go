package pagerduty

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/prometheus"
)

// AlertsMatcherKey is the constant, parent-scoped key of the optional
// Prometheus/AlertsMatcher child of an incident. It does not depend on
// labels or times, so refreshes keep the child's identity.
const AlertsMatcherKey = "pagerduty-alerts-matcher"

// matcherBlock returns the incident's Prometheus/AlertsMatcher child, timed
// at the matcher's since, or nil when there is no matcher.
func (t *IncidentType) matcherBlock(matcher *prometheus.AlertsMatcher) content.KeyedRecord {
	if matcher == nil {
		return nil
	}
	return content.NewKeyedItem(t.matcherType, AlertsMatcherKey, matcher.Since, *matcher)
}

// labeledOccurrence is one alert occurrence with usable labels. A zero end
// means unknown.
type labeledOccurrence struct {
	labels     map[string]string
	start, end time.Time
}

// deriveMatcher extracts labels and time bounds from the alerts' details.
// Details recognized as Alertmanager-like (an object with "firing" and/or
// "resolved") are interpreted; other shapes contribute nothing. Malformed
// recognized details are reported as field errors. It returns nil when no
// usable labels exist.
//
// The matcher has one alternative per distinct complete label set, in
// canonical order. Since is the earliest start; until is the latest end
// only when every represented occurrence has a known end.
func deriveMatcher(alerts []SourceAlert) (*prometheus.AlertsMatcher, []content.FieldError) {
	var occurrences []labeledOccurrence
	var fields []content.FieldError
	for i, alert := range alerts {
		found, errs := alertOccurrences(fmt.Sprintf("alerts.%d.details", i), alert)
		occurrences = append(occurrences, found...)
		fields = append(fields, errs...)
	}
	if len(fields) > 0 || len(occurrences) == 0 {
		return nil, fields
	}

	var since, until time.Time
	openEnded := false
	sets := map[string]map[string]string{}
	for _, o := range occurrences {
		if since.IsZero() || o.start.Before(since) {
			since = o.start
		}
		if o.end.IsZero() {
			openEnded = true
		} else if o.end.After(until) {
			until = o.end
		}
		sets[canonicalLabels(o.labels)] = o.labels
	}
	matcher := &prometheus.AlertsMatcher{Since: since}
	if !openEnded {
		matcher.Until = until
	}
	for _, key := range slices.Sorted(maps.Keys(sets)) {
		alternative := make(map[string][]string, len(sets[key]))
		for name, value := range sets[key] {
			alternative[name] = []string{value}
		}
		matcher.LabelsMatchers = append(matcher.LabelsMatchers, alternative)
	}
	return matcher, nil
}

// alertOccurrences interprets one alert's details. The "firing" and
// "resolved" sections are parsed independently, each as an array of
// structured Alertmanager records or as text with a Labels: section.
// Missing/null sections are skipped; the section name implies no end time.
func alertOccurrences(path string, alert SourceAlert) ([]labeledOccurrence, []content.FieldError) {
	details, ok := decodeDetails(alert.Details)
	if !ok {
		return nil, nil
	}
	var occurrences []labeledOccurrence
	var fields []content.FieldError
	for _, section := range []string{"firing", "resolved"} {
		sectionPath := path + "." + section
		switch value := details[section].(type) {
		case nil:
		case string:
			if labels := parseTextLabels(value); len(labels) > 0 {
				// Text records start at the alert creation time; no end.
				occurrences = append(occurrences, labeledOccurrence{labels: labels, start: alert.CreatedAt.UTC()})
			}
		case []any:
			for j, raw := range value {
				o, errs := structuredOccurrence(fmt.Sprintf("%s.%d", sectionPath, j), raw)
				fields = append(fields, errs...)
				if o != nil {
					occurrences = append(occurrences, *o)
				}
			}
		default:
			fields = append(fields, content.FieldError{Path: sectionPath,
				Message: "must be an array of Alertmanager alert records or a text string"})
		}
	}
	return occurrences, fields
}

// decodeDetails decodes details as a JSON object containing "firing" or
// "resolved"; ok is false for any other (unsupported) shape. Numbers are
// kept as their literal text.
func decodeDetails(raw json.RawMessage) (map[string]any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var details map[string]any
	if err := decoder.Decode(&details); err != nil || details == nil {
		return nil, false
	}
	_, firing := details["firing"]
	_, resolved := details["resolved"]
	return details, firing || resolved
}

// structuredOccurrence validates one structured record: a required, valid,
// nonzero startsAt; an optional endsAt (missing, empty, or zero means
// unknown); and optional labels with string, number, or boolean values.
// It returns nil when the record has no usable labels or is invalid.
func structuredOccurrence(path string, raw any) (*labeledOccurrence, []content.FieldError) {
	record, ok := raw.(map[string]any)
	if !ok {
		return nil, []content.FieldError{{Path: path, Message: "must be an Alertmanager alert record object"}}
	}
	var fields []content.FieldError
	add := func(field, format string, args ...any) {
		fields = append(fields, content.FieldError{Path: path + "." + field, Message: fmt.Sprintf(format, args...)})
	}

	start, err := parseAlertTime(record["startsAt"])
	switch {
	case err != nil:
		add("startsAt", "%v", err)
	case start.IsZero():
		add("startsAt", "must be a nonzero RFC 3339 time")
	}
	end, err := parseAlertTime(record["endsAt"])
	if err != nil {
		add("endsAt", "%v", err)
	}

	var labels map[string]string
	switch rawLabels := record["labels"].(type) {
	case nil:
	case map[string]any:
		labels = make(map[string]string, len(rawLabels))
		for _, name := range slices.Sorted(maps.Keys(rawLabels)) {
			switch value := rawLabels[name].(type) {
			case string:
				labels[name] = value
			case json.Number:
				labels[name] = value.String()
			case bool:
				labels[name] = strconv.FormatBool(value)
			default:
				add("labels."+name, "must be a string, number, or boolean")
			}
		}
	default:
		add("labels", "must be an object of label values")
	}

	if len(fields) > 0 || len(labels) == 0 {
		return nil, fields
	}
	return &labeledOccurrence{labels: labels, start: start, end: end}, nil
}

// parseAlertTime parses an optional RFC 3339 timestamp. A missing, null,
// empty, or zero value yields the zero time; other non-string or
// unparseable values are an error.
func parseAlertTime(raw any) (time.Time, error) {
	switch value := raw.(type) {
	case nil:
		return time.Time{}, nil
	case string:
		if value == "" {
			return time.Time{}, nil
		}
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return time.Time{}, fmt.Errorf("must be an RFC 3339 time: %q", value)
		}
		return parsed.UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("must be an RFC 3339 time string")
	}
}

// textLabelLine matches a label line of a text-rendered alert payload.
var textLabelLine = regexp.MustCompile(`^ - (.+?) = (.*)$`)

// parseTextLabels extracts the " - key = value" lines of the first Labels:
// section of a text-rendered alert payload. The section ends at the first
// non-label line (e.g. Annotations:); annotations are not labels.
func parseTextLabels(text string) map[string]string {
	var labels map[string]string
	inLabels := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !inLabels {
			inLabels = strings.HasPrefix(line, "Labels:")
			continue
		}
		m := textLabelLine.FindStringSubmatch(line)
		if m == nil {
			break
		}
		if labels == nil {
			labels = map[string]string{}
		}
		labels[m[1]] = m[2]
	}
	return labels
}

// canonicalLabels builds the deduplication and ordering key of a complete
// label set.
func canonicalLabels(labels map[string]string) string {
	names := slices.Sorted(maps.Keys(labels))
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"\x00"+labels[name])
	}
	return strings.Join(parts, "\x01")
}
