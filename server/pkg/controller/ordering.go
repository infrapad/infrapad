package controller

// Chronological ordering of persisted block views.
//
// Document-facing latest-block views are ordered by record event time
// ascending, then block number ascending; unique block numbers resolve
// ties. The rule is applied here in the controller, after record decoding,
// so GetDocument and ListBlocks share it and clients receive the intended
// order regardless of the store's row order. The store stays
// content-type agnostic: it never interprets record envelopes for
// ordering.
//
// What this deliberately does not do: renumber blocks, change deletion
// visibility, reorder revision history (which stays ordered by revision
// number), or reorder persisted collection membership — the identity
// matcher keeps consuming the old persisted member order.

import (
	"sort"
	"time"

	"github.com/infrapad/infrapad/server/pkg/model"
)

// sortBlocksChronologically sorts latest-revision block views in place by
// record event timestamp ascending, then block number ascending. Blocks
// without a decoded record (not expected on these paths) sort first, keep
// their relative order, and never block the sort.
func sortBlocksChronologically(blocks []model.Block) {
	sort.SliceStable(blocks, func(i, j int) bool {
		ti, tj := blockEventTime(blocks[i]), blockEventTime(blocks[j])
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return blocks[i].BlockNumber < blocks[j].BlockNumber
	})
}

// blockEventTime returns the block's record event time, or the zero time
// when no record is decoded.
func blockEventTime(blk model.Block) time.Time {
	if blk.Record == nil {
		return time.Time{}
	}
	return blk.Record.Timestamp()
}
