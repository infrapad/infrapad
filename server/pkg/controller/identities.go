package controller

// Generated-block identities: record-preserving internal writes and the
// pure membership matcher consumed by snapshot reconciliation
// (reconciliation.go).
//
// A generated constituent's identity is its document, its immediate
// generating parent block, and its opaque non-empty key. Keys are unique
// across the parent's complete live direct output, including different
// content types and separate groups; different parents (including at
// different nesting levels) may reuse them. Matching additionally requires
// the same content type: a key changing content type does not retain its
// old block number.
//
// The write helpers here take validated content.Record values (see
// Registry.ValidateRecord), never inner-content submissions: keys, member
// order, event timestamps, and inner values survive writes unchanged.
// Ordinary public writes keep submitting inner content only.

import (
	"context"
	"fmt"
	"sort"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store"
)

// AppendGeneratedRevisionTx appends a new revision of an existing live
// generated block from a validated record, preserving the immutable parent
// relationship, content type, and (for standalone references) key.
// Collection membership and the derived anchor may change freely while the
// block number stays stable. New member keys must not collide with the
// keys of the parent's other live children. Callers compose this with the
// other transaction-scoped host helpers inside one BeginHostTx
// transaction; rejected operations leave the transaction untouched for
// rollback.
func (c *Controller) AppendGeneratedRevisionTx(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, blockNumber model.BlockNumber, record content.Record, author model.AuthorID) (model.Block, error) {
	if err := checkGeneratedRecord(record); err != nil {
		return model.Block{}, err
	}

	latest, err := tx.Blocks().Get(ctx, documentUID, blockNumber, 0)
	if err != nil {
		return model.Block{}, fmt.Errorf("get latest block for generated revision: %w", err)
	}
	if latest.Status == model.DeletedBlock {
		return model.Block{}, invalidInput("block %d: already deleted", blockNumber)
	}
	if latest.ParentBlockNumber == nil {
		return model.Block{}, invalidInput("block %d: not a generated block", blockNumber)
	}
	if err := c.deserializeRecord(&latest); err != nil {
		return model.Block{}, err
	}
	if record.ContentType().Name() != latest.Type {
		return model.Block{}, invalidInput("block %d: content type %q cannot be changed to %q", blockNumber, latest.Type, record.ContentType().Name())
	}
	if err := checkIdentityChange(latest.Record, record, blockNumber); err != nil {
		return model.Block{}, err
	}
	if err := c.registry.ValidateRecord(record); err != nil {
		return model.Block{}, fmt.Errorf("block %d: %w", blockNumber, err)
	}
	if err := c.checkSiblingKeys(ctx, tx, documentUID, *latest.ParentBlockNumber, record, blockNumber); err != nil {
		return model.Block{}, err
	}

	return storeRecordRevision(ctx, tx, documentUID, latest, record, author)
}

// storeRecordRevision appends a revision of latest carrying record,
// preserving the block's status, type, and immutable parent. It performs
// no validation: callers check the record and identity first.
func storeRecordRevision(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, latest model.Block, record content.Record, author model.AuthorID) (model.Block, error) {
	blk := latest
	blk.Record = record
	if err := serializeRecord(&blk); err != nil {
		return model.Block{}, err
	}
	rev, err := appendRevision(ctx, tx, documentUID, blk, latest.Status, author)
	if err != nil {
		return model.Block{}, err
	}
	rev.Record = record
	return rev, nil
}

// MembershipMatch pairs one live generated block with the desired record
// whose membership it retains.
type MembershipMatch struct {
	BlockNumber model.BlockNumber
	Record      content.Record
}

// MembershipResult is the outcome of MatchGeneratedOutput: the matches,
// the desired records no old block retains, and the old blocks that lost
// or found no nomination.
type MembershipResult struct {
	Matches          []MembershipMatch
	UnmatchedRecords []content.Record
	UnmatchedBlocks  []model.Block
}

// MatchGeneratedOutput matches live generated blocks against already-formed
// desired records (collections or keyed standalone references; a standalone
// reference counts as a one-member record). The caller must supply only the
// latest direct children of one immediate parent in one document, with
// records deserialized (as returned by childBlocks). Desired records must
// belong to that same parent. It is side-effect-free: no writes, no block
// allocation, no ordering or grouping decisions — the caller supplies the
// desired groups and consumes the result.
//
// For each old block (in ascending block-number order), the first member
// in persisted order whose key survives in desired output of the same
// content type nominates that desired record. When several old blocks
// nominate the same desired record, the lower existing block number wins;
// losers do not fall back to another desired record. Deleted blocks are
// not candidates, and records of different content types never match.
// Every old block and desired record participates in at most one match.
func MatchGeneratedOutput(blocks []model.Block, desired []content.Record) MembershipResult {
	type typeKey struct {
		ct  content.ContentType
		key string
	}
	index := map[typeKey]int{}
	for n, record := range desired {
		for _, member := range content.Members(record) {
			k := typeKey{member.ContentType(), member.Key()}
			if _, seen := index[k]; !seen {
				index[k] = n
			}
		}
	}

	claimed := make([]bool, len(desired))
	var result MembershipResult
	sorted := append([]model.Block(nil), blocks...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].BlockNumber < sorted[j].BlockNumber })
	for _, blk := range sorted {
		if blk.Status == model.DeletedBlock {
			continue
		}
		nominated := -1
		for _, member := range content.Members(blk.Record) {
			if n, ok := index[typeKey{member.ContentType(), member.Key()}]; ok {
				nominated = n
				break
			}
		}
		if nominated < 0 || claimed[nominated] {
			result.UnmatchedBlocks = append(result.UnmatchedBlocks, blk)
			continue
		}
		claimed[nominated] = true
		result.Matches = append(result.Matches, MembershipMatch{BlockNumber: blk.BlockNumber, Record: desired[nominated]})
	}
	for n, record := range desired {
		if !claimed[n] {
			result.UnmatchedRecords = append(result.UnmatchedRecords, record)
		}
	}
	return result
}

// checkGeneratedRecord rejects records that cannot carry a generated
// identity: they must be keyed with a non-empty key (collection anchors
// derive theirs from the first member, checked by record validation).
func checkGeneratedRecord(record content.Record) error {
	if record == nil {
		return invalidInput("generated record is nil")
	}
	if keyed, ok := record.(content.KeyedRecord); !ok || keyed.Key() == "" {
		name := "<nil>"
		if ct := record.ContentType(); ct != nil {
			name = ct.Name()
		}
		return invalidInput("%s: generated record requires a non-empty key", name)
	}
	return nil
}

// checkIdentityChange rejects illegal identity changes on a revision: the
// content type is checked by the caller, and here a standalone keyed
// reference must keep its key (its key is its whole membership; a changed
// key is a different identity and belongs in a new block). Collections
// may change membership, including the derived anchor.
func checkIdentityChange(existing, updated content.Record, blockNumber model.BlockNumber) error {
	_, existingCollection := existing.(content.GroupableRecord)
	_, updatedCollection := updated.(content.GroupableRecord)
	if existingCollection || updatedCollection {
		if existingCollection != updatedCollection {
			return invalidInput("block %d: record shape cannot change between individual and collection", blockNumber)
		}
		return nil
	}
	old, ok := existing.(content.KeyedRecord)
	if !ok {
		return invalidInput("block %d: existing record is not keyed", blockNumber)
	}
	if keyed, ok := updated.(content.KeyedRecord); !ok || keyed.Key() != old.Key() {
		return invalidInput("block %d: generated reference key %q cannot change", blockNumber, old.Key())
	}
	return nil
}

// checkSiblingKeys rejects records whose member keys collide with the
// live direct output of parentBlockNumber, keeping keys unique across a
// parent's complete live output (any content type, separate groups
// included). excludeBlock exempts the block being revised itself. Deleted
// blocks are not part of the live output: a returning key receives a new
// block unless it joins a matching live group.
func (c *Controller) checkSiblingKeys(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, parentBlockNumber model.BlockNumber, record content.Record, excludeBlock model.BlockNumber) error {
	desired := map[string]bool{}
	for _, member := range content.Members(record) {
		desired[member.Key()] = true
	}
	if len(desired) == 0 {
		return nil
	}

	children, err := c.childBlocks(ctx, tx, documentUID, parentBlockNumber)
	if err != nil {
		return err
	}
	for _, blk := range children {
		if blk.BlockNumber == excludeBlock || blk.Status == model.DeletedBlock {
			continue
		}
		for _, member := range content.Members(blk.Record) {
			if desired[member.Key()] {
				return invalidInput("generated key %q already used by live block %d of parent %d", member.Key(), blk.BlockNumber, parentBlockNumber)
			}
		}
	}
	return nil
}
