package content

import (
	"context"
	"encoding/json"
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

// Schema references a (generated) CUE artifact and the definition selector
// within it, e.g. Schema{Source: slackCUE, Definition: "#Message"}.
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
