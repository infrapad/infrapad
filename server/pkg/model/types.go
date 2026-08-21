package model

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
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
	Type           string
	Status         BlockStatus
	CreatedAt      time.Time

	// Content is the deserialized block content.
	// Used by the controller layer; the store layer should use
	// SerializedContent instead.
	Content BlockContent

	// SerializedContent is the serialized form of the block content.
	// Used by the store layer for persistence; the controller converts
	// between Content and SerializedContent.
	SerializedContent SerializedContent
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

// SerializedContent holds the type-tagged serialized form of a block's content.
// The store layer works exclusively with this struct for persistence.
type SerializedContent struct {
	Type string
	Data []byte
}

// BlockContent is the interface that every block content type must implement.
type BlockContent interface {
	// Render produces the plain-text representation of the content.
	Render() string
	// Serialize returns the serialized form of the content.
	Serialize() (SerializedContent, error)
}

// BlockContentDeserializer is a function that reconstructs a BlockContent
// from its serialized data.
type BlockContentDeserializer func(data []byte) (BlockContent, error)

var (
	deserializersMu sync.RWMutex
	deserializers   = map[string]BlockContentDeserializer{}
)

// RegisterBlockContentType registers a deserializer for a given content type.
// It is typically called from init() functions of packages that define
// concrete BlockContent implementations.
func RegisterBlockContentType(typeName string, fn BlockContentDeserializer) {
	deserializersMu.Lock()
	defer deserializersMu.Unlock()
	deserializers[typeName] = fn
}

// DeserializeBlockContent looks up the registered deserializer for sc.Type
// and uses it to reconstruct a BlockContent value.
func DeserializeBlockContent(sc SerializedContent) (BlockContent, error) {
	deserializersMu.RLock()
	fn, ok := deserializers[sc.Type]
	deserializersMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no deserializer registered for block content type %q", sc.Type)
	}
	return fn(sc.Data)
}

type MarkdownBC struct {
	text string
}

// NewMarkdownBC creates a MarkdownBC with the given text content.
func NewMarkdownBC(text string) MarkdownBC {
	return MarkdownBC{text: text}
}

func (m MarkdownBC) Render() string {
	return m.text
}

func (m MarkdownBC) Serialize() (SerializedContent, error) {
	data, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: m.text})
	if err != nil {
		return SerializedContent{}, err
	}
	return SerializedContent{Type: "markdown", Data: data}, nil
}

var _ BlockContent = MarkdownBC{}

func init() {
	RegisterBlockContentType("markdown", func(data []byte) (BlockContent, error) {
		var v struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return MarkdownBC{text: v.Text}, nil
	})
}

type AlertsMatcherBC struct {
	// LabelsMatchers represent the conditions to match the alerts.
	// Semantics:
	//    [
	//      { "a":["b",],
	//        "c":["d", "e"]},
	//      { "g":["h"] }
	//    ]
	//  means:
	//    (
	//      (alert["a"] == "b") and
	//      ((alert["c"] == "d") or (alert["c"] == "e"))
	//    ) or (
	//      (alert["g"] == "h")
	//    )
	LabelsMatchers []map[string][]string
	Since          time.Time
	// Zero time means right-open interval.
	Until time.Time
}

func (am AlertsMatcherBC) Render() string {
	// TODO: implement simple string representation
	return fmt.Sprintf("%v %s %s", am.LabelsMatchers, am.Since, am.Until)
}

func (am AlertsMatcherBC) Serialize() (SerializedContent, error) {
	data, err := json.Marshal(am)
	if err != nil {
		return SerializedContent{}, err
	}
	return SerializedContent{Type: "alerts_matcher", Data: data}, nil
}

var _ BlockContent = AlertsMatcherBC{}

func init() {
	RegisterBlockContentType("alerts_matcher", func(data []byte) (BlockContent, error) {
		var v AlertsMatcherBC
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		return v, nil
	})
}
