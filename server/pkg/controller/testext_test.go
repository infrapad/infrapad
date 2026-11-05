package controller_test

// A small test-only expandable reference type for host orchestration
// checks (payload submission, cascading): Test/Ref emits one keyed Test/Ref
// child per payload entry, forwarding each entry's transient payload, or
// deliberately invalid output on request.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/core"
)

const refTypeName = "Test/Ref"

var refSchema = []byte(`
#Ref: {
	name: string
}
#Payload: {
	children?: [...{
		name:     string
		payload?: {...}
	}]
	// invalid requests output of an unregistered content type instance.
	invalid?: bool
}
`)

type ref struct {
	Name string `json:"name"`
}

type refPayload struct {
	Children []struct {
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload,omitempty"`
	} `json:"children"`
	Invalid bool `json:"invalid"`
}

type refType struct {
	content.TypedMarkdownRenderer[content.Item[ref]]
}

func newRefType() *refType {
	return &refType{TypedMarkdownRenderer: content.ItemMarkdown(func(item content.Item[ref]) (string, error) {
		return item.ContentValue.Name + "\n", nil
	})}
}

func (*refType) Name() string { return refTypeName }

func (*refType) ContentSchema() content.Schema {
	return content.Schema{Source: refSchema, Definition: "#Ref"}
}

func (*refType) PayloadSchema() content.Schema {
	return content.Schema{Source: refSchema, Definition: "#Payload"}
}

func (*refType) DecodeJSON(data json.RawMessage) (content.Content, error) {
	return content.DecodeContent[ref](data)
}

func (t *refType) Expand(ctx context.Context, existing content.Record, payload json.RawMessage) (content.ExpansionResult, error) {
	p, err := content.DecodePayload[refPayload](payload)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	result := content.ExpansionResult{Record: existing}
	at := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	if p.Invalid {
		result.Blocks = append(result.Blocks, content.EmittedBlock{
			Record: content.NewKeyedItem(core.NewMarkdownType(), "invalid", at, core.Markdown{Text: "x"}),
		})
	}
	for n, child := range p.Children {
		result.Blocks = append(result.Blocks, content.EmittedBlock{
			Record:           content.NewKeyedItem(t, child.Name, at.Add(time.Duration(n)*time.Minute), ref{Name: child.Name}),
			ExpansionPayload: child.Payload,
		})
	}
	return result, nil
}
