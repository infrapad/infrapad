// Package testext provides small test-only content types for host
// orchestration checks (payload submission, cascading expansion) in
// controller and integration tests. It is never registered in production
// startup and must not be imported outside tests.
package testext

// Test/Ref is an expandable standalone reference type: it emits one keyed
// Test/Ref child per payload entry, forwarding each entry's transient
// payload, and supports the hooks the orchestration tests need:
//
//   - note: enriches the primary record's content (reference enrichment)
//     while preserving its type, key, and timestamp;
//   - invalid: deliberately emits output of an unregistered content type
//     instance (invalid extension output);
//   - repeat: re-emits the received reference content and payload one
//     level down, producing an identical input tuple along the expansion
//     path (a cycle).

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
)

// RefTypeName is the canonical name of the test reference type.
const RefTypeName = "Test/Ref"

var refSchema = []byte(`
#Ref: {
	name: string
	note?: string
}
#Payload: {
	children?: [...{
		name:     string
		payload?: {...}
	}]
	note?:    string
	invalid?: bool
	repeat?:  bool
}
`)

// Ref is the inner content of a Test/Ref record.
type Ref struct {
	Name string `json:"name"`
	Note string `json:"note,omitempty"`
}

type refPayload struct {
	Children []struct {
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload,omitempty"`
	} `json:"children"`
	Note    string `json:"note"`
	Invalid bool   `json:"invalid"`
	Repeat  bool   `json:"repeat"`
}

// RefType is the test-only expandable reference content type.
type RefType struct {
	content.TypedMarkdownRenderer[content.Item[Ref]]
	expansions atomic.Int64
}

// NewRefType constructs the test reference type.
func NewRefType() *RefType {
	return &RefType{TypedMarkdownRenderer: content.ItemMarkdown(func(item content.Item[Ref]) (string, error) {
		return item.ContentValue.Name + "\n", nil
	})}
}

func (*RefType) Name() string { return RefTypeName }

func (*RefType) ContentSchema() content.Schema {
	return content.Schema{Source: refSchema, Definition: "#Ref"}
}

func (*RefType) PayloadSchema() content.Schema {
	return content.Schema{Source: refSchema, Definition: "#Payload"}
}

func (*RefType) DecodeJSON(data json.RawMessage) (content.Content, error) {
	return content.DecodeContent[Ref](data)
}

// Expansions reports how many times Expand was invoked, so tests can
// check that a node was rejected before its expander ran.
func (t *RefType) Expansions() int {
	return int(t.expansions.Load())
}

func (t *RefType) Expand(ctx context.Context, existing content.Record, payload json.RawMessage) (content.ExpansionResult, error) {
	t.expansions.Add(1)
	p, err := content.DecodePayload[refPayload](payload)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	// The payload note enriches the primary record's content while
	// preserving its identity (type, key, timestamp).
	record := existing
	if p.Note != "" {
		item, err := content.ItemView[Ref](existing)
		if err != nil {
			return content.ExpansionResult{}, err
		}
		if record, err = content.WithContent(existing, Ref{Name: item.ContentValue.Name, Note: p.Note}); err != nil {
			return content.ExpansionResult{}, err
		}
	}
	result := content.ExpansionResult{Record: record}
	at := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	if p.Invalid {
		// A fresh (unregistered) type instance makes the output invalid.
		result.Blocks = append(result.Blocks, content.EmittedBlock{
			Record: content.NewKeyedItem(core.NewMarkdownType(), "invalid", at, core.Markdown{Text: "x"}),
		})
	}
	for n, child := range p.Children {
		result.Blocks = append(result.Blocks, content.EmittedBlock{
			Record:           content.NewKeyedItem(t, child.Name, at.Add(time.Duration(n)*time.Minute), Ref{Name: child.Name}),
			ExpansionPayload: child.Payload,
		})
	}
	if p.Repeat {
		item, err := content.ItemView[Ref](existing)
		if err != nil {
			return content.ExpansionResult{}, err
		}
		result.Blocks = append(result.Blocks, content.EmittedBlock{
			Record:           content.NewKeyedItem(t, "repeat", at, item.ContentValue),
			ExpansionPayload: payload,
		})
	}
	return result, nil
}
