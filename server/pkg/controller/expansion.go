package controller

// Bounded cascading expansion: automatically expanding emitted standalone
// references through their transient ExpansionPayload values.
//
// ExpandTreeTx stages the complete expansion tree before any write: each
// node's payload is validated, its expander invoked, and its complete
// output validated (including cross-extension content), depth-first in
// emission order with parents before children. An emitted reference's
// final record is the record its own expansion returns; the parent's
// staged output already carries that final record, so a reference is
// persisted exactly once with its final state, never as the emitted
// record followed by a separate enrichment revision.
//
// Two bounds guard the traversal: cycles, detected by comparing each
// node's input tuple (content type, inner reference content, payload,
// compared as JSON values) against the active ancestor path only — the
// same reference is free to appear with a different payload or in sibling
// branches — and a fixed depth limit (see maxExpansionDepth), which also
// bounds chains whose inputs keep changing and therefore never repeat.
// A ninth level is rejected before its expander is invoked.
//
// After staging, the whole tree is reconciled as one document-wide plan
// (reconciliation.go): final grouping and identity matching are computed
// against the pre-submission persisted membership, so temporary traversal
// state never becomes a preserved boundary, and only final differences
// are applied — an identical complete tree creates zero revisions
// anywhere. All writes stay in the caller's document-locked transaction;
// any failure rolls back the entire operation.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/model"
)

// maxExpansionDepth bounds the expansion tree: the submitted root counts
// as level one, so at most eight levels expand. There is deliberately no
// total invocation limit and nothing configurable: the depth bound also
// terminates chains whose inputs keep changing and therefore never match
// the cycle check.
const maxExpansionDepth = 8

// expansionStep is one node of the active expansion path: a persisted or
// provisional block number for planning, plus the emitted reference key
// for every downstream step. The root has no emitted key.
type expansionStep struct {
	number model.BlockNumber
	key    string
}

// expansionInput is the cycle-detection tuple of one node: the canonical
// content type, the inner reference content, and the transient payload,
// all compared as JSON values (object-field order ignored) from the
// inputs before expansion. Host metadata (keys, timestamps, block
// numbers) is excluded, so the same reference with a different payload
// or identical work in sibling branches is not a cycle.
type expansionInput struct {
	typeName string
	content  string
	payload  string
}

// expander coordinates registered extensions to build a complete desired
// tree without writing revisions. Reconciliation persists the final state.
type expander struct {
	controller  *Controller
	documentUID model.DocumentUID
	// live is the pre-submission live membership, used to resolve which
	// old block retains an emitted reference.
	live map[model.BlockNumber]model.Block
	// next is the next provisional block number for references that will
	// be created (negative, so it never collides with persisted numbers).
	next model.BlockNumber
}

// expand builds a payload-free desired output tree without persistence:
// cycle and depth checks, payload validation, extension invocation, output
// validation, then recursion into emitted expandable references. existing
// is the persisted root record or the reference record emitted by the
// parent; step locates the node in the expansion path; ancestors and path
// hold the input tuples and steps of the active ancestor chain, root first.
func (e *expander) expand(ctx context.Context, existing content.Record, payload json.RawMessage, step expansionStep, ancestors []expansionInput, path []expansionStep) (*desiredTree, error) {
	path = append(append([]expansionStep{}, path...), step)
	typeName := existing.ContentType().Name()
	fail := func(reason SubmissionReason, err error) error {
		diagnostics := diagnosticPath(path)
		return &SubmissionError{
			Reason: reason, DocumentUID: e.documentUID,
			BlockNumber: diagnostics[len(diagnostics)-1].BlockNumber, ContentType: typeName,
			ExpansionPath: diagnostics, Err: err,
		}
	}

	// The depth bound is checked before the expander is invoked; the root
	// is level one, so a ninth level never runs.
	if len(path) > maxExpansionDepth {
		return nil, fail(ReasonExpansionDepthExceeded,
			fmt.Errorf("expansion of %s exceeds the maximum depth of %d levels", typeName, maxExpansionDepth))
	}
	payload = normalizePayload(payload)
	input := expansionInput{
		typeName: typeName,
		content:  canonicalJSON(contentJSON(existing)),
		payload:  canonicalJSON(payload),
	}
	for _, ancestor := range ancestors {
		if ancestor == input {
			return nil, fail(ReasonExpansionCycle,
				fmt.Errorf("%s repeats its reference content and payload in the expansion path", typeName))
		}
	}

	extension, ok := existing.ContentType().(content.Expander)
	if !ok {
		return nil, fail(ReasonExpansionUnsupported,
			fmt.Errorf("content type %s does not accept payloads", typeName))
	}
	// Payloads of downstream references come from extensions: invalid ones
	// are invalid extension output, not invalid producer input.
	if err := e.controller.registry.ValidatePayload(typeName, payload); err != nil {
		if len(path) == 1 {
			return nil, fail(ReasonInvalidPayload, err)
		}
		return nil, fail(ReasonInvalidOutput, err)
	}
	result, err := extension.Expand(ctx, existing, payload)
	if err != nil {
		// Extensions report semantic payload problems (beyond the schema)
		// as payload-stage validation errors; anything else is a
		// processing failure.
		var validation *content.ValidationError
		if errors.As(err, &validation) && validation.Stage == content.StagePayload {
			if len(path) == 1 {
				return nil, fail(ReasonInvalidPayload, err)
			}
			return nil, fail(ReasonInvalidOutput, err)
		}
		return nil, fail(ReasonExpansionFailed, err)
	}
	if err := e.controller.registry.ValidateExpansion(existing, result); err != nil {
		return nil, fail(ReasonInvalidOutput, err)
	}

	node := &desiredTree{number: step.number, record: result.Record}
	for _, block := range result.Blocks {
		emitted := block.Record
		if _, expands := emitted.ContentType().(content.Expander); !expands {
			node.output = append(node.output, emitted)
			continue
		}
		// A parent supplies its emitted reference content to the child
		// expander; the child's resulting record becomes that reference's
		// final state in the parent's staged output.
		child, err := e.expand(ctx, emitted, block.ExpansionPayload,
			e.referenceStep(step.number, emitted),
			append(append([]expansionInput{}, ancestors...), input), path)
		if err != nil {
			return nil, err
		}
		// ValidateExpansion guarantees an expander result preserves a keyed
		// primary's key, so the final reference record is keyed as emitted.
		final, keyed := child.record.(content.KeyedRecord)
		if !keyed {
			return nil, fail(ReasonInvalidOutput,
				fmt.Errorf("expansion of reference %q returned an unkeyed record", emitted.Key()))
		}
		node.output = append(node.output, final)
		node.children = append(node.children, child)
	}
	return node, nil
}

// referenceStep locates an emitted expandable reference of the current
// node parentNumber: the old live child the planner's membership matching
// will retain (keys are unique within a parent's live output, so the live
// child with the same content type and key is that block), or the next
// provisional number when the reference will be created. References of a
// provisional parent are always new.
func (e *expander) referenceStep(parentNumber model.BlockNumber, emitted content.KeyedRecord) expansionStep {
	if parentNumber > 0 {
		if number := resolveReference(e.live, parentNumber, emitted.ContentType().Name(), emitted.Key()); number > 0 {
			return expansionStep{number: number, key: emitted.Key()}
		}
	}
	step := expansionStep{number: e.next, key: emitted.Key()}
	e.next--
	return step
}

// resolveReference returns the live child of parent retaining a standalone
// reference of typeName with key, or 0 when none does. Deleted blocks and
// dead ancestries never retain anything (see liveBlocks).
func resolveReference(live map[model.BlockNumber]model.Block, parent model.BlockNumber, typeName, key string) model.BlockNumber {
	best := model.BlockNumber(0)
	for number, blk := range live {
		if blk.ParentBlockNumber == nil || *blk.ParentBlockNumber != parent || blk.Type != typeName {
			continue
		}
		if _, collection := blk.Record.(content.GroupableRecord); collection {
			continue
		}
		if keyed, ok := blk.Record.(content.KeyedRecord); ok && keyed.Key() == key && (best == 0 || number < best) {
			best = number
		}
	}
	return best
}

// ---------------------------------------------------------------------------
// Path and cycle-tuple helpers
// ---------------------------------------------------------------------------

// diagnosticPath preserves every emitted key and supplements it with
// known block numbers, omitting provisional planning numbers.
func diagnosticPath(path []expansionStep) []ExpansionPathStep {
	steps := make([]ExpansionPathStep, len(path))
	for n, step := range path {
		steps[n].ReferenceKey = step.key
		if step.number > 0 {
			steps[n].BlockNumber = step.number
		}
	}
	return steps
}

// normalizePayload treats an absent transient payload as an empty object.
func normalizePayload(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 {
		return json.RawMessage("{}")
	}
	return payload
}

// contentJSON returns the JSON encoding of the inner content of an
// individual record, or nil for other record shapes.
func contentJSON(record content.Record) []byte {
	item, ok := record.(content.ItemRecord)
	if !ok {
		return nil
	}
	data, err := json.Marshal(item.Content())
	if err != nil {
		return nil
	}
	return data
}

// canonicalJSON re-encodes a JSON value with sorted object keys, so
// semantically equal values (differing only in field order) compare equal
// as strings.
func canonicalJSON(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return string(data)
	}
	out, err := json.Marshal(value)
	if err != nil {
		return string(data)
	}
	return string(out)
}
