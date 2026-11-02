// Package content defines the contracts between the InfraPad host and content
// extensions.
//
// Extensions own inner content values (opaque, JSON-compatible Go values) and
// their schemas. The host owns records: the registered type association,
// event timestamps, generated keys, and grouped membership. Extension schemas
// and decoders never see the host record envelope.
package content

import (
	"fmt"
	"time"
)

// Content is an opaque, JSON-compatible extension-defined Go value. It is not
// a wrapper or marker interface; typed adapters (ItemView, CollectionView, ...)
// recover the concrete Go value.
type Content = any

// Record is the host-owned runtime representation of stored or emitted
// content, carrying its registered type association and event timestamp.
type Record interface {
	ContentType() ContentType
	Timestamp() time.Time
}

// ItemRecord is a record holding exactly one inner content value.
type ItemRecord interface {
	Record
	Content() Content
}

// KeyedRecord is a record with a stable identity, unique within its
// generating primary. Being keyed does not imply being groupable.
type KeyedRecord interface {
	Record
	Key() string
}

// GroupableRecord is a non-empty collection of keyed items of a groupable
// content type. Its Key and Timestamp are those of its first member; the key
// is an anchor, not durable block identity.
type GroupableRecord interface {
	KeyedRecord
	KeyedItems() []KeyedRecord
}

// Item is one timestamped inner content value. It is used for ordinary
// (top-level) standalone blocks. Fields are encoded with ordinary JSON
// encoding; the runtime type association is not serialized.
type Item[T any] struct {
	TimestampValue time.Time `json:"timestamp"`
	ContentValue   T         `json:"content"`
	contentType    ContentType
}

// NewItem constructs an unkeyed record of the given content type.
func NewItem[T any](ct ContentType, ts time.Time, value T) *Item[T] {
	return &Item[T]{TimestampValue: ts, ContentValue: value, contentType: ct}
}

func (i *Item[T]) ContentType() ContentType { return i.contentType }
func (i *Item[T]) Timestamp() time.Time     { return i.TimestampValue }
func (i *Item[T]) Content() Content         { return i.ContentValue }

// KeyedItem is an Item with a stable identity. It is used for generated items
// as well as generated standalone references.
type KeyedItem[T any] struct {
	Item[T]
	KeyValue string `json:"key"`
}

// NewKeyedItem constructs a keyed record of the given content type.
func NewKeyedItem[T any](ct ContentType, key string, ts time.Time, value T) *KeyedItem[T] {
	return &KeyedItem[T]{Item: *NewItem(ct, ts, value), KeyValue: key}
}

func (i *KeyedItem[T]) Key() string { return i.KeyValue }

// GroupableCollection is a non-empty collection of keyed items retained
// within a single block record. Construct it via NewCollection.
type GroupableCollection[T any] struct {
	Items       []KeyedItem[T] `json:"items"`
	contentType ContentType
}

func (c *GroupableCollection[T]) ContentType() ContentType { return c.contentType }

// Key returns the first member's key. It is an anchor, NOT durable block
// identity: removing the first member changes it.
func (c *GroupableCollection[T]) Key() string { return c.Items[0].Key() }

// Timestamp returns the first member's timestamp.
func (c *GroupableCollection[T]) Timestamp() time.Time { return c.Items[0].Timestamp() }

// KeyedItems returns copies of the members; inner content values are
// immutable by convention.
func (c *GroupableCollection[T]) KeyedItems() []KeyedRecord {
	items := make([]KeyedRecord, len(c.Items))
	for n := range c.Items {
		item := c.Items[n]
		items[n] = &item
	}
	return items
}

// NewCollection builds a collection of ct from keyed item records whose inner
// content is of type T. It preserves keys, timestamps, and type associations
// and rejects empty input or incompatible records. Single-member collections
// are valid, as the host may split groups anywhere.
//
// Groupable content types typically implement GroupableContentType.NewCollection
// by delegating to this helper.
func NewCollection[T any](ct ContentType, records []KeyedRecord) (*GroupableCollection[T], error) {
	if len(records) == 0 {
		return nil, fmt.Errorf("%s: collection must not be empty", ct.Name())
	}
	result := &GroupableCollection[T]{contentType: ct, Items: make([]KeyedItem[T], 0, len(records))}
	for n, record := range records {
		if record == nil {
			return nil, fmt.Errorf("%s: collection item %d is nil", ct.Name(), n)
		}
		item, ok := record.(ItemRecord)
		if !ok {
			return nil, fmt.Errorf("%s: collection item %d is not an individual record (%T)", ct.Name(), n, record)
		}
		if record.ContentType() != ct {
			return nil, fmt.Errorf("%s: collection item %d has content type %s", ct.Name(), n, typeName(record.ContentType()))
		}
		if record.Key() == "" {
			return nil, fmt.Errorf("%s: collection item %d has an empty key", ct.Name(), n)
		}
		value, err := ContentValue[T](item.Content())
		if err != nil {
			return nil, fmt.Errorf("%s: collection item %q: %w", ct.Name(), record.Key(), err)
		}
		result.Items = append(result.Items, *NewKeyedItem(ct, record.Key(), record.Timestamp(), value))
	}
	return result, nil
}

// Members returns the keyed constituents of a record: the members of a
// collection, the record itself when keyed, or nil for unkeyed records.
func Members(record Record) []KeyedRecord {
	if collection, ok := record.(GroupableRecord); ok {
		return collection.KeyedItems()
	}
	if keyed, ok := record.(KeyedRecord); ok {
		return []KeyedRecord{keyed}
	}
	return nil
}

func typeName(ct ContentType) string {
	if ct == nil {
		return "<nil>"
	}
	return ct.Name()
}
