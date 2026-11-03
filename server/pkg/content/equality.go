package content

// Semantic record equality: used by the host to suppress no-op revisions.

import (
	"encoding/json"
	"reflect"
)

// EqualRecords reports whether two records are semantically equal as
// persisted host records: the same content type name and shape
// (individual vs collection), the same keys, event timestamps (compared
// as instants), and inner content values, with collection members in the
// same order. Inner values are compared through their JSON
// representation, ignoring object-key ordering. Revision bookkeeping is
// not part of a record and therefore never considered.
func EqualRecords(a, b Record) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if typeName(a.ContentType()) != typeName(b.ContentType()) {
		return false
	}
	_, aCollection := a.(GroupableRecord)
	_, bCollection := b.(GroupableRecord)
	if aCollection != bCollection {
		return false
	}
	if aCollection {
		aItems, bItems := a.(GroupableRecord).KeyedItems(), b.(GroupableRecord).KeyedItems()
		if len(aItems) != len(bItems) {
			return false
		}
		for n := range aItems {
			if !equalItems(aItems[n], bItems[n]) {
				return false
			}
		}
		return true
	}
	return equalItems(a, b)
}

func equalItems(a, b Record) bool {
	aKeyed, aOK := a.(KeyedRecord)
	bKeyed, bOK := b.(KeyedRecord)
	if aOK != bOK || (aOK && aKeyed.Key() != bKeyed.Key()) {
		return false
	}
	if !a.Timestamp().Equal(b.Timestamp()) {
		return false
	}
	aItem, aOK := a.(ItemRecord)
	bItem, bOK := b.(ItemRecord)
	if !aOK || !bOK {
		return false
	}
	return equalJSON(aItem.Content(), bItem.Content())
}

func equalJSON(a, b Content) bool {
	aData, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bData, err := json.Marshal(b)
	if err != nil {
		return false
	}
	var aValue, bValue any
	if json.Unmarshal(aData, &aValue) != nil || json.Unmarshal(bData, &bValue) != nil {
		return false
	}
	return reflect.DeepEqual(aValue, bValue)
}
