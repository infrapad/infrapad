package controller_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
	"github.com/infrapad/infrapad/server/pkg/extensions/prometheus"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store"
)

// ---------------------------------------------------------------------------
// In-memory fake store
// ---------------------------------------------------------------------------

// fakeStore is a minimal transactional in-memory store.Store used to check
// the controller's record handling (validation, serialization,
// deserialization, timestamps) without a database.
type fakeStore struct {
	documents map[model.DocumentUID]model.Document
	blocks    map[model.DocumentUID][]model.Block
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		documents: map[model.DocumentUID]model.Document{},
		blocks:    map[model.DocumentUID][]model.Block{},
	}
}

func (s *fakeStore) Begin(ctx context.Context) (store.Tx, error) { return s, nil }
func (s *fakeStore) BeginReadOnly(ctx context.Context) (store.Tx, error) {
	return s, nil
}
func (s *fakeStore) Commit() error   { return nil }
func (s *fakeStore) Rollback() error { return nil }

func (s *fakeStore) Documents() store.DocumentsCollection { return (*fakeDocuments)(s) }
func (s *fakeStore) Blocks() store.BlocksCollection       { return (*fakeBlocks)(s) }

type fakeDocuments fakeStore

func (d *fakeDocuments) Create(ctx context.Context, doc model.Document) (model.Document, error) {
	uid := model.DocumentUID("doc-1")
	created := doc
	created.Uid = uid
	created.Status = model.ActiveDocument
	created.CreatedAt = time.Now().UTC()
	(*fakeStore)(d).documents[uid] = created
	return created, nil
}

func (d *fakeDocuments) Get(ctx context.Context, uid model.DocumentUID) (model.Document, error) {
	doc, ok := (*fakeStore)(d).documents[uid]
	if !ok {
		return model.Document{}, errNotFound
	}
	return doc, nil
}

func (d *fakeDocuments) List(ctx context.Context) ([]model.Document, error) {
	var out []model.Document
	for _, doc := range (*fakeStore)(d).documents {
		out = append(out, doc)
	}
	return out, nil
}

func (d *fakeDocuments) Lock(ctx context.Context, uid model.DocumentUID) error {
	if _, err := d.Get(ctx, uid); err != nil {
		return err
	}
	return nil
}

type fakeBlocks fakeStore

func (b *fakeBlocks) Create(ctx context.Context, documentUid model.DocumentUID, blk model.Block) (model.Block, error) {
	// Match the postgres default for omitted statuses.
	if blk.Status == "" {
		blk.Status = model.PublishedBlock
	}
	blocks := (*fakeStore)(b).blocks[documentUid]
	if blk.BlockNumber == 0 {
		blk.BlockNumber = model.BlockNumber(len(blocks) + 1)
		blk.RevisionNumber = 1
	} else {
		max := 0
		for _, existing := range blocks {
			if int(existing.BlockNumber) == int(blk.BlockNumber) && int(existing.RevisionNumber) > max {
				max = int(existing.RevisionNumber)
			}
		}
		blk.RevisionNumber = model.RevisionNumber(max + 1)
	}
	blk.CreatedAt = time.Now().UTC()
	(*fakeStore)(b).blocks[documentUid] = append(blocks, blk)
	return blk, nil
}

func (b *fakeBlocks) Get(ctx context.Context, documentUid model.DocumentUID, blockNumber model.BlockNumber, revisionNumber model.RevisionNumber) (model.Block, error) {
	var latest *model.Block
	for i, blk := range (*fakeStore)(b).blocks[documentUid] {
		if blk.BlockNumber != blockNumber {
			continue
		}
		if revisionNumber != 0 && blk.RevisionNumber != revisionNumber {
			continue
		}
		if latest == nil || blk.RevisionNumber > latest.RevisionNumber {
			latest = &(*fakeStore)(b).blocks[documentUid][i]
		}
	}
	if latest == nil {
		return model.Block{}, errNotFound
	}
	return *latest, nil
}

func (b *fakeBlocks) List(ctx context.Context, documentUid model.DocumentUID) ([]model.Block, error) {
	return append([]model.Block{}, (*fakeStore)(b).blocks[documentUid]...), nil
}

func (b *fakeBlocks) ListRevisions(ctx context.Context, documentUid model.DocumentUID, blockNumber model.BlockNumber) ([]model.Block, error) {
	var out []model.Block
	for _, blk := range (*fakeStore)(b).blocks[documentUid] {
		if blk.BlockNumber == blockNumber {
			out = append(out, blk)
		}
	}
	return out, nil
}

func (b *fakeBlocks) ListLatest(ctx context.Context, documentUid model.DocumentUID) ([]model.Block, error) {
	latest := map[model.BlockNumber]model.Block{}
	for _, blk := range (*fakeStore)(b).blocks[documentUid] {
		if current, ok := latest[blk.BlockNumber]; !ok || blk.RevisionNumber > current.RevisionNumber {
			latest[blk.BlockNumber] = blk
		}
	}
	var out []model.Block
	for n := 1; n <= len(latest); n++ {
		out = append(out, latest[model.BlockNumber(n)])
	}
	return out, nil
}

type notFoundError struct{}

func (notFoundError) Error() string { return "not found" }

var errNotFound = notFoundError{}

// ---------------------------------------------------------------------------
// Controller tests
// ---------------------------------------------------------------------------

func newTestController(t *testing.T) (*controller.Controller, *fakeStore) {
	t.Helper()
	registry, err := content.NewRegistry(core.ContentTypes(), prometheus.ContentTypes())
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}
	s := newFakeStore()
	return controller.New(s, registry), s
}

// TestRecordPersistenceRoundTrip checks that a submitted inner content value
// is persisted as a full host record envelope and restored losslessly.
func TestRecordPersistenceRoundTrip(t *testing.T) {
	ctx := context.Background()
	ctrl, _ := newTestController(t)

	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "incident"})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	blk, err := ctrl.AddBlock(ctx, doc.Uid, model.Block{
		Type:    prometheus.AlertsMatcherTypeName,
		Content: json.RawMessage(`{"labels_matchers":[{"alertname":["CrashLoopBackOff"]}],"since":"2026-10-25T10:00:00Z"}`),
	})
	if err != nil {
		t.Fatalf("add block: %v", err)
	}
	if _, ok := blk.Record.(content.ItemRecord); !ok {
		t.Fatal("created block carries no item record")
	}
	// The persisted envelope is the full host record, not the flat inner content.
	var envelope map[string]any
	if err := json.Unmarshal(blk.SerializedRecord.Data, &envelope); err != nil {
		t.Fatalf("serialized record is not JSON: %v", err)
	}
	if envelope["timestamp"] == nil || envelope["content"] == nil {
		t.Fatalf("serialized record is not a host record envelope: %v", envelope)
	}
	if blk.SerializedRecord.Type != prometheus.AlertsMatcherTypeName || blk.Type != prometheus.AlertsMatcherTypeName {
		t.Fatalf("unexpected type association: %q / %q", blk.SerializedRecord.Type, blk.Type)
	}

	fetched, err := ctrl.GetBlock(ctx, doc.Uid, blk.BlockNumber, 0)
	if err != nil {
		t.Fatalf("get block: %v", err)
	}
	view, err := content.ItemView[prometheus.AlertsMatcher](fetched.Record)
	if err != nil {
		t.Fatalf("restored record: %v", err)
	}
	if len(view.ContentValue.LabelsMatchers) != 1 ||
		view.ContentValue.LabelsMatchers[0]["alertname"][0] != "CrashLoopBackOff" {
		t.Fatalf("unexpected restored content: %#v", view.ContentValue)
	}
	if !view.ContentValue.Since.Equal(time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected since: %v", view.ContentValue.Since)
	}
}

// TestTimestampPreservationAcrossRevisions checks that an ordinary block's
// record timestamp is initialized from block creation time and preserved
// on updates.
func TestTimestampPreservationAcrossRevisions(t *testing.T) {
	ctx := context.Background()
	ctrl, _ := newTestController(t)

	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "incident"})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	before := time.Now().UTC()
	blk, err := ctrl.AddBlock(ctx, doc.Uid, model.Block{
		Type:    core.MarkdownTypeName,
		Content: json.RawMessage(`{"text":"first"}`),
	})
	if err != nil {
		t.Fatalf("add block: %v", err)
	}
	created := blk.Record.Timestamp()
	if created.Before(before.Add(-time.Minute)) || created.After(time.Now().UTC().Add(time.Minute)) {
		t.Fatalf("record timestamp not initialized from creation time: %v", created)
	}

	updated, err := ctrl.UpdateBlock(ctx, doc.Uid, blk.BlockNumber, model.Block{
		Type:    core.MarkdownTypeName,
		Content: json.RawMessage(`{"text":"second"}`),
	})
	if err != nil {
		t.Fatalf("update block: %v", err)
	}
	if !updated.Record.Timestamp().Equal(created) {
		t.Fatalf("record timestamp not preserved: %v != %v", updated.Record.Timestamp(), created)
	}
	view, err := content.ItemView[core.Markdown](updated.Record)
	if err != nil || view.ContentValue.Text != "second" {
		t.Fatalf("unexpected updated content: %#v (%v)", view.ContentValue, err)
	}
}

// TestStrictInputRejection checks that legacy type names, normalized-away
// field spellings, unknown fields, and missing required fields are rejected
// as invalid input without changing the document.
func TestStrictInputRejection(t *testing.T) {
	ctx := context.Background()
	ctrl, _ := newTestController(t)

	doc, err := ctrl.CreateDocument(ctx, model.Document{Title: "incident"})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	cases := []struct {
		name     string
		typeName string
		content  string
	}{
		{"legacy markdown type", "markdown", `{"text":"x"}`},
		{"legacy alerts_matcher type", "alerts_matcher", `{"labels_matchers":[],"since":"2026-10-25T10:00:00Z"}`},
		{"unregistered type", "Some/Unknown", `{"text":"x"}`},
		{"uppercase alert fields", prometheus.AlertsMatcherTypeName, `{"LabelsMatchers":[{"alertname":["x"]}],"Since":"2026-10-25T10:00:00Z"}`},
		{"missing required since", prometheus.AlertsMatcherTypeName, `{"labels_matchers":[{"alertname":["x"]}]}`},
		{"unknown field", core.MarkdownTypeName, `{"text":"x","bogus":1}`},
		{"missing required text", core.MarkdownTypeName, `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ctrl.AddBlock(ctx, doc.Uid, model.Block{
				Type:    tc.typeName,
				Content: json.RawMessage(tc.content),
			})
			if err == nil {
				t.Fatal("expected rejection")
			}
			if !controller.IsInvalidInput(err) {
				t.Fatalf("expected invalid input, got: %v", err)
			}
			blocks, err := ctrl.ListBlocks(ctx, doc.Uid)
			if err != nil {
				t.Fatalf("list blocks: %v", err)
			}
			if len(blocks) != 0 {
				t.Fatalf("rejected submission changed the document: %d blocks", len(blocks))
			}
		})
	}
}
