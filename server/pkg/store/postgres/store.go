package postgres

// TODO: this file contains a terrible and unmaintanable slop that needs
// to be revisited and cleaned up. In particular pgBlocks is juicy.
import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/infrapad/infrapad/server/pkg/model"
	"github.com/infrapad/infrapad/server/pkg/store"
)

//go:embed migrations/*.sql
var migrations embed.FS

func OpenPG(connStr string) (*sql.DB, error) {
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	return db, nil
}

type Store struct {
	db *sql.DB
}

var _ store.Store = Store{}

// NewStore creates a new postgres-backed Store from an already-opened *sql.DB.
func NewStore(db *sql.DB) Store {
	return Store{db: db}
}

func (s Store) Begin(ctx context.Context) (store.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &pgTx{tx: tx}, nil
}

func (s Store) BeginReadOnly(ctx context.Context) (store.Tx, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	return &pgTx{tx: tx}, nil
}

// ---------------------------------------------------------------------------
// Tx
// ---------------------------------------------------------------------------

type pgTx struct {
	tx *sql.Tx
}

var _ store.Tx = &pgTx{}

func (t *pgTx) Docs() store.DocsCollection     { return &pgDocs{tx: t.tx} }
func (t *pgTx) Blocks() store.BlocksCollection { return &pgBlocks{tx: t.tx} }
func (t *pgTx) Commit() error                  { return t.tx.Commit() }
func (t *pgTx) Rollback() error                { return t.tx.Rollback() }

// ---------------------------------------------------------------------------
// DocsCollection
// ---------------------------------------------------------------------------

type pgDocs struct {
	tx *sql.Tx
}

var _ store.DocsCollection = &pgDocs{}

func (d *pgDocs) Create(ctx context.Context, doc model.Doc) (model.Doc, error) {
	row := d.tx.QueryRowContext(ctx,
		`INSERT INTO docs (status, title, namespace)
		 VALUES ($1, $2, $3)
		 RETURNING uid, status, title, namespace, created_at`,
		cond(doc.Status == "", string(model.ActiveDoc), string(doc.Status)),
		doc.Title,
		cond(doc.Namespace == "", "default", doc.Namespace),
	)
	var out model.Doc
	var status string
	if err := row.Scan(&out.Uid, &status, &out.Title, &out.Namespace, &out.CreatedAt); err != nil {
		return model.Doc{}, fmt.Errorf("insert doc: %w", err)
	}
	out.Status = model.DocStatus(status)
	return out, nil
}

func (d *pgDocs) Get(ctx context.Context, uid model.DocUID) (model.Doc, error) {
	row := d.tx.QueryRowContext(ctx,
		`SELECT uid, status, title, namespace, created_at FROM docs WHERE uid = $1`, uid)
	var out model.Doc
	var status string
	if err := row.Scan(&out.Uid, &status, &out.Title, &out.Namespace, &out.CreatedAt); err != nil {
		return model.Doc{}, fmt.Errorf("get doc %s: %w", uid, err)
	}
	out.Status = model.DocStatus(status)
	return out, nil
}

func (d *pgDocs) List(ctx context.Context) ([]model.Doc, error) {
	rows, err := d.tx.QueryContext(ctx,
		`SELECT uid, status, title, namespace, created_at FROM docs ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var docs []model.Doc
	for rows.Next() {
		var doc model.Doc
		var status string
		if err := rows.Scan(&doc.Uid, &status, &doc.Title, &doc.Namespace, &doc.CreatedAt); err != nil {
			return nil, err
		}
		doc.Status = model.DocStatus(status)
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}

// ---------------------------------------------------------------------------
// BlocksCollection
// ---------------------------------------------------------------------------

type pgBlocks struct {
	tx *sql.Tx
}

var _ store.BlocksCollection = &pgBlocks{}

func (b *pgBlocks) Create(ctx context.Context, docUid model.DocUID, blk model.Block) (model.Block, error) {
	sc := blk.SerializedContent
	if blk.Content != nil {
		var err error
		sc, err = blk.Content.Serialize()
		if err != nil {
			return model.Block{}, fmt.Errorf("serialize block content: %w", err)
		}
	}

	var revisionExpr string
	var args []any

	if blk.BlockNumber == 0 {
		// New block – assign next block_number, revision = 1.
		revisionExpr = `(SELECT COALESCE(MAX(block_number), 0) + 1 FROM blocks WHERE doc_uid = $1), 1`
		args = []any{string(docUid), blk.AuthorID, blk.Type, cond(blk.Status == "", model.PublishedBlock, string(blk.Status)), sc.Data}
	} else {
		// New revision of existing block.
		revisionExpr = `$6, (SELECT COALESCE(MAX(revision_number), 0) + 1 FROM blocks WHERE doc_uid = $1 AND block_number = $6)`
		args = []any{string(docUid), blk.AuthorID, blk.Type, cond(blk.Status == "", model.PublishedBlock, string(blk.Status)), sc.Data, blk.BlockNumber}
	}

	query := fmt.Sprintf(
		`INSERT INTO blocks (doc_uid, block_number, revision_number, author_id, type, status, serialized_content)
		 VALUES ($1, %s, $2, $3, $4, $5)
		 RETURNING block_number, revision_number, author_id, type, status, serialized_content, created_at`,
		revisionExpr,
	)

	row := b.tx.QueryRowContext(ctx, query, args...)
	return scanBlock(docUid, row)
}

func (b *pgBlocks) Get(ctx context.Context, docUid model.DocUID, blockNumber model.BlockNumber, revisionNumber model.RevisionNumber) (model.Block, error) {
	var row *sql.Row
	if revisionNumber == 0 {
		row = b.tx.QueryRowContext(ctx,
			`SELECT block_number, revision_number, author_id, type, status, serialized_content, created_at
			 FROM blocks WHERE doc_uid = $1 AND block_number = $2
			 ORDER BY revision_number DESC LIMIT 1`,
			docUid, blockNumber)
	} else {
		row = b.tx.QueryRowContext(ctx,
			`SELECT block_number, revision_number, author_id, type, status, serialized_content, created_at
			 FROM blocks WHERE doc_uid = $1 AND block_number = $2 AND revision_number = $3`,
			docUid, blockNumber, revisionNumber)
	}
	return scanBlock(docUid, row)
}

func (b *pgBlocks) List(ctx context.Context, docUid model.DocUID) ([]model.Block, error) {
	rows, err := b.tx.QueryContext(ctx,
		`SELECT block_number, revision_number, author_id, type, status, serialized_content, created_at
		 FROM blocks WHERE doc_uid = $1
		 ORDER BY block_number, revision_number`,
		docUid)
	if err != nil {
		return nil, err
	}
	return scanBlocks(docUid, rows)
}

func (b *pgBlocks) ListRevisions(ctx context.Context, docUid model.DocUID, blockNumber model.BlockNumber) ([]model.Block, error) {
	rows, err := b.tx.QueryContext(ctx,
		`SELECT block_number, revision_number, author_id, type, status, serialized_content, created_at
		 FROM blocks WHERE doc_uid = $1 AND block_number = $2
		 ORDER BY revision_number`,
		docUid, blockNumber)
	if err != nil {
		return nil, err
	}
	return scanBlocks(docUid, rows)
}

func (b *pgBlocks) ListLatest(ctx context.Context, docUid model.DocUID) ([]model.Block, error) {
	rows, err := b.tx.QueryContext(ctx,
		`SELECT DISTINCT ON (block_number)
		        block_number, revision_number, author_id, type, status, serialized_content, created_at
		 FROM blocks WHERE doc_uid = $1
		 ORDER BY block_number, revision_number DESC`,
		docUid)
	if err != nil {
		return nil, err
	}
	return scanBlocks(docUid, rows)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func scanBlock(docUid model.DocUID, row *sql.Row) (model.Block, error) {
	var blk model.Block
	var status, typ string
	var data []byte
	if err := row.Scan(&blk.BlockNumber, &blk.RevisionNumber, &blk.AuthorID,
		&typ, &status, &data, &blk.CreatedAt); err != nil {
		return model.Block{}, err
	}
	blk.Type = typ
	blk.Status = model.BlockStatus(status)
	blk.SerializedContent = model.SerializedContent{Type: typ, Data: data}
	content, err := model.DeserializeBlockContent(blk.SerializedContent)
	if err != nil {
		return model.Block{}, fmt.Errorf("deserialize block content: %w", err)
	}
	blk.Content = content
	return blk, nil
}

func scanBlocks(docUid model.DocUID, rows *sql.Rows) ([]model.Block, error) {
	defer rows.Close()
	var blocks []model.Block
	for rows.Next() {
		var blk model.Block
		var status, typ string
		var data []byte
		if err := rows.Scan(&blk.BlockNumber, &blk.RevisionNumber, &blk.AuthorID,
			&typ, &status, &data, &blk.CreatedAt); err != nil {
			return nil, err
		}
		blk.Type = typ
		blk.Status = model.BlockStatus(status)
		blk.SerializedContent = model.SerializedContent{Type: typ, Data: data}
		content, err := model.DeserializeBlockContent(blk.SerializedContent)
		if err != nil {
			return nil, fmt.Errorf("deserialize block content: %w", err)
		}
		blk.Content = content
		blocks = append(blocks, blk)
	}
	return blocks, rows.Err()
}

func cond[T any](c bool, ifTrue, ifFalse T) T {
	if c {
		return ifTrue
	}
	return ifFalse
}
