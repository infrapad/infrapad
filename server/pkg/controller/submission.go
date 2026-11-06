package controller

// Payload submission: the producer-driven refresh of an existing root
// primary from a complete, transient payload snapshot.
//
// SubmitBlockPayload owns the document-locked transaction (BeginHostTx),
// the root-target checks, capturing the refreshed document, and
// commit/rollback. Tree processing (payload validation, expansion,
// output validation, downstream expansion, and reconciliation) is
// delegated to the transaction-scoped ExpandTreeTx, which never commits
// on its own. Any failure rolls back every change; payloads are never
// persisted.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store"
)

// SubmitBlockPayload processes a complete payload snapshot for the live
// root primary blockNumber, selecting the registered expander from the
// stored block type. It reads and processes the latest state under the
// document mutation lock: concurrent submissions serialize and the last
// successful one wins. It returns the complete refreshed document,
// captured within the same transaction and returned only after commit.
// Identical snapshots create no revisions.
//
// Rejections are *SubmissionError values (see SubmissionReason) and
// leave the document unchanged.
func (c *Controller) SubmitBlockPayload(ctx context.Context, documentUID model.DocumentUID, blockNumber model.BlockNumber, payload json.RawMessage, author model.AuthorID) (model.Document, error) {
	tx, err := c.BeginHostTx(ctx, documentUID)
	if err != nil {
		if IsNotFound(err) {
			return model.Document{}, &SubmissionError{
				Reason:      ReasonDocumentNotFound,
				DocumentUID: documentUID, BlockNumber: blockNumber, Err: err,
			}
		}
		return model.Document{}, err
	}
	defer tx.Rollback()

	primary, err := c.submissionTarget(ctx, tx, documentUID, blockNumber)
	if err != nil {
		return model.Document{}, err
	}
	if err := c.ExpandTreeTx(ctx, tx, documentUID, primary, payload, author); err != nil {
		return model.Document{}, err
	}

	doc, err := c.loadDocumentTx(ctx, tx, documentUID)
	if err != nil {
		return model.Document{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Document{}, fmt.Errorf("commit tx: %w", err)
	}
	return doc, nil
}

// submissionTarget loads the latest revision of the targeted block and
// checks it is a live root primary implementing expansion. Generated
// references are expanded only internally, never targeted directly.
func (c *Controller) submissionTarget(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, blockNumber model.BlockNumber) (model.Block, error) {
	reject := func(reason SubmissionReason, blk model.Block, err error) error {
		return &SubmissionError{
			Reason: reason, DocumentUID: documentUID, BlockNumber: blockNumber,
			ContentType: blk.Type, Err: err,
		}
	}
	primary, err := tx.Blocks().Get(ctx, documentUID, blockNumber, 0)
	if err != nil {
		if IsNotFound(err) {
			return model.Block{}, reject(ReasonBlockNotFound, primary,
				fmt.Errorf("block %d: %w", blockNumber, err))
		}
		return model.Block{}, fmt.Errorf("get block %d: %w", blockNumber, err)
	}
	// Ownership is decided from stored provenance alone, before anything
	// else, like the public update boundary.
	if parent := primary.ParentBlockNumber; parent != nil {
		return model.Block{}, reject(ReasonGeneratedBlock, primary, &ForbiddenEditError{
			DocumentUID: documentUID, BlockNumber: blockNumber, ParentBlockNumber: *parent,
		})
	}
	if primary.Status == model.DeletedBlock {
		return model.Block{}, reject(ReasonDeletedBlock, primary,
			fmt.Errorf("block %d is deleted", blockNumber))
	}
	if err := c.deserializeRecord(&primary); err != nil {
		return model.Block{}, err
	}
	if _, expands := primary.Record.ContentType().(content.Expander); !expands {
		return model.Block{}, reject(ReasonExpansionUnsupported, primary,
			fmt.Errorf("content type %s does not accept payloads", primary.Type))
	}
	return primary, nil
}

// ExpandTreeTx processes a payload for the live primary block inside a
// document-locked transaction obtained from BeginHostTx: it stages the
// complete bounded expansion tree (payload validation, expansion, output
// validation, and recursive downstream expansion of emitted references,
// with cycle and depth checks — see expansion.go), then reconciles the
// whole tree as one plan (reconciliation.go). The caller owns
// Commit/Rollback and must roll back on any error: a partial tree is
// never valid to commit. Failures are *SubmissionError values carrying
// the expansion path.
func (c *Controller) ExpandTreeTx(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, primary model.Block, payload json.RawMessage, author model.AuthorID) error {
	latest, err := c.latestBlocks(ctx, tx, documentUID)
	if err != nil {
		return err
	}
	live, err := c.liveBlocks(latest)
	if err != nil {
		return err
	}
	root := func(reason SubmissionReason, err error) error {
		return &SubmissionError{
			Reason: reason, DocumentUID: documentUID, BlockNumber: primary.BlockNumber,
			ContentType: primary.Type, ExpansionPath: []ExpansionPathStep{{BlockNumber: primary.BlockNumber}}, Err: err,
		}
	}
	if _, ok := live[primary.BlockNumber]; !ok {
		if _, exists := latest[primary.BlockNumber]; exists {
			return root(ReasonDeletedBlock, fmt.Errorf("block %d is deleted", primary.BlockNumber))
		}
		return root(ReasonBlockNotFound, fmt.Errorf("block %d: %w", primary.BlockNumber, errNotFound))
	}
	if err := c.registry.ValidateRecord(primary.Record); err != nil {
		return root(ReasonExpansionFailed, fmt.Errorf("stored primary record: %w", err))
	}

	expander := &expander{controller: c, documentUID: documentUID, live: live, next: -1}
	tree, err := expander.expand(ctx, primary.Record, payload, expansionStep{number: primary.BlockNumber}, nil, nil)
	if err != nil {
		return err
	}
	if err := c.reconcileTree(ctx, tx, documentUID, latest, live, primary, tree, author); err != nil {
		// The tree was staged and validated above; remaining invalid input
		// concerns the extension output (e.g. key collisions).
		if IsInvalidInput(err) {
			return root(ReasonInvalidOutput, err)
		}
		return root(ReasonExpansionFailed, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Structured failures
// ---------------------------------------------------------------------------

// SubmissionReason is a stable, machine-readable rejection reason.
// Transports map it to their status codes.
type SubmissionReason string

const (
	ReasonInvalidPayload       SubmissionReason = "INVALID_PAYLOAD"
	ReasonDocumentNotFound     SubmissionReason = "DOCUMENT_NOT_FOUND"
	ReasonBlockNotFound        SubmissionReason = "BLOCK_NOT_FOUND"
	ReasonGeneratedBlock       SubmissionReason = "GENERATED_BLOCK"
	ReasonDeletedBlock         SubmissionReason = "DELETED_BLOCK"
	ReasonExpansionUnsupported SubmissionReason = "EXPANSION_UNSUPPORTED"
	// ReasonExpansionCycle marks an emitted reference whose input tuple
	// (content type, reference content, payload) repeats an active
	// ancestor's, so the tree would never terminate.
	ReasonExpansionCycle SubmissionReason = "EXPANSION_CYCLE"
	// ReasonExpansionDepthExceeded marks a tree deeper than the fixed
	// maximum of eight levels (the submitted root included).
	ReasonExpansionDepthExceeded SubmissionReason = "EXPANSION_DEPTH_EXCEEDED"
	ReasonInvalidOutput          SubmissionReason = "INVALID_EXTENSION_OUTPUT"
	ReasonExpansionFailed        SubmissionReason = "EXPANSION_FAILED"
)

// ExpansionPathStep locates one reference in an expansion path. The root
// carries the submitted block number; every downstream step carries its
// emitted key, scoped to the preceding step. Known persisted block numbers
// supplement keys but never replace them. Zero means not yet persisted;
// provisional planning numbers are never exposed.
type ExpansionPathStep struct {
	ReferenceKey string            `json:"reference_key,omitempty"`
	BlockNumber  model.BlockNumber `json:"block_number,omitempty"`
}

// SubmissionError is a structured, transport-neutral payload-submission
// rejection. Inspect it with errors.As; a wrapped *content.ValidationError
// (if any) carries stage, location, and field violations.
type SubmissionError struct {
	Reason SubmissionReason

	DocumentUID model.DocumentUID
	// BlockNumber is the block being processed when the failure occurred
	// (the target for root-level failures).
	BlockNumber model.BlockNumber
	// ContentType is the type of that block, when known.
	ContentType string
	// ExpansionPath locates the failure from the submitted root through
	// the emitted reference keys; empty for target checks before processing.
	ExpansionPath []ExpansionPathStep

	Err error
}

func (e *SubmissionError) Error() string {
	var out strings.Builder
	fmt.Fprintf(&out, "block %d of document %q", e.BlockNumber, e.DocumentUID)
	if len(e.ExpansionPath) > 1 {
		fmt.Fprintf(&out, " (expansion path %s)", e.Path())
	}
	fmt.Fprintf(&out, ": %s", e.Reason)
	if e.Err != nil {
		fmt.Fprintf(&out, ": %v", e.Err)
	}
	return out.String()
}

func (e *SubmissionError) Unwrap() error { return e.Err }

// Path renders the root block number followed by emitted reference keys,
// e.g. "1/thread/message". Keys are URL path-escaped so slashes within a
// key cannot be confused with step separators. The path is independent
// of whether downstream references have been persisted.
func (e *SubmissionError) Path() string {
	steps := make([]string, len(e.ExpansionPath))
	for n, step := range e.ExpansionPath {
		if n == 0 {
			steps[n] = strconv.Itoa(int(step.BlockNumber))
		} else {
			steps[n] = url.PathEscape(step.ReferenceKey)
		}
	}
	return strings.Join(steps, "/")
}

// Validation returns the wrapped validation error, if any.
func (e *SubmissionError) Validation() *content.ValidationError {
	var validation *content.ValidationError
	if errors.As(e.Err, &validation) {
		return validation
	}
	return nil
}
