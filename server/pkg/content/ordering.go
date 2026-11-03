package content

// Member ordering: a side-effect-free primitive for chronological member
// views. Snapshot reconciliation (controller) applies the same rule within
// a parent, extended document-wide with the owning primary's block number;
// it never reorders persisted collections (the identity matcher keeps
// consuming the old persisted member order).

import "sort"

// OrderKeyed returns a new slice holding records ordered by event
// timestamp ascending, then key lexicographically. The input slice is not
// modified.
func OrderKeyed(records []KeyedRecord) []KeyedRecord {
	ordered := append([]KeyedRecord(nil), records...)
	sort.SliceStable(ordered, func(i, j int) bool {
		ti, tj := ordered[i].Timestamp(), ordered[j].Timestamp()
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return ordered[i].Key() < ordered[j].Key()
	})
	return ordered
}

// OrderMembers returns the keyed members of record (see Members) ordered
// by event timestamp ascending, then key lexicographically, as a new
// slice. The source record is left unchanged: a collection keeps its
// persisted member order in place, and the returned members are the same
// record values (inner content is immutable by convention).
func OrderMembers(record Record) []KeyedRecord {
	return OrderKeyed(Members(record))
}
