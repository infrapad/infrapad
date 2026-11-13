package pagerduty

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
)

// Expand turns a complete incident payload into the enriched primary and
// its generated output: the Markdown summary in the primary's text (with
// the source incident creation time as its timestamp) and the notes/log
// entries as one PagerDuty/Timeline collection of stably keyed members,
// plus an optional Prometheus/AlertsMatcher child derived from the alert
// details (see deriveMatcher), receiving the payload's query results through
// cascading expansion (see matcherBlock). Final sorting, grouping, interleaving, and deletion of missing output are
// up to the host. existing is not modified.
func (t *IncidentType) Expand(ctx context.Context, existing content.Record, payload json.RawMessage) (content.ExpansionResult, error) {
	item, err := content.ItemView[Incident](existing)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	input, err := content.DecodePayload[Payload](payload)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	// Semantic and matcher-detail errors are collected together; any of
	// them rejects the whole submission.
	validationErrors := validatePayload(item.ContentValue, input)
	matcher, matcherErrors := deriveMatcher(input.Alerts)
	validationErrors = append(validationErrors, matcherErrors...)
	if input.QueryResults != nil {
		validationErrors = append(validationErrors, input.QueryResults.Validate("query_results")...)
	}
	if len(validationErrors) > 0 {
		return content.ExpansionResult{}, &content.ValidationError{ContentType: IncidentTypeName, Stage: content.StagePayload, Fields: validationErrors}
	}

	primary, err := enrichedPrimary(existing, item.ContentValue, input)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	result := content.ExpansionResult{Record: primary}
	timeline, err := t.timelineBlock(ctx, input)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	if timeline != nil {
		result.Blocks = append(result.Blocks, content.EmittedBlock{Record: timeline})
	}
	matcherBlock, err := t.matcherBlock(matcherOrFallback(matcher, input), input.QueryResults)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	if matcherBlock != nil {
		result.Blocks = append(result.Blocks, *matcherBlock)
	}
	return result, nil
}

// enrichedPrimary returns a copy of the primary with the summary of the
// canonically ordered alerts and the source incident creation time,
// preserving its type, reference, and any generated key.
func enrichedPrimary(existing content.Record, incident Incident, input Payload) (content.Record, error) {
	alerts := slices.Clone(input.Alerts)
	slices.SortStableFunc(alerts, func(a, b SourceAlert) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	incident.Text = FormatIncidentSummary(input.Incident, alerts)
	primary, err := content.WithContent(existing, incident)
	if err != nil {
		return nil, err
	}
	return content.WithTimestamp(primary, input.Incident.CreatedAt.UTC())
}

// timelineBlock builds one timeline collection of the notes and log
// entries with their source times, or nil when there are none.
func (t *IncidentType) timelineBlock(ctx context.Context, input Payload) (content.GroupableRecord, error) {
	var members []content.KeyedRecord
	for _, note := range input.Notes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		members = append(members, content.NewKeyedItem(t.timelineType, NoteKey(note.ID), note.CreatedAt.UTC(),
			NoteTimelineEntry(note, input.Incident.URL)))
	}
	for _, entry := range input.LogEntries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		members = append(members, content.NewKeyedItem(t.timelineType, LogEntryKey(entry.ID), entry.CreatedAt.UTC(),
			LogTimelineEntry(entry, input.Incident.URL)))
	}
	if len(members) == 0 {
		return nil, nil
	}
	return t.timelineType.NewCollection(members)
}

// validatePayload checks what the payload schema cannot express: the
// incident matches the primary reference, collections are explicit arrays,
// creation times are nonzero, and IDs are unique within each collection
// (they may overlap across collections). It returns the collected field
// errors.
func validatePayload(incident Incident, input Payload) []content.FieldError {
	var fieldErrors []content.FieldError
	add := func(path, format string, args ...any) {
		fieldErrors = append(fieldErrors, content.FieldError{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if input.Incident.ID != incident.ID {
		add("incident.id", "payload incident %q does not match the referenced incident %q", input.Incident.ID, incident.ID)
	}
	if input.Incident.URL != incident.URL {
		add("incident.url", "payload incident URL %q does not match the referenced URL %q", input.Incident.URL, incident.URL)
	}
	if input.Incident.CreatedAt.IsZero() {
		add("incident.created_at", "must be a nonzero time")
	}
	checkCollection(add, "alerts", input.Alerts, func(a SourceAlert) (string, time.Time) { return a.ID, a.CreatedAt })
	checkCollection(add, "notes", input.Notes, func(n SourceNote) (string, time.Time) { return n.ID, n.CreatedAt })
	checkCollection(add, "log_entries", input.LogEntries, func(e SourceLogEntry) (string, time.Time) { return e.ID, e.CreatedAt })
	return fieldErrors
}

// checkCollection reports a missing/null collection, zero creation times,
// and duplicate IDs within it.
func checkCollection[T any](add func(path, format string, args ...any), name string, items []T, identity func(T) (string, time.Time)) {
	// An explicit empty array is valid; missing or null arrays are not
	// (the generated CUE schema cannot distinguish them).
	if items == nil {
		add(name, "must be an explicit array; missing or null arrays are invalid")
		return
	}
	seen := make(map[string]struct{}, len(items))
	for i, item := range items {
		id, createdAt := identity(item)
		if createdAt.IsZero() {
			add(fmt.Sprintf("%s.%d.created_at", name, i), "must be a nonzero time")
		}
		if _, duplicate := seen[id]; duplicate {
			add(fmt.Sprintf("%s.%d.id", name, i), "duplicate ID %q", id)
		}
		seen[id] = struct{}{}
	}
}
