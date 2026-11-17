package controller

// Markdown push: server-side interpretation of an edited InfraPad Markdown
// document (see shared/markdown for the host framing).
//
// PushDocumentMarkdown parses the document structurally, compares every
// existing block with the revision it references (its historical
// baseline), and turns actual header/body edits into new revisions through
// the registered content types' optional capabilities
// (content.MarkdownHeader, content.MarkdownUpdater). `block=new` sections
// are created from temporary empty typed records through the same
// capabilities. Everything runs in one document-locked transaction,
// including rendering the refreshed document; any failure leaves the
// document unchanged. Omitted blocks are untouched (no deletion).

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store"
	"github.com/infrapad/infrapad/shared/markdown"
)

// MarkdownChangeAction is the kind of a persisted Markdown push change.
type MarkdownChangeAction string

const (
	MarkdownBlockAdded   MarkdownChangeAction = "added"
	MarkdownBlockUpdated MarkdownChangeAction = "updated"
)

// MarkdownChange is one block actually persisted by a push.
type MarkdownChange struct {
	BlockNumber model.BlockNumber
	Action      MarkdownChangeAction
}

// MarkdownPushResult is the outcome of a successful push: the complete
// refreshed Markdown (captured in the push transaction) and the persisted
// changes in submitted order (empty for a no-op push).
type MarkdownPushResult struct {
	Markdown string
	Changes  []MarkdownChange
}

// PushDocumentMarkdown applies an edited InfraPad Markdown document to the
// document uid atomically and returns the refreshed Markdown.
//
//   - Existing blocks are compared with their referenced revision: the
//     structured header (by parsed values) and the body (ignoring
//     trailing newlines) independently. Untouched blocks are skipped,
//     including generated and stale ones; edited blocks must be root
//     blocks referencing their current, live revision. Host metadata
//     (type, author, parent, timestamp) cannot be edited.
//   - Changed headers are applied (UpdateMarkdownHeader) before changed
//     bodies (UpdateMarkdown); results identical to the stored record
//     create no revision.
//   - `block=new` sections are created in submitted order from an empty
//     typed seed of a standalone type.
//
// Submitted title, namespace, and document status are informational and
// ignored. Rejections are *MarkdownPushError values.
func (c *Controller) PushDocumentMarkdown(ctx context.Context, uid model.DocumentUID, source string, author model.AuthorID) (MarkdownPushResult, error) {
	parsed, err := markdown.Parse(source)
	if err != nil {
		pushErr := &MarkdownPushError{Reason: PushInvalidMarkdown, DocumentUID: uid, Err: err}
		var parseErr *markdown.ParseError
		if errors.As(err, &parseErr) {
			pushErr.Line, pushErr.BlockIndex, pushErr.Err = parseErr.Line, parseErr.BlockIndex, parseErr.Err
		}
		return MarkdownPushResult{}, pushErr
	}
	if got := model.DocumentUID(parsed.Frontmatter.DocumentID); got != uid {
		return MarkdownPushResult{}, &MarkdownPushError{
			Reason: PushDocumentMismatch, DocumentUID: uid, Line: 1,
			Err: fmt.Errorf("frontmatter document %q does not match the target document", got),
		}
	}

	tx, err := c.BeginHostTx(ctx, uid)
	if err != nil {
		if IsNotFound(err) {
			return MarkdownPushResult{}, &MarkdownPushError{Reason: PushDocumentNotFound, DocumentUID: uid, Err: err}
		}
		return MarkdownPushResult{}, err
	}
	defer tx.Rollback()

	// Prepare and validate every change before writing anything.
	now := time.Now().UTC()
	var plans []markdownPlan
	for _, blk := range parsed.Blocks {
		plan, err := c.planMarkdownBlock(ctx, tx, uid, blk, now)
		if err != nil {
			return MarkdownPushResult{}, err
		}
		if plan != nil {
			plans = append(plans, *plan)
		}
	}

	changes, err := applyMarkdownPlans(ctx, tx, uid, plans, author)
	if err != nil {
		return MarkdownPushResult{}, err
	}

	doc, err := c.loadDocumentTx(ctx, tx, uid)
	if err != nil {
		return MarkdownPushResult{}, err
	}
	rendered, err := renderDocumentMarkdown(doc)
	if err != nil {
		return MarkdownPushResult{}, &MarkdownPushError{Reason: PushRenderFailed, DocumentUID: uid, Err: err}
	}
	if err := tx.Commit(); err != nil {
		return MarkdownPushResult{}, fmt.Errorf("commit tx: %w", err)
	}
	return MarkdownPushResult{Markdown: rendered, Changes: changes}, nil
}

// markdownPlan is one validated change: a new revision of latest, or a new
// block when latest is nil.
type markdownPlan struct {
	latest *model.Block
	record content.Record
	// newRevision is false for in-place updates of existing blocks.
	newRevision bool
}

// planMarkdownBlock returns the validated change of one parsed block, or
// nil when the block is unchanged.
func (c *Controller) planMarkdownBlock(ctx context.Context, tx store.Tx, uid model.DocumentUID, blk markdown.Block, now time.Time) (*markdownPlan, error) {
	if blk.Directive.New {
		return c.planNewMarkdownBlock(uid, blk, now)
	}
	return c.planExistingMarkdownBlock(ctx, tx, uid, blk)
}

func (c *Controller) planExistingMarkdownBlock(ctx context.Context, tx store.Tx, uid model.DocumentUID, blk markdown.Block) (*markdownPlan, error) {
	d := blk.Directive
	number := model.BlockNumber(d.BlockNumber)
	reject := func(reason MarkdownPushReason, contentType string, err error) error {
		return &MarkdownPushError{
			Reason: reason, DocumentUID: uid, BlockNumber: number, BlockIndex: blk.Index,
			Line: blk.Line, ContentType: contentType, Err: err,
		}
	}

	revision, err := tx.Blocks().Get(ctx, uid, number, model.RevisionNumber(d.Revision))
	if err != nil {
		if IsNotFound(err) {
			return nil, reject(PushBlockNotFound, d.Type, fmt.Errorf("block %d revision %d: %w", number, d.Revision, err))
		}
		return nil, fmt.Errorf("get block %d revision %d: %w", number, d.Revision, err)
	}
	if err := c.deserializeRecord(&revision); err != nil {
		return nil, err
	}
	if err := checkMarkdownHostMetadata(d, revision); err != nil {
		return nil, reject(PushHostMetadataChanged, revision.Type, err)
	}

	// Detect local edits against the referenced (historical) revision.
	record := revision.Record
	ct := record.ContentType()
	baseHeader, err := markdownHeaderFields(record)
	if err != nil {
		return nil, reject(PushInvalidOutput, revision.Type, err)
	}
	baseBody, err := ct.RenderMarkdown(record)
	if err != nil {
		return nil, reject(PushInvalidOutput, revision.Type, fmt.Errorf("render body: %w", err))
	}
	_, hasUpdater := ct.(content.MarkdownUpdater)
	headerChanged := !equalMarkdownFields(blk.Header, baseHeader)
	bodyChanged := normalizeBody(blk.Body) != normalizeBody(baseBody) &&
		(hasUpdater || strings.TrimSpace(blk.Body) != "")
	if !headerChanged && !bodyChanged {
		return nil, nil
	}

	// An actual edit: ownership first, then the current-revision check.
	latest, err := tx.Blocks().Get(ctx, uid, number, 0)
	if err != nil {
		return nil, fmt.Errorf("get latest block %d: %w", number, err)
	}
	if err := checkPublicEditOwnership(uid, &latest); err != nil {
		return nil, reject(PushGeneratedBlock, latest.Type, err)
	}
	if latest.Status == model.DeletedBlock {
		return nil, reject(PushDeletedBlock, latest.Type, fmt.Errorf("block %d is deleted", number))
	}
	if latest.RevisionNumber != revision.RevisionNumber {
		return nil, reject(PushStaleRevision, latest.Type,
			fmt.Errorf("block %d edited at revision %d, but the current revision is %d", number, d.Revision, latest.RevisionNumber))
	}
	latest.Record = record

	updated, err := c.applyMarkdownEdits(record, blk, headerChanged, bodyChanged)
	if err != nil {
		return nil, c.markdownEditError(reject, revision.Type, err)
	}
	if content.EqualRecords(updated, record) {
		return nil, nil
	}
	newRevision, err := shouldCreateRevision(latest, updated, latest.Status)
	if err != nil {
		return nil, reject(PushInvalidOutput, revision.Type, err)
	}
	return &markdownPlan{latest: &latest, record: updated, newRevision: newRevision}, nil
}

func (c *Controller) planNewMarkdownBlock(uid model.DocumentUID, blk markdown.Block, now time.Time) (*markdownPlan, error) {
	typ := blk.Directive.Type
	reject := func(reason MarkdownPushReason, contentType string, err error) error {
		return &MarkdownPushError{
			Reason: reason, DocumentUID: uid, BlockIndex: blk.Index,
			Line: blk.Line, ContentType: contentType, Err: err,
		}
	}
	ct, err := c.registry.Lookup(typ)
	if err != nil {
		return nil, reject(PushInvalidContent, typ, err)
	}
	if _, groupable := ct.(content.GroupableContentType); groupable {
		return nil, reject(PushUnsupportedEdit, typ, fmt.Errorf("content type %s is generated-only and cannot be created directly", typ))
	}
	// A temporary empty typed seed: never validated or persisted itself.
	seedValue, err := ct.DecodeJSON([]byte(`{}`))
	if err != nil {
		return nil, reject(PushInvalidOutput, typ, fmt.Errorf("decode empty seed: %w", err))
	}
	seed := content.NewItem(ct, now, seedValue)
	record, err := c.applyMarkdownEdits(seed, blk, len(blk.Header) > 0, strings.TrimSpace(blk.Body) != "")
	if err != nil {
		return nil, c.markdownEditError(reject, typ, err)
	}
	return &markdownPlan{record: record}, nil
}

// applyMarkdownEdits applies the changed header (replacement semantics)
// and then the changed body through the type's capabilities, and checks
// the completed record: preserved host metadata, then full validation.
// Intermediate states are not validated.
func (c *Controller) applyMarkdownEdits(existing content.Record, blk markdown.Block, header, body bool) (content.Record, error) {
	ct := existing.ContentType()
	record := existing
	if header {
		headerUpdater, ok := ct.(content.MarkdownHeader)
		if !ok {
			return nil, errUnsupportedEdit{fmt.Errorf("content type %s has no editable structured header", ct.Name())}
		}
		updated, err := headerUpdater.UpdateMarkdownHeader(record, blk.Header)
		if err != nil {
			return nil, errCallback{fmt.Errorf("update header: %w", err)}
		}
		record = updated
	}
	if body {
		bodyUpdater, ok := ct.(content.MarkdownUpdater)
		if !ok {
			return nil, errUnsupportedEdit{fmt.Errorf("content type %s has no editable Markdown body", ct.Name())}
		}
		updated, err := bodyUpdater.UpdateMarkdown(record, normalizeBody(blk.Body))
		if err != nil {
			return nil, errCallback{fmt.Errorf("update body: %w", err)}
		}
		record = updated
	}
	if err := checkPreservedRecordMetadata(existing, record); err != nil {
		return nil, err
	}
	if err := c.registry.ValidateRecord(record); err != nil {
		return nil, errCallback{err}
	}
	return record, nil
}

// errUnsupportedEdit and errCallback classify applyMarkdownEdits failures;
// anything else is invalid extension output.
type errUnsupportedEdit struct{ error }
type errCallback struct{ error }

func (e errUnsupportedEdit) Unwrap() error { return e.error }
func (e errCallback) Unwrap() error        { return e.error }

func (c *Controller) markdownEditError(reject func(MarkdownPushReason, string, error) error, contentType string, err error) error {
	var unsupported errUnsupportedEdit
	var callback errCallback
	switch {
	case errors.As(err, &unsupported):
		return reject(PushUnsupportedEdit, contentType, unsupported.error)
	case errors.As(err, &callback):
		return reject(PushInvalidContent, contentType, callback.error)
	default:
		return reject(PushInvalidOutput, contentType, err)
	}
}

// applyMarkdownPlans persists validated changes in submitted order:
// revisions (or in-place replacements chosen by a content type's revision
// policy) preserve stored status, type, parent, and event time; new
// blocks are allocated by the store.
func applyMarkdownPlans(ctx context.Context, tx store.Tx, uid model.DocumentUID, plans []markdownPlan, author model.AuthorID) ([]MarkdownChange, error) {
	changes := []MarkdownChange{}
	for _, plan := range plans {
		if plan.latest != nil {
			rev, err := applyRecordWrite(ctx, tx, uid, *plan.latest, plan.record, plan.latest.Status, author, plan.newRevision)
			if err != nil {
				return nil, err
			}
			changes = append(changes, MarkdownChange{BlockNumber: rev.BlockNumber, Action: MarkdownBlockUpdated})
			continue
		}
		blk := model.Block{Type: plan.record.ContentType().Name(), AuthorID: author, Record: plan.record}
		if err := serializeRecord(&blk); err != nil {
			return nil, err
		}
		created, err := tx.Blocks().Create(ctx, uid, blk)
		if err != nil {
			return nil, fmt.Errorf("create block: %w", err)
		}
		changes = append(changes, MarkdownChange{BlockNumber: created.BlockNumber, Action: MarkdownBlockAdded})
	}
	return changes, nil
}

// ---------------------------------------------------------------------------
// Comparison helpers
// ---------------------------------------------------------------------------

// checkMarkdownHostMetadata rejects edits of host metadata: type, author,
// and parent must match the referenced revision exactly (absence is a
// value); the timestamp may be omitted or echoed unchanged.
func checkMarkdownHostMetadata(d markdown.Directive, revision model.Block) error {
	if d.Type != revision.Type {
		return fmt.Errorf("block type %q cannot be changed to %q", revision.Type, d.Type)
	}
	if d.Author != string(revision.AuthorID) {
		return fmt.Errorf("block author %q cannot be changed to %q", revision.AuthorID, d.Author)
	}
	stored := revision.ParentBlockNumber
	switch parent := d.Parent; {
	case parent == nil && stored != nil:
		return fmt.Errorf("block parent %d cannot be removed", *stored)
	case parent != nil && stored == nil:
		return fmt.Errorf("block has no parent: provenance cannot be assigned")
	case parent != nil && model.BlockNumber(*parent) != *stored:
		return fmt.Errorf("block parent %d cannot be changed to %d", *stored, *parent)
	}
	if ts := d.Timestamp; ts != nil && !ts.Equal(revision.Record.Timestamp()) {
		return fmt.Errorf("event timestamp %s cannot be changed to %s",
			revision.Record.Timestamp().Format(time.RFC3339Nano), ts.Format(time.RFC3339Nano))
	}
	return nil
}

// checkPreservedRecordMetadata rejects extension callbacks that changed
// host metadata: type association, event timestamp, key, or shape.
func checkPreservedRecordMetadata(before, after content.Record) error {
	if after == nil {
		return fmt.Errorf("update returned no record")
	}
	if after.ContentType() != before.ContentType() {
		return fmt.Errorf("update changed the content type")
	}
	if !after.Timestamp().Equal(before.Timestamp()) {
		return fmt.Errorf("update changed the event timestamp")
	}
	if _, ok := after.(content.ItemRecord); !ok {
		return fmt.Errorf("update returned a non-individual record %T", after)
	}
	beforeKeyed, beforeOK := before.(content.KeyedRecord)
	afterKeyed, afterOK := after.(content.KeyedRecord)
	if beforeOK != afterOK || (beforeOK && beforeKeyed.Key() != afterKeyed.Key()) {
		return fmt.Errorf("update changed the record key")
	}
	return nil
}

// markdownHeaderFields returns the header projection of record, or nil
// for types without the capability.
func markdownHeaderFields(record content.Record) (content.MarkdownFields, error) {
	headerType, ok := record.ContentType().(content.MarkdownHeader)
	if !ok {
		return nil, nil
	}
	fields, err := headerType.MarkdownHeader(record)
	if err != nil {
		return nil, fmt.Errorf("project header: %w", err)
	}
	return fields, nil
}

// equalMarkdownFields compares header fields by value; nil and empty are
// equal. Both sides use the JSON-compatible data model.
func equalMarkdownFields(a, b content.MarkdownFields) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	return reflect.DeepEqual(map[string]any(a), map[string]any(b))
}

// normalizeBody strips trailing line breaks: the renderer adds the
// framing line break, so they never carry content.
func normalizeBody(body string) string {
	return strings.TrimRight(body, "\r\n")
}

// ---------------------------------------------------------------------------
// Structured failures
// ---------------------------------------------------------------------------

// MarkdownPushReason is a stable, machine-readable push rejection reason.
// Transports map it to their status codes.
type MarkdownPushReason string

const (
	// PushInvalidMarkdown marks malformed framing (frontmatter, directives,
	// structured headers) or duplicate blocks.
	PushInvalidMarkdown MarkdownPushReason = "INVALID_MARKDOWN"
	// PushDocumentMismatch marks a frontmatter document ID differing from
	// the target document.
	PushDocumentMismatch MarkdownPushReason = "DOCUMENT_MISMATCH"
	PushDocumentNotFound MarkdownPushReason = "DOCUMENT_NOT_FOUND"
	// PushBlockNotFound marks a referenced block revision that does not
	// exist.
	PushBlockNotFound MarkdownPushReason = "BLOCK_NOT_FOUND"
	// PushHostMetadataChanged marks edited host metadata (type, author,
	// parent, timestamp) of an existing block.
	PushHostMetadataChanged MarkdownPushReason = "HOST_METADATA_CHANGED"
	// PushInvalidContent marks edited content rejected by the content type
	// (unknown type, header/body callback failure, schema validation).
	PushInvalidContent MarkdownPushReason = "INVALID_CONTENT"
	// PushUnsupportedEdit marks a header/body edit the content type has no
	// capability for, or direct creation of a generated-only type.
	PushUnsupportedEdit MarkdownPushReason = "UNSUPPORTED_EDIT"
	// PushGeneratedBlock marks an edit of an extension-managed block.
	PushGeneratedBlock MarkdownPushReason = "GENERATED_BLOCK"
	// PushStaleRevision marks an edit of a block whose referenced revision
	// is no longer current.
	PushStaleRevision MarkdownPushReason = "STALE_REVISION"
	// PushDeletedBlock marks an edit of a block deleted since.
	PushDeletedBlock MarkdownPushReason = "DELETED_BLOCK"
	// PushInvalidOutput marks a content type misbehaving during the push
	// (changed host metadata, failing projections).
	PushInvalidOutput MarkdownPushReason = "INVALID_EXTENSION_OUTPUT"
	// PushRenderFailed marks a failure rendering the refreshed document.
	PushRenderFailed MarkdownPushReason = "RENDER_FAILED"
)

// MarkdownPushError is a structured, transport-neutral Markdown push
// rejection. Inspect it with errors.As; a wrapped *content.ValidationError
// (if any) carries stage and field violations.
type MarkdownPushError struct {
	Reason      MarkdownPushReason
	DocumentUID model.DocumentUID
	// BlockNumber is the existing block concerned (0 for new blocks and
	// document-level failures).
	BlockNumber model.BlockNumber
	// BlockIndex is the 1-based position of the block section in the
	// submitted Markdown (0 outside blocks).
	BlockIndex int
	// Line is the 1-based source line (0 when unknown).
	Line        int
	ContentType string
	Err         error
}

func (e *MarkdownPushError) Error() string {
	var out strings.Builder
	fmt.Fprintf(&out, "document %q", e.DocumentUID)
	switch {
	case e.BlockNumber > 0:
		fmt.Fprintf(&out, " block %d", e.BlockNumber)
	case e.BlockIndex > 0:
		fmt.Fprintf(&out, " block section #%d", e.BlockIndex)
	}
	if e.Line > 0 {
		fmt.Fprintf(&out, " (line %d)", e.Line)
	}
	fmt.Fprintf(&out, ": %s", e.Reason)
	if e.Err != nil {
		fmt.Fprintf(&out, ": %v", e.Err)
	}
	return out.String()
}

func (e *MarkdownPushError) Unwrap() error { return e.Err }

// Validation returns the wrapped validation error, if any.
func (e *MarkdownPushError) Validation() *content.ValidationError {
	var validation *content.ValidationError
	if errors.As(e.Err, &validation) {
		return validation
	}
	return nil
}
