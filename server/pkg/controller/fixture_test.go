package controller_test

// Package-local test fixture: one controller over a fresh in-memory store
// with the Markdown, Prometheus, Slack, and AI-agent types registered.
// Tests use the exposed dependencies directly; the fixture has no methods
// wrapping controller operations, store inspection, or record construction.

import (
	"context"
	"testing"
	"time"

	"github.com/infrapad/infrapad/server/internal/testutil"
	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/controller"
	"github.com/infrapad/infrapad/server/pkg/extensions/aiagent"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
	"github.com/infrapad/infrapad/server/pkg/extensions/prometheus"
	"github.com/infrapad/infrapad/server/pkg/extensions/slack"
	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store"
)

// fixture owns the controller, registry, fake store, and concrete content
// type instances registered with that controller. Each test builds its
// own fixture; nothing is shared between tests.
type fixture struct {
	ctrl     *controller.Controller
	registry *content.Registry
	store    *fakeStore

	markdownType     *core.MarkdownType
	alertsType       *prometheus.AlertsMatcherType
	agentType        *aiagent.MessageType
	conversationType *slack.ConversationType
	threadType       *slack.ThreadType
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		store:            newFakeStore(),
		markdownType:     core.NewMarkdownType(),
		alertsType:       prometheus.NewAlertsMatcherType(),
		agentType:        aiagent.NewMessageType(),
		conversationType: slack.NewConversationType(),
	}
	f.threadType = slack.NewThreadType(f.conversationType, f.agentType)
	registry, err := content.NewRegistry([]content.ContentType{
		f.markdownType, f.alertsType, f.agentType, f.threadType, f.conversationType,
	})
	testutil.NoErr(t, err)
	f.registry = registry
	f.ctrl = controller.New(f.store, f.registry)
	return f
}

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
