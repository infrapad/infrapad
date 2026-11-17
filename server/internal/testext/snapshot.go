package testext

// Test/Snapshot is an expandable standalone type with a content-type
// revision policy (content.RevisionPolicy), for checking revision-policy
// handling across all record write paths without depending on a real
// extension:
//
//   - name is revision-worthy metadata; data is a refreshable snapshot
//     replaced in place when only it changes;
//   - the name "policy-error" makes the policy fail;
//   - the Markdown body is the name (editable; data is preserved);
//   - payloads replace the primary's data (when given) and emit one keyed
//     Test/Snapshot child per entry (keyed by its key).

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/infrapad/infrapad/server/pkg/content"
)

// SnapshotTypeName is the canonical name of the test snapshot type.
const SnapshotTypeName = "Test/Snapshot"

// SnapshotPolicyError is the name that makes the revision policy fail.
const SnapshotPolicyError = "policy-error"

var snapshotSchema = []byte(`
#Snapshot: {
	name:  string
	data?: string
}
#Payload: {
	data?: string
	children?: [...{
		key:   string
		name:  string
		data?: string
	}]
}
`)

// Snapshot is the inner content of a Test/Snapshot record.
type Snapshot struct {
	Name string `json:"name"`
	Data string `json:"data,omitempty"`
}

type snapshotPayload struct {
	Data     *string `json:"data"`
	Children []struct {
		Key  string `json:"key"`
		Name string `json:"name"`
		Data string `json:"data"`
	} `json:"children"`
}

// SnapshotType is the test-only content type with a revision policy.
type SnapshotType struct {
	content.TypedMarkdownRenderer[content.Item[Snapshot]]
}

// NewSnapshotType constructs the test snapshot type.
func NewSnapshotType() *SnapshotType {
	return &SnapshotType{TypedMarkdownRenderer: content.ItemMarkdown(func(item content.Item[Snapshot]) (string, error) {
		return item.ContentValue.Name + "\n", nil
	})}
}

func (*SnapshotType) Name() string { return SnapshotTypeName }

func (*SnapshotType) ContentSchema() content.Schema {
	return content.Schema{Source: snapshotSchema, Definition: "#Snapshot"}
}

func (*SnapshotType) PayloadSchema() content.Schema {
	return content.Schema{Source: snapshotSchema, Definition: "#Payload"}
}

func (*SnapshotType) DecodeJSON(data json.RawMessage) (content.Content, error) {
	return content.DecodeContent[Snapshot](data)
}

// ShouldCreateRevision creates revisions for name changes only.
func (*SnapshotType) ShouldCreateRevision(previous, next content.Record) (bool, error) {
	before, err := content.ItemView[Snapshot](previous)
	if err != nil {
		return false, err
	}
	after, err := content.ItemView[Snapshot](next)
	if err != nil {
		return false, err
	}
	if after.ContentValue.Name == SnapshotPolicyError {
		return false, errors.New("test policy failure")
	}
	return before.ContentValue.Name != after.ContentValue.Name, nil
}

// UpdateMarkdown replaces the name, preserving data.
func (*SnapshotType) UpdateMarkdown(existing content.Record, body string) (content.Record, error) {
	item, err := content.ItemView[Snapshot](existing)
	if err != nil {
		return nil, err
	}
	value := item.ContentValue
	value.Name = strings.TrimSpace(body)
	return content.WithContent(existing, value)
}

func (t *SnapshotType) Expand(ctx context.Context, existing content.Record, payload json.RawMessage) (content.ExpansionResult, error) {
	p, err := content.DecodePayload[snapshotPayload](payload)
	if err != nil {
		return content.ExpansionResult{}, err
	}
	record := existing
	if p.Data != nil {
		item, err := content.ItemView[Snapshot](existing)
		if err != nil {
			return content.ExpansionResult{}, err
		}
		value := item.ContentValue
		value.Data = *p.Data
		if record, err = content.WithContent(existing, value); err != nil {
			return content.ExpansionResult{}, err
		}
	}
	result := content.ExpansionResult{Record: record}
	at := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	for n, child := range p.Children {
		result.Blocks = append(result.Blocks, content.EmittedBlock{
			Record:           content.NewKeyedItem(t, child.Key, at.Add(time.Duration(n)*time.Minute), Snapshot{Name: child.Name, Data: child.Data}),
			ExpansionPayload: json.RawMessage(`{}`),
		})
	}
	return result, nil
}
