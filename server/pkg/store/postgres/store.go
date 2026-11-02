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

// Lock takes an exclusive row lock on the document for the rest of the
// transaction, serializing block mutations within the document. It must
// be acquired before any block validation or write.
func (d *pgDocuments) Lock(ctx context.Context, uid model.DocumentUID) error {
	var out model.DocumentUID
	if err := d.tx.QueryRowContext(ctx,
		`SELECT uid FROM documents WHERE uid = $1 FOR UPDATE`, uid).Scan(&out); err != nil {
		return fmt.Errorf("lock document %s: %w", uid, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// BlocksCollection
// ---------------------------------------------------------------------------

type pgBlocks struct {
	tx *sql.Tx
}

var _ store.BlocksCollection = &pgBlocks{}

func (b *pgBlocks) Create(ctx context.Context, documentUid model.DocumentUID, blk model.Block) (model.Block, error) {
	// The store is content-type agnostic: it persists the serialized host
	// record envelope produced by the controller as-is.
	sc := blk.SerializedRecord
	parent, err := parentValue(blk.ParentBlockNumber)
	if err != nil {
		return model.Block{}, err
	}

	var revisionExpr, parentParam string
	var args []any

	if blk.BlockNumber == 0 {
		// New block – assign next block_number, revision = 1.
		revisionExpr = `(SELECT COALESCE(MAX(block_number), 0) + 1 FROM blocks WHERE document_uid = $1), 1`
		parentParam = `$6`
		args = []any{string(documentUid), blk.AuthorID, blk.Type, cond(blk.Status == "", model.PublishedBlock, string(blk.Status)), sc.Data, parent}
	} else {
		// New revision of existing block.
		revisionExpr = `$6, (SELECT COALESCE(MAX(revision_number), 0) + 1 FROM blocks WHERE document_uid = $1 AND block_number = $6)`
		parentParam = `$7`
		args = []any{string(documentUid), blk.AuthorID, blk.Type, cond(blk.Status == "", model.PublishedBlock, string(blk.Status)), sc.Data, blk.BlockNumber, parent}
	}

	query := fmt.Sprintf(
		`INSERT INTO blocks (document_uid, block_number, revision_number, author_id, type, status, serialized_content, parent_block_number)
		 VALUES ($1, %s, $2, $3, $4, $5, %s)
		 RETURNING block_number, revision_number, author_id, type, status, serialized_content, created_at, parent_block_number`,
		revisionExpr, parentParam,
	)

	row := b.tx.QueryRowContext(ctx, query, args...)
	return scanBlock(documentUid, row)
}

func (b *pgBlocks) Get(ctx context.Context, documentUid model.DocumentUID, blockNumber model.BlockNumber, revisionNumber model.RevisionNumber) (model.Block, error) {
	var row *sql.Row
	if revisionNumber == 0 {
		row = b.tx.QueryRowContext(ctx,
			`SELECT block_number, revision_number, author_id, type, status, serialized_content, created_at, parent_block_number
			 FROM blocks WHERE document_uid = $1 AND block_number = $2
			 ORDER BY revision_number DESC LIMIT 1`,
			documentUid, blockNumber)
	} else {
		row = b.tx.QueryRowContext(ctx,
			`SELECT block_number, revision_number, author_id, type, status, serialized_content, created_at, parent_block_number
			 FROM blocks WHERE document_uid = $1 AND block_number = $2 AND revision_number = $3`,
			documentUid, blockNumber, revisionNumber)
	}
	return scanBlock(documentUid, row)
}

func (b *pgBlocks) List(ctx context.Context, documentUid model.DocumentUID) ([]model.Block, error) {
	rows, err := b.tx.QueryContext(ctx,
		`SELECT block_number, revision_number, author_id, type, status, serialized_content, created_at, parent_block_number
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
		`SELECT block_number, revision_number, author_id, type, status, serialized_content, created_at, parent_block_number
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
		        block_number, revision_number, author_id, type, status, serialized_content, created_at, parent_block_number
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
	var parent sql.NullInt64
	if err := row.Scan(&blk.BlockNumber, &blk.RevisionNumber, &blk.AuthorID,
		&typ, &status, &data, &blk.CreatedAt, &parent); err != nil {
		return model.Block{}, err
	}
	blk.Type = typ
	blk.Status = model.BlockStatus(status)
	setScannedParent(&blk, parent)
	// The deserialized record is restored by the controller through the
	// content registry; the store keeps only the serialized envelope.
	blk.SerializedRecord = model.SerializedRecord{Type: typ, Data: data}
	return blk, nil
}

func scanBlocks(documentUid model.DocumentUID, rows *sql.Rows) ([]model.Block, error) {
	defer rows.Close()
	var blocks []model.Block
	for rows.Next() {
		var blk model.Block
		var status, typ string
		var data []byte
		var parent sql.NullInt64
		if err := rows.Scan(&blk.BlockNumber, &blk.RevisionNumber, &blk.AuthorID,
			&typ, &status, &data, &blk.CreatedAt, &parent); err != nil {
			return nil, err
		}
		blk.Type = typ
		blk.Status = model.BlockStatus(status)
		setScannedParent(&blk, parent)
		blk.SerializedRecord = model.SerializedRecord{Type: typ, Data: data}
		blocks = append(blocks, blk)
	}
	return blocks, rows.Err()
}

// parentValue converts the optional model parent into a typed driver
// value (sql.NullInt64 so Postgres can infer the parameter type even
// when the value is NULL), rejecting non-positive numbers (defense in
// depth: the controller is the primary validation point).
func parentValue(parent *model.BlockNumber) (any, error) {
	if parent == nil {
		return sql.NullInt64{}, nil
	}
	if *parent <= 0 {
		return nil, fmt.Errorf("block parent %d: must be positive", *parent)
	}
	return sql.NullInt64{Int64: int64(*parent), Valid: true}, nil
}

// setScannedParent restores the nullable parent column onto blk.
func setScannedParent(blk *model.Block, parent sql.NullInt64) {
	if parent.Valid {
		p := model.BlockNumber(parent.Int64)
		blk.ParentBlockNumber = &p
	}
}

func cond[T any](c bool, ifTrue, ifFalse T) T {
	if c {
		return ifTrue
	}
	return ifFalse
}
