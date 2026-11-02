package model

import (
	"encoding/json"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
)

type Document struct {
	Uid       DocumentUID
	Status    DocumentStatus
	Title     string
	Namespace string
	CreatedAt time.Time
	// Blocks contain the latest revision of all blocks that belong to the document.
	Blocks []Block
}

type DocumentUID string

type DocumentStatus string

const (
	ActiveDocument   DocumentStatus = "active"
	ArchivedDocument DocumentStatus = "archived"
)

// Block is one revision of a block in a document.
//
// Content handling is split across three representations, each owned by a
// different layer:
//
//   - Content is the raw inner content JSON submitted through the public
//     API ({"text": "..."} for Core/Markdown). Producers never supply host
//     record envelopes or timestamps. The controller validates it against
//     the registered content type and builds the host record from it.
//   - Record is the deserialized host record (content.Record). The
//     controller converts between Content and Record.
//   - SerializedRecord is the serialized full host record envelope. The
//     controller produces it for persistence; the store persists it
//     as-is, without any content-type knowledge.
type Block struct {
	// The unique identifier of the block inside the document.
	// Monotonically increasing, staring from 1.
	BlockNumber BlockNumber
	// The revision number of the block.
	// Monotonically increasing, staring from 1.
	// The block with the highest RevisionNumber is always considered
	// the current version of the block with the corresponding BlockNumber.
	RevisionNumber RevisionNumber
	AuthorID       AuthorID
	// Type is the canonical namespaced content type name
	// (e.g. "Core/Markdown").
	Type      string
	Status    BlockStatus
	CreatedAt time.Time

	// ParentBlockNumber identifies the immediate generating block in
	// the same document; nil means a root block. It is host-owned
	// provenance metadata, kept outside the content and the serialized
	// host record envelope: it is assigned only when the host creates a
	// child and is preserved unchanged across every revision, including
	// deletion and restoration. Public submissions can never assign or
	// change it.
	ParentBlockNumber *BlockNumber

	// Content is the raw inner content JSON submitted through the public
	// API; see the struct documentation. Only meaningful on input (the
	// controller consumes it when building the record).
	Content json.RawMessage

	// Record is the deserialized host record; see the struct documentation.
	// It is the authoritative in-memory form used by the controller and
	// transport layers.
	Record content.Record

	// SerializedRecord is the serialized full host record envelope used by
	// the store layer for persistence; see the struct documentation.
	SerializedRecord SerializedRecord
}

type BlockNumber int
type RevisionNumber int
type AuthorID string
type BlockStatus string

const (
	ProgressingBlock = "progressing"
	PublishedBlock   = "published"
	DeletedBlock     = "deleted"
)

// SerializedRecord holds the type-tagged serialized form of a block's full
// host record envelope. The store layer works exclusively with this struct
// for persistence; (de)serialization is owned by the controller through the
// content registry.
type SerializedRecord struct {
	// Type is the canonical namespaced content type name; it must match
	// the record's own type association and the block's Type.
	Type string
	// Data is the JSON encoding of the full host record (timestamp,
	// optional key, and inner content), not just the flat inner content.
	Data []byte
}
