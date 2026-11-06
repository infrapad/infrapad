package controller

// Snapshot reconciliation: applying a desired output tree to the document.
//
// Each parent in the tree replaces its previous live direct output with
// its complete desired output. The whole document is re-planned from
// pre-submission persisted membership. Reconciliation is independent of
// how the desired records were prepared; it neither consumes payloads nor
// invokes extensions:
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
//     in planner order, parent-first for new references. Sibling-key
//     uniqueness is checked against the complete final plan, so
//     membership may move between blocks without intermediate revisions.
//
// Final persisted block views keep their own ordering (timestamp, then
// block number); planning order only decides grouping and allocation.

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store"
)

// desiredTree describes final records and complete direct output for one
// affected parent and its affected descendants. It carries no payloads or
// information about how the desired state was prepared. Records must be
// validated before reconciliation.
type desiredTree struct {
	// number identifies the existing block, or is a negative provisional
	// number for a new parent. The root must identify a live block.
	number model.BlockNumber
	record content.Record
	output []content.KeyedRecord
	// children describe output replacements for records in output, in
	// output order. An omitted child leaves that record's descendants
	// unchanged; an included child with empty output retires them.
	children []*desiredTree
}

// reconcileTree plans and applies a complete desired tree inside the
// caller's document-locked transaction: document-wide planning against
// pre-submission live membership, a stable retirement-exclusion fixpoint,
// whole-plan validation, then application of only the final differences.
// The root block must be live. Preparation and expansion are outside this
// boundary; only final records and parent relationships are inputs.
func (c *Controller) reconcileTree(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, latest, live map[model.BlockNumber]model.Block, root model.Block, tree *desiredTree, author model.AuthorID) error {
	nodes := tree.flatten(0)
	// Plan until the retired set is stable: retiring a block excludes its
	// descendants, which may in turn change grouping elsewhere.
	excluded := map[model.BlockNumber]bool{}
	var p *reconcilePlan
	var err error
	for iteration := 0; ; iteration++ {
		if p, err = planReconciliation(live, root, nodes, excluded); err != nil {
			return err
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
			return fmt.Errorf("reconcile block %d: planning did not converge", root.BlockNumber)
		}
		excluded = next
	}

	if err := c.validatePlan(p); err != nil {
		return err
	}
	return c.applyPlan(ctx, tx, documentUID, p, author)
}

// ---------------------------------------------------------------------------
// Planning
// ---------------------------------------------------------------------------

// desiredNode is a flat planning node: its final record and complete
// desired direct output, with its immediate generating parent (0 for the root).
type desiredNode struct {
	parentBlockNumber model.BlockNumber
	blockNumber       model.BlockNumber
	record            content.Record
	output            []content.KeyedRecord
}

// flatten projects the desired tree into parent-first planning nodes.
// Tree shape is independent of document-wide event order.
func (n *desiredTree) flatten(parentBlockNumber model.BlockNumber) []desiredNode {
	out := []desiredNode{{parentBlockNumber: parentBlockNumber, blockNumber: n.number, record: n.record, output: n.output}}
	for _, child := range n.children {
		out = append(out, child.flatten(n.number)...)
	}
	return out
}

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
	// provisional maps (parent, anchor key) of a creation that realizes a
	// new parent to that parent's provisional number, so
	// application can translate it to the allocated block number.
	provisional map[parentKey]model.BlockNumber
}

type parentKey struct {
	parent model.BlockNumber
	key    string
}

// planReconciliation computes one planning pass from the live document,
// ignoring excluded blocks (descendants of retired output). The affected
// parents' previous live direct output is replaced by their desired
// output (for the root, its resulting record replaces the persisted one);
// every other live, non-excluded block contributes its persisted members.
// It only builds a plan: the caller repeats planning if retirements change
// descendant exclusions, then validates the stable plan before applying
// any writes.
func planReconciliation(live map[model.BlockNumber]model.Block, root model.Block, nodes []desiredNode, excluded map[model.BlockNumber]bool) (*reconcilePlan, error) {
	// Group across the whole document first: even another primary's output
	// can split a collection. No block identities are assigned at this stage.
	events, oldChildren := buildPlanningInput(live, root, nodes, excluded)
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
		primary:        root,
		primaryRecord:  nodes[0].record,
		primaryChanged: !content.EqualRecords(root.Record, nodes[0].record),
		final:          map[model.BlockNumber][]content.Record{},
		provisional:    map[parentKey]model.BlockNumber{},
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
			// Append revisions only for semantic changes.
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
	// Represent empty snapshots explicitly, even for parents with no
	// previous output that never appeared in either parent set. New
	// (provisional) references have no old output at all.
	for _, node := range nodes {
		if _, ok := p.final[node.blockNumber]; !ok {
			p.final[node.blockNumber] = nil
		}
		if node.blockNumber < 0 {
			p.provisional[parentKey{node.parentBlockNumber, anchorKey(node.record)}] = node.blockNumber
		}
	}
	return p, nil
}

// buildPlanningInput builds the document-wide event stream and
// indexes old live children per parent for matching. Each affected parent's
// previous live direct output is replaced by its desired output (the
// root's own event carries its resulting record); every other live,
// non-excluded block contributes its persisted members. Desired output
// under a provisional (negative) parent number belongs to a reference
// that will be created.
func buildPlanningInput(live map[model.BlockNumber]model.Block, root model.Block, nodes []desiredNode, excluded map[model.BlockNumber]bool) ([]planEvent, map[model.BlockNumber][]model.Block) {
	target := root.BlockNumber
	replacedParents := map[model.BlockNumber]bool{}
	for _, node := range nodes {
		if node.blockNumber > 0 {
			replacedParents[node.blockNumber] = true
		}
	}
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
			// The root participates with its resulting record.
			blk.Record = nodes[0].record
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
		if replacedParents[parent] {
			continue // replaced by the desired output below
		}
		events = append(events, generatedEvents(parent, blk.Record)...)
	}
	for _, node := range nodes {
		for _, record := range node.output {
			events = append(events, generatedEvents(node.blockNumber, record)...)
		}
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
// then the primary and record revisions, then creations. Creations run
// parent-first: a child of a reference that is itself being created can
// only be inserted after that reference, whatever the planner order
// says, with provisional (negative) parent numbers translated to the
// allocated block numbers as references are created. Per-write
// sibling-key checks are skipped: the final plan was validated as a
// whole, which allows membership to move between blocks.
func (c *Controller) applyPlan(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, p *reconcilePlan, author model.AuthorID) error {
	retired := map[model.BlockNumber]bool{}
	for _, blk := range p.retired {
		if retired[blk.BlockNumber] {
			continue
		}
		revs, err := c.DeleteBlockTreeTx(ctx, tx, documentUID, blk.BlockNumber, author)
		if err != nil {
			return err
		}
		for _, rev := range revs {
			retired[rev.BlockNumber] = true
		}
	}

	if p.primaryChanged {
		if _, err := storeRecordRevision(ctx, tx, documentUID, p.primary, p.primaryRecord, author); err != nil {
			return err
		}
	}

	for _, planned := range p.revisions {
		if _, err := storeRecordRevision(ctx, tx, documentUID, planned.block, planned.record, author); err != nil {
			return err
		}
	}

	// Creations run in planner order, but a creation under a provisional
	// parent waits until that reference has been created; each pass
	// resolves at least one tree level.
	allocated := map[model.BlockNumber]model.BlockNumber{}
	creations := append([]plannedRecord(nil), p.creations...)
	for len(creations) > 0 {
		var deferred []plannedRecord
		for _, creation := range creations {
			parent := creation.parent
			if real, ok := allocated[parent]; ok {
				parent = real
			} else if parent < 0 {
				deferred = append(deferred, creation)
				continue
			}
			created, err := insertChildBlock(ctx, tx, documentUID, parent, creation.record, author)
			if err != nil {
				return err
			}
			// A creation realizing a new parent publishes its
			// allocated number for the reference's own children.
			if prov, ok := p.provisional[parentKey{creation.parent, anchorKey(creation.record)}]; ok {
				allocated[prov] = created.BlockNumber
			}
		}
		if len(deferred) == len(creations) {
			return fmt.Errorf("apply plan: unresolved provisional parent for %d creation(s)", len(deferred))
		}
		creations = deferred
	}
	return nil
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
