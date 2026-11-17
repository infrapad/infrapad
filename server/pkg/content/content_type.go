package content

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ContentType is the required contract implemented once per canonical
// namespaced type (e.g. "Slack/Conversation"). Optional behavior is advertised
// through the small capability interfaces below.
type ContentType interface {
	// Name returns the canonical namespaced type name ("Namespace/Name").
	Name() string
	// ContentSchema describes a single inner content value, also for
	// groupable types. Host record metadata is never part of it.
	ContentSchema() Schema
	// DecodeJSON decodes only the inner content JSON value, never a record
	// envelope. Usually implemented via DecodeContent[T].
	DecodeJSON(json.RawMessage) (Content, error)
	// RenderMarkdown returns the Markdown body of a record; the host owns
	// block boundaries and metadata. Groupable types receive the
	// host-selected collection.
	RenderMarkdown(Record) (string, error)
}

// Schema references a self-contained CUE artifact and the definition selector
// within it, e.g. Schema{Source: slackCUE, Definition: "#Message"}. Extension
// artifacts are generated from Go types.
type Schema struct {
	Source     []byte
	Definition string
}

// GroupableContentType advertises that records of this type are stored as
// collections of keyed items, grouped by the host. A groupable type cannot
// also be an Expander.
type GroupableContentType interface {
	ContentType
	NewCollection([]KeyedRecord) (GroupableRecord, error)
}

// Expander is implemented by primary (reference) types that turn a transient
// payload into the complete desired direct output.
type Expander interface {
	// PayloadSchema describes the transient ingestion payload.
	PayloadSchema() Schema
	// Expand receives the existing primary record (target-first) and the
	// transient payload. It returns resulting primary state and the complete
	// desired direct output; it must not mutate the existing record and must
	// preserve the key of a keyed (generated) primary.
	Expand(ctx context.Context, existing Record, payload json.RawMessage) (ExpansionResult, error)
}

// ExpansionResult is the complete output of one expansion.
type ExpansionResult struct {
	// Record is the resulting primary state.
	Record Record
	// Blocks is the complete desired direct output. Groupable collection
	// boundaries do not prescribe final persisted groups.
	Blocks []EmittedBlock
}

// Members returns the keyed constituents of all emitted blocks in emission
// order (collections flattened), for host grouping/reconciliation.
func (r ExpansionResult) Members() []KeyedRecord {
	var members []KeyedRecord
	for _, block := range r.Blocks {
		members = append(members, Members(block.Record)...)
	}
	return members
}

// EmittedBlock is one keyed output record.
type EmittedBlock struct {
	// Record is a keyed standalone record, or a GroupableRecord for
	// groupable types.
	Record KeyedRecord
	// ExpansionPayload is transient input for an emitted expandable
	// reference, dispatched to its Expander by the host. Never persisted.
	ExpansionPayload json.RawMessage
}

// MarkdownUpdater is implemented by types editable through Markdown.
type MarkdownUpdater interface {
	// UpdateMarkdown returns replacement state for the body without mutating
	// existing. Body-represented fields are replaced; other content and the
	// record metadata are preserved (see WithContent).
	UpdateMarkdown(existing Record, body string) (Record, error)
}

// RevisionPolicy is implemented by types that decide whether an
// inner-content change creates a new block revision or replaces the stored
// record of the latest revision in place (e.g. refreshing a stored data
// snapshot without changing what the block is about). Types without it
// always append revisions. This is an opt-in exception to append-only
// block history: earlier revisions are never rewritten.
//
// The host consults the policy only for changes of inner content values.
// It always appends a revision for status/lifecycle changes and for
// host-owned structural changes (content type, event timestamps, keys,
// individual/collection shape, collection membership and member order),
// and it skips both the policy and the write when the records are
// semantically equal (see EqualRecords). Ownership and validation rules
// are unaffected.
type RevisionPolicy interface {
	// ShouldCreateRevision receives the complete stored record and the
	// complete validated replacement, both of this content type; it must
	// not mutate either. true appends a new revision; false persists next
	// in the latest revision in place, preserving its revision number,
	// author, and creation time. An error rejects the whole mutation.
	ShouldCreateRevision(previous, next Record) (bool, error)
}

// ContentValidator is implemented by types with semantic rules the
// content schema cannot express (e.g. ordering, bounds, URL shapes). The
// registry calls it from ValidateRecord after the schema accepts each inner
// value, so it applies to every host write path (JSON ingestion, Markdown
// edits and creation, expansion output) but not to decoding stored records.
type ContentValidator interface {
	// ValidateContent returns the violations of value, with paths
	// following the inner content JSON layout; nil means valid. It must
	// not mutate value.
	ValidateContent(value Content) []FieldError
}

// EventTimestamper is implemented by types whose event timestamp is derived
// from their content (e.g. an explicit timestamp of a stored snapshot)
// rather than block creation time. The host applies it on
// ordinary public writes (JSON creation/update, Markdown creation/edit)
// after validation and timestamp-echo checks, so producers still cannot
// assign timestamps directly. A changed timestamp is a structural change
// and therefore creates a revision (see RevisionPolicy).
type EventTimestamper interface {
	// EventTimestamp returns the event timestamp for a validated inner
	// value; the zero time requests original block creation time. It must not
	// mutate value.
	EventTimestamp(value Content) (time.Time, error)
}

// ApplyEventTimestamp returns record with its event timestamp derived
// through the type's EventTimestamper, falling back to createdAt when it
// returns zero. Types without this capability retain their timestamp.
// createdAt must be the original block creation time, not revision time.
// Only individual records are supported.
func ApplyEventTimestamp(record Record, createdAt time.Time) (Record, error) {
	timestamper, ok := record.ContentType().(EventTimestamper)
	if !ok {
		return record, nil
	}
	item, ok := record.(ItemRecord)
	if !ok {
		return nil, fmt.Errorf("event timestamp: expected an individual record, got %T", record)
	}
	ts, err := timestamper.EventTimestamp(item.Content())
	if err != nil {
		return nil, fmt.Errorf("event timestamp: %w", err)
	}
	if ts.IsZero() {
		ts = createdAt
	}
	if ts.Equal(record.Timestamp()) {
		return record, nil
	}
	return WithTimestamp(record, ts.UTC())
}

// Presenter is implemented by types providing structured presentation.
// Without it, Markdown is the fallback.
type Presenter interface {
	Present(Record) (Presentation, error)
}

// TypedMarkdownRenderer adapts a typed render function to
// ContentType.RenderMarkdown. View recovers typed record data (e.g.
// ItemView[T], CollectionView[T]) without re-encoding. Embed it into a
// content type implementation.
type TypedMarkdownRenderer[V any] struct {
	View   func(Record) (V, error)
	Render func(V) (string, error)
}

func (r TypedMarkdownRenderer[V]) RenderMarkdown(record Record) (string, error) {
	value, err := r.View(record)
	if err != nil {
		return "", err
	}
	return r.Render(value)
}

// ItemMarkdown returns a TypedMarkdownRenderer for individual records of T.
func ItemMarkdown[T any](render func(Item[T]) (string, error)) TypedMarkdownRenderer[Item[T]] {
	return TypedMarkdownRenderer[Item[T]]{View: ItemView[T], Render: render}
}

// CollectionMarkdown returns a TypedMarkdownRenderer for collections of T.
func CollectionMarkdown[T any](render func(GroupableCollection[T]) (string, error)) TypedMarkdownRenderer[GroupableCollection[T]] {
	return TypedMarkdownRenderer[GroupableCollection[T]]{View: CollectionView[T], Render: render}
}
