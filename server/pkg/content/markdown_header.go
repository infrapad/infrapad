package content

import (
	"encoding/json"
	"fmt"
)

// MarkdownFields are the structured fields of a record exposed in the
// host-managed Markdown header, i.e. the mapping nested under the host's
// `content:` key. Values use the JSON-compatible content data model
// (map[string]any, []any, string, float64, bool, nil). Nil or empty fields
// mean no header.
type MarkdownFields map[string]any

// MarkdownHeader is implemented by types exposing structured fields in the
// host-managed Markdown header, complementary to their rendered body.
// Types without it have no extension header. The host owns directives,
// YAML serialization, and framing.
//
// Header presence never grants editing authority: generated blocks stay
// protected by the host ownership policy regardless of this capability.
type MarkdownHeader interface {
	// MarkdownHeader projects the whole record (including collections)
	// into header fields. It must not mutate existing.
	MarkdownHeader(existing Record) (MarkdownFields, error)
	// UpdateMarkdownHeader returns replacement state for the header with
	// replacement (not patch) semantics: omitted optional header fields
	// are removed, while body-owned fields and host metadata are
	// preserved. It must mutate neither existing nor fields.
	UpdateMarkdownHeader(existing Record, fields MarkdownFields) (Record, error)
}

// TypedMarkdownHeader adapts a typed header projection H of individual
// records with inner content T to MarkdownHeader. H is an ordinary
// JSON-encoded Go struct: `omitempty` fields are omitted from the header,
// and header updates are decoded strictly (unknown fields are rejected).
// Embed it into a content type implementation.
type TypedMarkdownHeader[T, H any] struct {
	// Project returns the header projection of the content.
	Project func(T) H
	// Apply returns the content with the header replaced, preserving
	// body-owned fields.
	Apply func(existing T, header H) T
}

// ItemMarkdownHeader returns a TypedMarkdownHeader for individual records
// of T with header projection H.
func ItemMarkdownHeader[T, H any](project func(T) H, apply func(T, H) T) TypedMarkdownHeader[T, H] {
	return TypedMarkdownHeader[T, H]{Project: project, Apply: apply}
}

func (h TypedMarkdownHeader[T, H]) MarkdownHeader(existing Record) (MarkdownFields, error) {
	item, err := ItemView[T](existing)
	if err != nil {
		return nil, err
	}
	return EncodeMarkdownFields(h.Project(item.ContentValue))
}

func (h TypedMarkdownHeader[T, H]) UpdateMarkdownHeader(existing Record, fields MarkdownFields) (Record, error) {
	item, err := ItemView[T](existing)
	if err != nil {
		return nil, err
	}
	header, err := DecodeMarkdownFields[H](fields)
	if err != nil {
		return nil, &ValidationError{ContentType: typeName(existing.ContentType()), Stage: StageContent, Err: err}
	}
	return WithContent(existing, h.Apply(item.ContentValue, header))
}

// EncodeMarkdownFields converts a JSON-encodable value (usually a header
// struct) into fresh MarkdownFields of the JSON-compatible data model. It
// returns nil when the value encodes to an empty or null object.
func EncodeMarkdownFields(value any) (MarkdownFields, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode Markdown header: %w", err)
	}
	var fields MarkdownFields
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("encode Markdown header: expected an object: %w", err)
	}
	if len(fields) == 0 {
		return nil, nil
	}
	return fields, nil
}

// DecodeMarkdownFields strictly decodes header fields into H, rejecting
// unknown fields. Nil fields decode into the zero value.
func DecodeMarkdownFields[H any](fields MarkdownFields) (H, error) {
	var header H
	if fields == nil {
		fields = MarkdownFields{}
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return header, fmt.Errorf("decode Markdown header: %w", err)
	}
	if err := decodeStrict(data, &header); err != nil {
		return header, fmt.Errorf("decode Markdown header: %w", err)
	}
	return header, nil
}
