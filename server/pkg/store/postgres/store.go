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

func (t *pgTx) Documents() store.DocumentsCollection { return &pgDocuments{tx: t.tx} }
func (t *pgTx) Blocks() store.BlocksCollection       { return &pgBlocks{tx: t.tx} }
func (t *pgTx) Commit() error                        { return t.tx.Commit() }
func (t *pgTx) Rollback() error                      { return t.tx.Rollback() }

// ---------------------------------------------------------------------------
// DocumentsCollection
// ---------------------------------------------------------------------------

type pgDocuments struct {
	tx *sql.Tx
}

var _ store.DocumentsCollection = &pgDocuments{}

func (d *pgDocuments) Create(ctx context.Context, doc model.Document) (model.Document, error) {
	row := d.tx.QueryRowContext(ctx,
		`INSERT INTO documents (status, title, namespace)
		 VALUES ($1, $2, $3)
		 RETURNING uid, status, title, namespace, created_at`,
		cond(doc.Status == "", string(model.ActiveDocument), string(doc.Status)),
		doc.Title,
		cond(doc.Namespace == "", "default", doc.Namespace),
	)
	var out model.Document
	var status string
	if err := row.Scan(&out.Uid, &status, &out.Title, &out.Namespace, &out.CreatedAt); err != nil {
		return model.Document{}, fmt.Errorf("insert document: %w", err)
	}
	out.Status = model.DocumentStatus(status)
	return out, nil
}

func (d *pgDocuments) Get(ctx context.Context, uid model.DocumentUID) (model.Document, error) {
	row := d.tx.QueryRowContext(ctx,
		`SELECT uid, status, title, namespace, created_at FROM documents WHERE uid = $1`, uid)
	var out model.Document
	var status string
	if err := row.Scan(&out.Uid, &status, &out.Title, &out.Namespace, &out.CreatedAt); err != nil {
		return model.Document{}, fmt.Errorf("get document %s: %w", uid, err)
	}
	out.Status = model.DocumentStatus(status)
	return out, nil
}

func (d *pgDocuments) List(ctx context.Context) ([]model.Document, error) {
	rows, err := d.tx.QueryContext(ctx,
		`SELECT uid, status, title, namespace, created_at FROM documents ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var documents []model.Document
	for rows.Next() {
		var doc model.Document
		var status string
		if err := rows.Scan(&doc.Uid, &status, &doc.Title, &doc.Namespace, &doc.CreatedAt); err != nil {
			return nil, err
		}
		doc.Status = model.DocumentStatus(status)
		documents = append(documents, doc)
	}
	return documents, rows.Err()
}

// ---------------------------------------------------------------------------
// BlocksCollection
// ---------------------------------------------------------------------------

type pgBlocks struct {
	tx *sql.Tx
}

var _ store.BlocksCollection = &pgBlocks{}

func (b *pgBlocks) Create(ctx context.Context, documentUid model.DocumentUID, blk model.Block) (model.Block, error) {
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
		revisionExpr = `(SELECT COALESCE(MAX(block_number), 0) + 1 FROM blocks WHERE document_uid = $1), 1`
		args = []any{string(documentUid), blk.AuthorID, blk.Type, cond(blk.Status == "", model.PublishedBlock, string(blk.Status)), sc.Data}
	} else {
		// New revision of existing block.
		revisionExpr = `$6, (SELECT COALESCE(MAX(revision_number), 0) + 1 FROM blocks WHERE document_uid = $1 AND block_number = $6)`
		args = []any{string(documentUid), blk.AuthorID, blk.Type, cond(blk.Status == "", model.PublishedBlock, string(blk.Status)), sc.Data, blk.BlockNumber}
	}

	query := fmt.Sprintf(
		`INSERT INTO blocks (document_uid, block_number, revision_number, author_id, type, status, serialized_content)
		 VALUES ($1, %s, $2, $3, $4, $5)
		 RETURNING block_number, revision_number, author_id, type, status, serialized_content, created_at`,
		revisionExpr,
	)

	row := b.tx.QueryRowContext(ctx, query, args...)
	return scanBlock(documentUid, row)
}

func (b *pgBlocks) Get(ctx context.Context, documentUid model.DocumentUID, blockNumber model.BlockNumber, revisionNumber model.RevisionNumber) (model.Block, error) {
	var row *sql.Row
	if revisionNumber == 0 {
		row = b.tx.QueryRowContext(ctx,
			`SELECT block_number, revision_number, author_id, type, status, serialized_content, created_at
			 FROM blocks WHERE document_uid = $1 AND block_number = $2
			 ORDER BY revision_number DESC LIMIT 1`,
			documentUid, blockNumber)
	} else {
		row = b.tx.QueryRowContext(ctx,
			`SELECT block_number, revision_number, author_id, type, status, serialized_content, created_at
			 FROM blocks WHERE document_uid = $1 AND block_number = $2 AND revision_number = $3`,
			documentUid, blockNumber, revisionNumber)
	}
	return scanBlock(documentUid, row)
}

func (b *pgBlocks) List(ctx context.Context, documentUid model.DocumentUID) ([]model.Block, error) {
	rows, err := b.tx.QueryContext(ctx,
		`SELECT block_number, revision_number, author_id, type, status, serialized_content, created_at
		 FROM blocks WHERE document_uid = $1
		 ORDER BY block_number, revision_number`,
		documentUid)
	if err != nil {
		return nil, err
	}
	return scanBlocks(documentUid, rows)
}

func (b *pgBlocks) ListRevisions(ctx context.Context, documentUid model.DocumentUID, blockNumber model.BlockNumber) ([]model.Block, error) {
	rows, err := b.tx.QueryContext(ctx,
		`SELECT block_number, revision_number, author_id, type, status, serialized_content, created_at
		 FROM blocks WHERE document_uid = $1 AND block_number = $2
		 ORDER BY revision_number`,
		documentUid, blockNumber)
	if err != nil {
		return nil, err
	}
	return scanBlocks(documentUid, rows)
}

func (b *pgBlocks) ListLatest(ctx context.Context, documentUid model.DocumentUID) ([]model.Block, error) {
	rows, err := b.tx.QueryContext(ctx,
		`SELECT DISTINCT ON (block_number)
		        block_number, revision_number, author_id, type, status, serialized_content, created_at
		 FROM blocks WHERE document_uid = $1
		 ORDER BY block_number, revision_number DESC`,
		documentUid)
	if err != nil {
		return nil, err
	}
	return scanBlocks(documentUid, rows)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func scanBlock(documentUid model.DocumentUID, row *sql.Row) (model.Block, error) {
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

func scanBlocks(documentUid model.DocumentUID, rows *sql.Rows) ([]model.Block, error) {
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
