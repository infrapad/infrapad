package controller

// Payload submission: the producer-driven refresh of an existing root
// primary from a complete, transient payload snapshot.
//
// SubmitBlockPayload owns the document-locked transaction (BeginHostTx),
// the root-target checks, capturing the refreshed document, and
// commit/rollback. Tree processing (payload validation, expansion, output
// validation, reconciliation, and downstream expansion) is delegated to
// the transaction-scoped ExpandTreeTx, which never commits on its own.
// Any failure rolls back every change; payloads are never persisted.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// document-locked transaction obtained from BeginHostTx: payload
// validation, expansion, output validation, ReconcileExpansionTx, and
// downstream processing of emitted expandable references, all sharing tx.
// The caller owns Commit/Rollback and must roll back on any error: a
// partial tree is never valid to commit. Failures are *SubmissionError
// values carrying the expansion path.
func (c *Controller) ExpandTreeTx(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, primary model.Block, payload json.RawMessage, author model.AuthorID) error {
	return c.expandNodeTx(ctx, tx, documentUID, primary, payload, nil, author)
}

// expandNodeTx processes one node of the expansion tree. path holds the
// block numbers of the ancestors already being processed in this tree,
// root first.
func (c *Controller) expandNodeTx(ctx context.Context, tx store.Tx, documentUID model.DocumentUID, primary model.Block, payload json.RawMessage, path []model.BlockNumber, author model.AuthorID) error {
	path = append(append([]model.BlockNumber{}, path...), primary.BlockNumber)
	root := len(path) == 1
	fail := func(reason SubmissionReason, err error) error {
		return &SubmissionError{
			Reason: reason, DocumentUID: documentUID, BlockNumber: primary.BlockNumber,
			ContentType: primary.Type, ExpansionPath: path, Err: err,
		}
	}
	// Payloads of downstream references come from extensions: invalid ones
	// are invalid extension output, not invalid producer input.
	invalidPayload := func(err error) error {
		if root {
			return fail(ReasonInvalidPayload, err)
		}
		return fail(ReasonInvalidOutput, err)
	}

	expander, ok := primary.Record.ContentType().(content.Expander)
	if !ok {
		return fail(ReasonExpansionUnsupported,
			fmt.Errorf("content type %s does not accept payloads", primary.Type))
	}
	if err := c.registry.ValidateRecord(primary.Record); err != nil {
		return fail(ReasonExpansionFailed, fmt.Errorf("stored primary record: %w", err))
	}
	if err := c.registry.ValidatePayload(primary.Type, payload); err != nil {
		return invalidPayload(err)
	}

	result, err := expander.Expand(ctx, primary.Record, payload)
	if err != nil {
		// Extensions report semantic payload problems (beyond the schema)
		// as payload-stage validation errors; anything else is a
		// processing failure.
		var validation *content.ValidationError
		if errors.As(err, &validation) && validation.Stage == content.StagePayload {
			return invalidPayload(err)
		}
		return fail(ReasonExpansionFailed, err)
	}
	if err := c.registry.ValidateExpansion(primary.Record, result); err != nil {
		return fail(ReasonInvalidOutput, err)
	}

	// TODO(cascading-extension-expansion): reconcile first, then recursively
	// expand every ReconcileResult.Expansions entry (its BlockNumber with
	// its transient Payload, loaded from tx) through expandNodeTx with
	// path, enforcing the cycle and depth policy
	// (mapped to FailedPrecondition by the transport). Until then, reject
	// downstream work up front rather than committing only the root expansion.
	for _, block := range result.Blocks {
		if _, expands := block.Record.ContentType().(content.Expander); expands {
			return fail(ReasonCascadingUnsupported,
				fmt.Errorf("emitted %s reference %q requires downstream expansion, which is not supported yet",
					block.Record.ContentType().Name(), block.Record.Key()))
		}
	}

	if _, err := c.ReconcileExpansionTx(ctx, tx, documentUID, primary.BlockNumber, result, author); err != nil {
		// The target and output were validated above; remaining invalid
		// input concerns the extension output (e.g. key collisions).
		if IsInvalidInput(err) {
			return fail(ReasonInvalidOutput, err)
		}
		return fail(ReasonExpansionFailed, err)
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
	// ReasonCascadingUnsupported is an interim rejection of downstream
	// expansion until bounded cascading is implemented.
	ReasonCascadingUnsupported SubmissionReason = "CASCADING_EXPANSION_UNSUPPORTED"
	ReasonInvalidOutput        SubmissionReason = "INVALID_EXTENSION_OUTPUT"
	ReasonExpansionFailed      SubmissionReason = "EXPANSION_FAILED"
)

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
	// ExpansionPath lists the block numbers from the submitted root to
	// BlockNumber; empty for target checks before processing started.
	ExpansionPath []model.BlockNumber

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

// Path renders ExpansionPath as "1/5/7".
func (e *SubmissionError) Path() string {
	steps := make([]string, len(e.ExpansionPath))
	for n, number := range e.ExpansionPath {
		steps[n] = strconv.Itoa(int(number))
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
