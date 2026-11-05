package content

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Inner content codecs and typed views (extension-facing).
// ---------------------------------------------------------------------------

// DecodeContent decodes an inner content JSON value into T. Content types
// typically implement DecodeJSON with it. Schema validation happens before
// in Registry.DecodeContent.
func DecodeContent[T any](data json.RawMessage) (Content, error) {
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}

// DecodePayload decodes a transient expansion payload into T.
func DecodePayload[T any](payload json.RawMessage) (T, error) {
	var value T
	if err := json.Unmarshal(payload, &value); err != nil {
		return value, fmt.Errorf("decode payload: %w", err)
	}
	return value, nil
}

// ContentValue recovers the concrete inner value without re-encoding.
func ContentValue[T any](content Content) (T, error) {
	typed, ok := content.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("unexpected inner content %T, expected %T", content, zero)
	}
	return typed, nil
}

// ItemView returns a typed view of an individual record.
func ItemView[T any](record Record) (Item[T], error) {
	switch r := record.(type) {
	case *Item[T]:
		return *r, nil
	case *KeyedItem[T]:
		return r.Item, nil
	}
	item, ok := record.(ItemRecord)
	if !ok {
		return Item[T]{}, fmt.Errorf("expected an individual record, got %T", record)
	}
	value, err := ContentValue[T](item.Content())
	if err != nil {
		return Item[T]{}, err
	}
	return *NewItem(record.ContentType(), record.Timestamp(), value), nil
}

// KeyedItemView returns a typed view of a keyed individual record.
func KeyedItemView[T any](record Record) (KeyedItem[T], error) {
	keyed, ok := record.(KeyedRecord)
	if !ok {
		return KeyedItem[T]{}, fmt.Errorf("expected a keyed record, got %T", record)
	}
	item, err := ItemView[T](record)
	if err != nil {
		return KeyedItem[T]{}, err
	}
	return KeyedItem[T]{Item: item, KeyValue: keyed.Key()}, nil
}

// CollectionView returns a typed view of a collection record.
func CollectionView[T any](record Record) (GroupableCollection[T], error) {
	if typed, ok := record.(*GroupableCollection[T]); ok {
		return *typed, nil
	}
	collection, ok := record.(GroupableRecord)
	if !ok {
		return GroupableCollection[T]{}, fmt.Errorf("expected a collection record, got %T", record)
	}
	typed, err := NewCollection[T](record.ContentType(), collection.KeyedItems())
	if err != nil {
		return GroupableCollection[T]{}, err
	}
	return *typed, nil
}

// WithContent returns a new individual record replacing the inner value while
// preserving type association, timestamp, and key. existing is not modified.
func WithContent[T any](existing Record, value T) (Record, error) {
	if _, ok := existing.(ItemRecord); !ok {
		return nil, fmt.Errorf("cannot replace individual content on %T", existing)
	}
	if keyed, ok := existing.(KeyedRecord); ok {
		return NewKeyedItem(existing.ContentType(), keyed.Key(), existing.Timestamp(), value), nil
	}
	return NewItem(existing.ContentType(), existing.Timestamp(), value), nil
}

// ---------------------------------------------------------------------------
// Host record codec. Records use ordinary JSON encoding of their fields;
// only inner content is delegated to extension decoders. Keeping all record
// (de)serialization behind these entry points leaves a seam for future
// schema evolution (upgrades must run before validation).
// ---------------------------------------------------------------------------

// checkShape validates host-owned record shape and metadata, independently
// of CUE validation of inner content: a registered-looking type association,
// non-zero timestamps, non-empty unique keys, and a shape matching the type's
// grouping capability (groupable types are always non-empty collections).
// Failures are record-stage *ValidationError values.
func checkShape(record Record) error {
	if record == nil {
		return recordError(nil, "record is nil")
	}
	ct := record.ContentType()
	if ct == nil {
		return recordError(nil, "record %T has no content type", record)
	}
	if _, groupable := ct.(GroupableContentType); groupable {
		collection, ok := record.(GroupableRecord)
		if !ok {
			return recordError(ct, "groupable type requires a collection record, got %T", record)
		}
		items := collection.KeyedItems()
		if len(items) == 0 {
			return recordError(ct, "collection must not be empty")
		}
		seen := make(map[string]bool, len(items))
		for n, item := range items {
			if err := checkItem(ct, item); err != nil {
				return locateError(err, atMember(n, item.Key()))
			}
			if item.ContentType() != ct {
				return locateError(recordError(ct, "collection member has content type %s", typeName(item.ContentType())), atMember(n, item.Key()))
			}
			if seen[item.Key()] {
				return locateError(recordError(ct, "duplicate collection member key %q", item.Key()), atMember(n, item.Key()))
			}
			seen[item.Key()] = true
		}
		return nil
	}
	if _, isCollection := record.(GroupableRecord); isCollection {
		return recordError(ct, "non-groupable type cannot be stored as a collection")
	}
	return checkItem(ct, record)
}

func checkItem(ct ContentType, record Record) error {
	if _, ok := record.(ItemRecord); !ok {
		return recordError(ct, "expected an individual record, got %T", record)
	}
	if record.Timestamp().IsZero() {
		return recordError(ct, "record requires a timestamp")
	}
	if keyed, ok := record.(KeyedRecord); ok && keyed.Key() == "" {
		return recordError(ct, "keyed record requires a non-empty key")
	}
	return nil
}

// EncodeRecord serializes a host record after checking its shape.
func EncodeRecord(record Record) (json.RawMessage, error) {
	if err := checkShape(record); err != nil {
		return nil, err
	}
	return json.Marshal(record)
}

// wireItem mirrors the serialized Item/KeyedItem fields with raw content.
type wireItem struct {
	TimestampValue time.Time       `json:"timestamp"`
	ContentValue   json.RawMessage `json:"content"`
	KeyValue       *string         `json:"key,omitempty"`
}

type wireCollection struct {
	Items []wireItem `json:"items"`
}

// DecodeRecord restores a host record of the named registered type,
// delegating only inner content to the extension's DecodeJSON. Each inner
// value is validated against its content schema (see DecodeContent); the
// envelope and metadata are checked separately as host record shape.
func (r *Registry) DecodeRecord(name string, data json.RawMessage) (Record, error) {
	ct, err := r.Lookup(name)
	if err != nil {
		return nil, err
	}
	var record Record
	if groupable, ok := ct.(GroupableContentType); ok {
		var wire wireCollection
		if err := decodeStrict(data, &wire); err != nil {
			return nil, recordError(ct, "decode collection record: %w", err)
		}
		if len(wire.Items) == 0 {
			return nil, recordError(ct, "collection must not be empty")
		}
		items := make([]KeyedRecord, 0, len(wire.Items))
		for n, w := range wire.Items {
			if w.KeyValue == nil {
				return nil, locateError(recordError(ct, "collection member requires a key"), atMember(n, ""))
			}
			item, err := r.decodeItem(ct, w)
			if err != nil {
				return nil, locateError(err, atMember(n, *w.KeyValue))
			}
			items = append(items, item.(KeyedRecord))
		}
		if record, err = groupable.NewCollection(items); err != nil {
			return nil, err
		}
	} else {
		var wire wireItem
		if err := decodeStrict(data, &wire); err != nil {
			return nil, recordError(ct, "decode record: %w", err)
		}
		if record, err = r.decodeItem(ct, wire); err != nil {
			return nil, err
		}
	}
	if err := checkShape(record); err != nil {
		return nil, err
	}
	return record, nil
}

func (r *Registry) decodeItem(ct ContentType, wire wireItem) (Record, error) {
	if len(wire.ContentValue) == 0 {
		return nil, recordError(ct, "record requires content")
	}
	value, err := r.DecodeContent(ct.Name(), wire.ContentValue)
	if err != nil {
		return nil, err
	}
	if wire.KeyValue != nil {
		return NewKeyedItem(ct, *wire.KeyValue, wire.TimestampValue, value), nil
	}
	return NewItem(ct, wire.TimestampValue, value), nil
}

// RecordInput is the writable part of a public record envelope submitted
// through ordinary block writes: the inner content value and an optional
// timestamp echo. Host-owned metadata (generated keys, collection
// membership) has no representation here.
type RecordInput struct {
	// Timestamp is the optional submitted timestamp echo; nil when omitted.
	Timestamp *time.Time
	// Content is the raw inner content JSON (nil when omitted).
	Content json.RawMessage
}

// wireRecordInput mirrors the standalone record envelope; key and items
// are recognized only to reject them explicitly.
type wireRecordInput struct {
	Timestamp *time.Time      `json:"timestamp,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	Key       *string         `json:"key,omitempty"`
	Items     json.RawMessage `json:"items,omitempty"`
}

// DecodeRecordInput decodes a public record envelope submitted through
// ordinary writes ({"content": …} plus an optional "timestamp" echo). It
// rejects generated keys, collection membership, and unknown fields as
// record-stage *ValidationError values; content validation and timestamp
// echo rules remain with the host write path.
func DecodeRecordInput(blockType string, data json.RawMessage) (RecordInput, error) {
	inputError := func(format string, args ...any) error {
		return &ValidationError{ContentType: blockType, Stage: StageRecord, Err: fmt.Errorf(format, args...)}
	}
	var wire wireRecordInput
	if err := decodeStrict(data, &wire); err != nil {
		return RecordInput{}, inputError("decode record: %w", err)
	}
	if wire.Key != nil {
		return RecordInput{}, inputError("record key: generated keys cannot be assigned through public input")
	}
	if wire.Items != nil {
		return RecordInput{}, inputError("record items: collection membership cannot be assigned through public input")
	}
	return RecordInput{Timestamp: wire.Timestamp, Content: wire.Content}, nil
}

func decodeStrict(data []byte, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	if decoder.More() {
		return fmt.Errorf("unexpected trailing data")
	}
	return nil
}
