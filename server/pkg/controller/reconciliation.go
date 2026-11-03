package controller

// Snapshot reconciliation: applying one validated expansion result for an
// existing primary to the document.
//
// The primary's previous live direct output is replaced by its complete
// desired output, and the whole document is re-planned from persisted
// membership (other primaries' payloads are never needed and their
// extensions never run):
//
//  1. Planning events. Every live block contributes events; generated
//     groupable collections are flattened into their members, ordinary
//     blocks and standalone references stay individual. Events are ordered
//     by event timestamp, then the owning immediate primary's block number
//     (ordinary blocks use their own number), then member key. New output
//     needs no provisional block numbers.
//  2. Grouping. Only adjacent groupable members with the same immediate
//     parent and content type form a group; emitted collection boundaries
//     are ignored. Each member carries its origin (the live block currently
//     holding its key, or none for new members); a group never contains
//     members of two different origins, so surviving old boundaries are
//     preserved even when separating content disappears. New members
//     extend the preceding compatible group (or prepend to the following
//     one when no group precedes them).
//  3. Matching, per immediate parent, through MatchGeneratedOutput.
//     Unmatched old blocks are retired with cascading deletion; their
//     descendants are excluded from planning, which is repeated until the
//     retired set is stable.
//  4. Application of the validated final plan: retirements, record
//     revisions (only for semantically changed records), then creations
//     in planner order. Sibling-key uniqueness is checked against the
//     complete final plan, so membership may move between blocks without
//     intermediate revisions.
//
// Final persisted block views keep their own ordering (timestamp, then
// block number); planning order only decides grouping and allocation.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store"
)

// ReconcileResult is the persisted outcome of ReconcileExpansionTx.
type ReconcileResult struct {
	// Primary is the latest revision of the primary block (unchanged when
	// its record did not change), with its record deserialized.
	Primary model.Block
	// Output maps every key of the desired direct output (collection
	// members included) to the block now holding it.
	Output map[string]model.BlockNumber
	// Expansions lists the emitted expandable references in emission
	// order, including unchanged ones, with their transient payloads for
	// downstream processing. Payloads are never persisted.
	Expansions []PendingExpansion
	// Revisions lists every appended revision (retirements, record
	// revisions, and creations) in application order. Empty for an
	// identical snapshot.
	Revisions []model.Block
}

// PendingExpansion is an emitted expandable reference handed to the
// cascading caller.
type PendingExpansion struct {
	BlockNumber model.BlockNumber
	Record      content.KeyedRecord
	// Payload is the transient ExpansionPayload supplied by the emitting
	// expansion (possibly empty).
	Payload json.RawMessage
}

// ReconcileExpansionTx reconciles an already validated expansion result
// (see Registry.ValidateExpansion) for the existing live primary block
// inside a document-locked transaction obtained from BeginHostTx. The
// caller owns Commit/Rollback; any error requires rolling back the whole
// transaction. It neither runs expanders nor recurses into emitted
// references: those are returned in ReconcileResult.Expansions.
func (c *Controller) ReconcileExpansionTx(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, primaryBlockNumber model.BlockNumber, result content.ExpansionResult, author model.AuthorID) (ReconcileResult, error) {
	latest, err := c.latestBlocks(ctx, tx, documentUID)
	if err != nil {
		return ReconcileResult{}, err
	}
	live, err := c.liveBlocks(latest)
	if err != nil {
		return ReconcileResult{}, err
	}
	primary, ok := live[primaryBlockNumber]
	if !ok {
		if _, exists := latest[primaryBlockNumber]; !exists {
			return ReconcileResult{}, fmt.Errorf("reconcile block %d: %w", primaryBlockNumber, errNotFound)
		}
		return ReconcileResult{}, invalidInput("reconcile block %d: primary is deleted", primaryBlockNumber)
	}
	if err := checkPrimaryResult(primary, result); err != nil {
		return ReconcileResult{}, err
	}

	// Plan until the retired set is stable: retiring a block excludes its
	// descendants, which may in turn change grouping elsewhere.
	excluded := map[model.BlockNumber]bool{}
	var p *reconcilePlan
	for iteration := 0; ; iteration++ {
		if p, err = planReconciliation(live, primary, result, excluded); err != nil {
			return ReconcileResult{}, err
		}
		next := map[model.BlockNumber]bool{}
		for _, blk := range p.retired {
			for _, descendant := range liveDescendants(latest, blk.BlockNumber) {
				next[descendant.BlockNumber] = true
			}
		}
		if sameBlockSet(next, excluded) {
			break
		}
		if iteration > len(live) {
			return ReconcileResult{}, fmt.Errorf("reconcile block %d: planning did not converge", primaryBlockNumber)
		}
		excluded = next
	}

	if err := c.validatePlan(p); err != nil {
		return ReconcileResult{}, err
	}
	return c.applyPlan(ctx, tx, documentUID, p, result, author)
}

// ---------------------------------------------------------------------------
// Planning
// ---------------------------------------------------------------------------

// planEvent is one entry of the document-wide planning stream.
type planEvent struct {
	ts    time.Time
	owner model.BlockNumber
	key   string

	// parent is the immediate parent for generated events; 0 for ordinary
	// blocks, which only act as barriers.
	parent model.BlockNumber
	// record is the generated member or standalone record.
	record content.KeyedRecord
	// groupable is set for members of groupable types.
	groupable content.GroupableContentType
	// origin is the live block currently holding the member; 0 for new
	// members.
	origin model.BlockNumber
}

// plannedRecord is one desired record of a parent, at the stream position
// of its first member.
type plannedRecord struct {
	parent model.BlockNumber
	record content.Record
	pos    int
}

type plannedRevision struct {
	block  model.Block
	record content.Record
}

// reconcilePlan is the complete final mutation plan.
type reconcilePlan struct {
	primary        model.Block
	primaryRecord  content.Record
	primaryChanged bool

	retired   []model.Block
	revisions []plannedRevision
	creations []plannedRecord
	// final holds the complete final live direct output per planned
	// parent, for sibling-key validation.
	final map[model.BlockNumber][]content.Record
	// matched maps (parent, anchor key) of matched desired records to the
	// retaining block.
	matched map[parentKey]model.BlockNumber
}

type parentKey struct {
	parent model.BlockNumber
	key    string
}

// planReconciliation computes one planning pass from the live document,
// ignoring excluded blocks (descendants of retired output). It only builds
// a plan: the caller repeats planning if retirements change descendant
// exclusions, then validates the stable plan before applying any writes.
func planReconciliation(live map[model.BlockNumber]model.Block, primary model.Block, result content.ExpansionResult, excluded map[model.BlockNumber]bool) (*reconcilePlan, error) {
	target := primary.BlockNumber
	// Group across the whole document first: even another primary's output
	// can split a collection. No block identities are assigned at this stage.
	events, oldChildren := buildPlanningInput(live, primary, result, excluded)
	desired, err := groupEvents(events)
	if err != nil {
		return nil, err
	}

	// Identity matching is parent-scoped, unlike grouping: identical keys
	// from different primaries must never compete for the same old block.
	byParent := map[model.BlockNumber][]plannedRecord{}
	for _, d := range desired {
		byParent[d.parent] = append(byParent[d.parent], d)
	}
	// Include both sides. A parent with only desired output needs creations;
	// one with only old output needs retirements, even with no desired groups.
	parents := map[model.BlockNumber]bool{}
	for parent := range byParent {
		parents[parent] = true
	}
	for parent := range oldChildren {
		parents[parent] = true
	}
	ordered := make([]model.BlockNumber, 0, len(parents))
	for parent := range parents {
		ordered = append(ordered, parent)
	}
	// Stable parent traversal makes revision planning deterministic. New
	// block allocation follows global event positions instead (sorted below).
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })

	// The primary's record change is independent of its output changes:
	// either may be a no-op while the other still requires writes.
	p := &reconcilePlan{
		primary:        primary,
		primaryRecord:  result.Record,
		primaryChanged: !content.EqualRecords(primary.Record, result.Record),
		final:          map[model.BlockNumber][]content.Record{},
		matched:        map[parentKey]model.BlockNumber{},
	}
	for _, parent := range ordered {
		records := byParent[parent]
		// The matcher consumes bare records. Keep their stream positions
		// separately for any creations it returns. Anchors uniquely index
		// desired groups here; they do not determine retained block identity.
		positions := map[string]int{}
		desiredRecords := make([]content.Record, len(records))
		for n, d := range records {
			desiredRecords[n] = d.record
			positions[anchorKey(d.record)] = d.pos
		}
		// Each old block nominates the group containing its earliest surviving
		// member in persisted order. Lower block number wins competing
		// nominations; losers retire without trying another group.
		match := MatchGeneratedOutput(oldChildren[parent], desiredRecords)
		old := map[model.BlockNumber]model.Block{}
		for _, blk := range oldChildren[parent] {
			old[blk.BlockNumber] = blk
		}
		for _, m := range match.Matches {
			// Record every retained identity for the output handoff, including
			// unchanged blocks, but append revisions only for semantic changes.
			p.matched[parentKey{parent, anchorKey(m.Record)}] = m.BlockNumber
			if blk := old[m.BlockNumber]; !content.EqualRecords(blk.Record, m.Record) {
				p.revisions = append(p.revisions, plannedRevision{block: blk, record: m.Record})
			}
		}
		for _, record := range match.UnmatchedRecords {
			p.creations = append(p.creations, plannedRecord{parent: parent, record: record, pos: positions[anchorKey(record)]})
		}
		p.retired = append(p.retired, match.UnmatchedBlocks...)
		// Validation needs the complete final sibling set, not just changes:
		// new or transferred keys must not collide with unchanged output.
		p.final[parent] = desiredRecords
	}
	// Allocate new blocks in document-wide planner order, not parent order,
	// without reserving provisional block numbers during planning.
	sort.SliceStable(p.creations, func(i, j int) bool { return p.creations[i].pos < p.creations[j].pos })
	sort.Slice(p.retired, func(i, j int) bool { return p.retired[i].BlockNumber < p.retired[j].BlockNumber })
	// Represent an empty snapshot explicitly, even if the target had no
	// previous output and therefore never appeared in either parent set.
	if _, ok := p.final[target]; !ok {
		p.final[target] = nil
	}
	return p, nil
}

// buildPlanningInput builds the document-wide event stream and
// indexes old live children per parent for matching. The target's previous
// live direct output is replaced by its desired output; every other live,
// non-excluded block contributes its persisted members.
func buildPlanningInput(live map[model.BlockNumber]model.Block, primary model.Block, result content.ExpansionResult, excluded map[model.BlockNumber]bool) ([]planEvent, map[model.BlockNumber][]model.Block) {
	target := primary.BlockNumber
	oldChildren := map[model.BlockNumber][]model.Block{}
	// origins indexes the live holder of every generated member by
	// parent, content type, and key.
	type originKey struct {
		parent model.BlockNumber
		ct     string
		key    string
	}
	origins := map[originKey]model.BlockNumber{}

	numbers := make([]model.BlockNumber, 0, len(live))
	for number := range live {
		if !excluded[number] {
			numbers = append(numbers, number)
		}
	}
	sort.Slice(numbers, func(i, j int) bool { return numbers[i] < numbers[j] })

	var events []planEvent
	for _, number := range numbers {
		blk := live[number]
		if number == target {
			// The primary participates with its resulting record.
			blk.Record = result.Record
		}
		if blk.ParentBlockNumber == nil {
			events = append(events, planEvent{ts: blk.Record.Timestamp(), owner: number})
			continue
		}
		parent := *blk.ParentBlockNumber
		oldChildren[parent] = append(oldChildren[parent], blk)
		for _, member := range content.Members(blk.Record) {
			origins[originKey{parent, blk.Type, member.Key()}] = number
		}
		if parent == target {
			continue // replaced by the desired output below
		}
		events = append(events, generatedEvents(parent, blk.Record)...)
	}
	for _, block := range result.Blocks {
		events = append(events, generatedEvents(target, block.Record)...)
	}
	for n := range events {
		e := &events[n]
		if e.record != nil {
			e.origin = origins[originKey{e.parent, e.record.ContentType().Name(), e.key}]
		}
	}
	return events, oldChildren
}

// generatedEvents flattens a generated record of parent into events.
// Origins are filled in by the caller from the old live output.
func generatedEvents(parent model.BlockNumber, record content.Record) []planEvent {
	if groupable, ok := record.ContentType().(content.GroupableContentType); ok {
		var events []planEvent
		for _, member := range content.Members(record) {
			events = append(events, planEvent{
				ts: member.Timestamp(), owner: parent, key: member.Key(),
				parent: parent, record: member, groupable: groupable,
			})
		}
		return events
	}
	keyed, _ := record.(content.KeyedRecord)
	key := ""
	if keyed != nil {
		key = keyed.Key()
	}
	return []planEvent{{ts: record.Timestamp(), owner: parent, key: key, parent: parent, record: keyed}}
}

// groupEvents sorts events in place and groups adjacent compatible events
// into desired records. It neither matches block identities nor writes.
func groupEvents(events []planEvent) ([]plannedRecord, error) {
	// Chronological planning order defines which members are adjacent.
	// Preserve construction order for equal timestamp/owner/key tuples:
	// buildPlanningInput traverses existing blocks by block number and
	// desired output in emission order.
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if !a.ts.Equal(b.ts) {
			return a.ts.Before(b.ts)
		}
		if a.owner != b.owner {
			return a.owner < b.owner
		}
		return a.key < b.key
	})

	// Keep at most one open group. Its non-zero origin identifies the old
	// block supplying its surviving members; zero means all members so far
	// are new. A group may contain new members, but never survivors from
	// two different old blocks.
	type group struct {
		parent  model.BlockNumber
		ct      content.GroupableContentType
		origin  model.BlockNumber
		members []content.KeyedRecord
		pos     int
	}
	var desired []plannedRecord
	var current *group
	// Materialize a completed run as a collection. Retain its first event's
	// position so unmatched groups can later be allocated in planner order.
	flush := func() error {
		if current == nil {
			return nil
		}
		record, err := current.ct.NewCollection(current.members)
		if err != nil {
			return fmt.Errorf("group %s under block %d: %w", current.ct.Name(), current.parent, err)
		}
		desired = append(desired, plannedRecord{parent: current.parent, record: record, pos: current.pos})
		current = nil
		return nil
	}

	for pos, e := range events {
		if e.groupable != nil {
			// Adjacency alone is not enough: parent and type must agree,
			// and two distinct old origins preserve a boundary even after
			// the content that used to separate them disappears.
			if current != nil && current.parent == e.parent && current.ct == e.groupable &&
				(current.origin == 0 || e.origin == 0 || current.origin == e.origin) {
				// New members extend the preceding compatible group. For
				// example, old A -> new -> old B becomes [A, new], [B],
				// not one merged group.
				current.members = append(current.members, e.record)
				if current.origin == 0 {
					// A run of new members before the first old survivor
					// prepends to that survivor's group. Adopt its origin
					// so a later, different old group cannot join the run.
					current.origin = e.origin
				}
				continue
			}
			// A different parent, type, or old origin ends the run.
			if err := flush(); err != nil {
				return nil, err
			}
			current = &group{parent: e.parent, ct: e.groupable, origin: e.origin, members: []content.KeyedRecord{e.record}, pos: pos}
			continue
		}
		// Ordinary blocks and standalone generated references are barriers:
		// neither allows groups on opposite sides to attach to each other.
		if err := flush(); err != nil {
			return nil, err
		}
		if e.parent != 0 {
			// Only generated output belongs in the desired records. An
			// ordinary block is already persisted and acts only as a barrier.
			if e.record == nil {
				return nil, invalidInput("generated record under block %d is not keyed", e.parent)
			}
			desired = append(desired, plannedRecord{parent: e.parent, record: e.record, pos: pos})
		}
	}
	// The final run has no following event to trigger its flush.
	if err := flush(); err != nil {
		return nil, err
	}
	return desired, nil
}

// ---------------------------------------------------------------------------
// Validation and application
// ---------------------------------------------------------------------------

// validatePlan checks every record that will be written and sibling-key
// uniqueness against the complete final plan, before any write.
func (c *Controller) validatePlan(p *reconcilePlan) error {
	if p.primaryChanged {
		if err := c.registry.ValidateRecord(p.primaryRecord); err != nil {
			return fmt.Errorf("block %d: %w", p.primary.BlockNumber, err)
		}
	}
	for _, rev := range p.revisions {
		if err := checkGeneratedRecord(rev.record); err != nil {
			return err
		}
		if rev.record.ContentType().Name() != rev.block.Type {
			return invalidInput("block %d: content type %q cannot be changed to %q", rev.block.BlockNumber, rev.block.Type, rev.record.ContentType().Name())
		}
		if err := checkIdentityChange(rev.block.Record, rev.record, rev.block.BlockNumber); err != nil {
			return err
		}
		if err := c.registry.ValidateRecord(rev.record); err != nil {
			return fmt.Errorf("block %d: %w", rev.block.BlockNumber, err)
		}
	}
	for _, creation := range p.creations {
		if err := checkGeneratedRecord(creation.record); err != nil {
			return err
		}
		if err := c.registry.ValidateRecord(creation.record); err != nil {
			return fmt.Errorf("new output of block %d: %w", creation.parent, err)
		}
	}
	for parent, records := range p.final {
		seen := map[string]bool{}
		for _, record := range records {
			for _, member := range content.Members(record) {
				if seen[member.Key()] {
					return invalidInput("generated key %q used more than once in the output of block %d", member.Key(), parent)
				}
				seen[member.Key()] = true
			}
		}
	}
	return nil
}

// applyPlan writes the validated plan: retirements first (cascading),
// then the primary and record revisions, then creations in planner order.
// Per-write sibling-key checks are skipped: the final plan was validated
// as a whole, which allows membership to move between blocks.
func (c *Controller) applyPlan(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, p *reconcilePlan, result content.ExpansionResult, author model.AuthorID) (ReconcileResult, error) {
	out := ReconcileResult{Primary: p.primary, Output: map[string]model.BlockNumber{}}

	retired := map[model.BlockNumber]bool{}
	for _, blk := range p.retired {
		if retired[blk.BlockNumber] {
			continue
		}
		revs, err := c.DeleteBlockTreeTx(ctx, tx, documentUID, blk.BlockNumber, author)
		if err != nil {
			return ReconcileResult{}, err
		}
		for _, rev := range revs {
			retired[rev.BlockNumber] = true
		}
		out.Revisions = append(out.Revisions, revs...)
	}

	if p.primaryChanged {
		rev, err := storeRecordRevision(ctx, tx, documentUID, p.primary, p.primaryRecord, author)
		if err != nil {
			return ReconcileResult{}, err
		}
		out.Primary = rev
		out.Revisions = append(out.Revisions, rev)
	}

	for _, planned := range p.revisions {
		rev, err := storeRecordRevision(ctx, tx, documentUID, planned.block, planned.record, author)
		if err != nil {
			return ReconcileResult{}, err
		}
		out.Revisions = append(out.Revisions, rev)
	}

	target := p.primary.BlockNumber
	for _, creation := range p.creations {
		created, err := insertChildBlock(ctx, tx, documentUID, creation.parent, creation.record, author)
		if err != nil {
			return ReconcileResult{}, err
		}
		p.matched[parentKey{creation.parent, anchorKey(creation.record)}] = created.BlockNumber
		out.Revisions = append(out.Revisions, created)
	}

	for _, record := range p.final[target] {
		number := p.matched[parentKey{target, anchorKey(record)}]
		for _, member := range content.Members(record) {
			out.Output[member.Key()] = number
		}
	}
	for _, block := range result.Blocks {
		if _, expands := block.Record.ContentType().(content.Expander); !expands {
			continue
		}
		out.Expansions = append(out.Expansions, PendingExpansion{
			BlockNumber: out.Output[block.Record.Key()],
			Record:      block.Record,
			Payload:     block.ExpansionPayload,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// liveBlocks returns the latest revisions of blocks that are live with a
// live ancestry, records deserialized. Blocks beneath a deleted ancestor
// (possible after public root deletion) do not take part in planning.
func (c *Controller) liveBlocks(latest map[model.BlockNumber]model.Block) (map[model.BlockNumber]model.Block, error) {
	live := map[model.BlockNumber]model.Block{}
	for number, blk := range latest {
		if !liveAncestry(latest, number) {
			continue
		}
		if err := c.deserializeRecord(&blk); err != nil {
			return nil, err
		}
		live[number] = blk
	}
	return live, nil
}

func liveAncestry(latest map[model.BlockNumber]model.Block, number model.BlockNumber) bool {
	seen := map[model.BlockNumber]bool{}
	for {
		blk, ok := latest[number]
		if !ok || blk.Status == model.DeletedBlock || seen[number] {
			return false
		}
		if blk.ParentBlockNumber == nil {
			return true
		}
		seen[number] = true
		number = *blk.ParentBlockNumber
	}
}

// checkPrimaryResult rejects results changing the primary's identity: its
// content type and (for keyed generated primaries) its key.
func checkPrimaryResult(primary model.Block, result content.ExpansionResult) error {
	if result.Record == nil || result.Record.ContentType() == nil || result.Record.ContentType().Name() != primary.Type {
		return invalidInput("block %d: expansion changed or omitted the primary record type", primary.BlockNumber)
	}
	if old, keyed := primary.Record.(content.KeyedRecord); keyed {
		updated, ok := result.Record.(content.KeyedRecord)
		if !ok || updated.Key() != old.Key() {
			return invalidInput("block %d: expansion changed the primary key %q", primary.BlockNumber, old.Key())
		}
	}
	for n, block := range result.Blocks {
		if block.Record == nil || block.Record.ContentType() == nil {
			return invalidInput("block %d: emitted block %d has no record", primary.BlockNumber, n)
		}
	}
	return nil
}

// anchorKey returns the key of a desired record (a collection's first
// member); unique within a parent's desired output.
func anchorKey(record content.Record) string {
	if keyed, ok := record.(content.KeyedRecord); ok {
		return keyed.Key()
	}
	return ""
}

func sameBlockSet(a, b map[model.BlockNumber]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for number := range a {
		if !b[number] {
			return false
		}
	}
	return true
}
