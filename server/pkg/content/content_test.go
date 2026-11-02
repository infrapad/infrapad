package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

const testCUE = `
#Text: close({text: string})
#Payload: close({texts: [...string]})
`

type text struct {
	Text string `json:"text"`
}

// note is an editable standalone type, line a groupable type, and ref an
// expander emitting lines, standing in for real extensions.
type note struct {
	TypedMarkdownRenderer[Item[text]]
}
type line struct {
	name string
	TypedMarkdownRenderer[GroupableCollection[text]]
}
type ref struct {
	TypedMarkdownRenderer[Item[text]]
}

func (*note) Name() string                                     { return "Test/Note" }
func (*note) ContentSchema() Schema                            { return Schema{[]byte(testCUE), "#Text"} }
func (*note) DecodeJSON(data json.RawMessage) (Content, error) { return DecodeContent[text](data) }
func (*note) UpdateMarkdown(existing Record, body string) (Record, error) {
	item, err := ItemView[text](existing)
	if err != nil {
		return nil, err
	}
	item.ContentValue.Text = body
	return WithContent(existing, item.ContentValue)
}

func (l *line) Name() string                                   { return l.name }
func (*line) ContentSchema() Schema                            { return Schema{[]byte(testCUE), "#Text"} }
func (*line) DecodeJSON(data json.RawMessage) (Content, error) { return DecodeContent[text](data) }
func (l *line) NewCollection(items []KeyedRecord) (GroupableRecord, error) {
	return NewCollection[text](l, items)
}

func (*ref) Name() string                                     { return "Test/Ref" }
func (*ref) ContentSchema() Schema                            { return Schema{[]byte(testCUE), "#Text"} }
func (*ref) PayloadSchema() Schema                            { return Schema{[]byte(testCUE), "#Payload"} }
func (*ref) DecodeJSON(data json.RawMessage) (Content, error) { return DecodeContent[text](data) }
func (r *ref) Expand(_ context.Context, existing Record, _ json.RawMessage) (ExpansionResult, error) {
	return ExpansionResult{Record: existing}, nil
}

var ts = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

func renderText(item Item[text]) (string, error) { return item.ContentValue.Text, nil }

func newTypes() (*note, *line, *ref) {
	l := &line{name: "Test/Line"}
	return &note{ItemMarkdown(renderText)}, l, &ref{ItemMarkdown(renderText)}
}

func TestRegistrationChecks(t *testing.T) {
	n, l, r := newTypes()
	registry, err := NewRegistry([]ContentType{n, l, r})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Lookup("Missing/Type"); err == nil {
		t.Error("unknown type accepted")
	}
	for name, types := range map[string][]ContentType{
		"duplicate":    {n, n},
		"invalid name": {&line{name: "Line"}},
		"bad selector": {&badSchema{schema: Schema{[]byte(testCUE), "#Missing"}}},
		"bad source":   {&badSchema{schema: Schema{[]byte("#Text: {"), "#Text"}}},
		"missing ref":  {&badSchema{schema: Schema{}}},
		"group+expand": {&groupingRef{ref: r}},
	} {
		if _, err := NewRegistry(types); err == nil {
			t.Errorf("%s: registration accepted", name)
		}
	}
}

type badSchema struct {
	note
	schema Schema
}

func (b *badSchema) ContentSchema() Schema { return b.schema }

type groupingRef struct{ *ref }

func (*groupingRef) NewCollection([]KeyedRecord) (GroupableRecord, error) { return nil, nil }

func TestCodecAndCollections(t *testing.T) {
	n, l, r := newTypes()
	registry, err := NewRegistry([]ContentType{n, l, r})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.NewCollection(nil); err == nil {
		t.Error("empty collection accepted")
	}
	for name, data := range map[string]string{
		"bare content":  `{"text":"x"}`,
		"unknown field": `{"timestamp":"2026-10-25T10:00:00Z","content":{"text":"x"},"parent":1}`,
		"empty key":     `{"timestamp":"2026-10-25T10:00:00Z","content":{"text":"x"},"key":""}`,
	} {
		if _, err := registry.DecodeRecord(n.Name(), json.RawMessage(data)); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
	if _, err := registry.DecodeRecord(l.Name(), json.RawMessage(`{"items":[]}`)); err == nil {
		t.Error("empty stored collection decoded")
	}
	if _, err := EncodeRecord(NewKeyedItem(l, "a", ts, text{"x"})); err == nil {
		t.Error("groupable type encoded as a standalone item")
	}

	// Markdown updates replace content only, keeping metadata and the old record.
	original := NewKeyedItem(n, "k", ts, text{"before"})
	updated, err := n.UpdateMarkdown(original, "after")
	if err != nil {
		t.Fatal(err)
	}
	view, err := KeyedItemView[text](updated)
	if err != nil {
		t.Fatal(err)
	}
	if view.Key() != "k" || !view.Timestamp().Equal(ts) || view.ContentValue.Text != "after" || original.ContentValue.Text != "before" {
		t.Fatalf("unexpected update: %#v (original %#v)", view, original)
	}
}

func TestValidateExpansionIdentities(t *testing.T) {
	n, l, r := newTypes()
	registry, err := NewRegistry([]ContentType{n, l, r})
	if err != nil {
		t.Fatal(err)
	}
	collection := func(keys ...string) KeyedRecord {
		var items []KeyedRecord
		for _, key := range keys {
			items = append(items, NewKeyedItem(l, key, ts, text{key}))
		}
		c, err := l.NewCollection(items)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	primary := NewKeyedItem(r, "primary", ts, text{"ref"})
	cases := map[string]ExpansionResult{
		"duplicate key":    {Record: primary, Blocks: []EmittedBlock{{Record: collection("a")}, {Record: collection("b", "a")}}},
		"empty key":        {Record: primary, Blocks: []EmittedBlock{{Record: NewKeyedItem(n, "", ts, text{})}}},
		"missing ts":       {Record: primary, Blocks: []EmittedBlock{{Record: NewKeyedItem(n, "a", time.Time{}, text{})}}},
		"changed key":      {Record: NewKeyedItem(r, "other", ts, text{"ref"})},
		"unregistered":     {Record: primary, Blocks: []EmittedBlock{{Record: NewKeyedItem(&note{}, "a", ts, text{})}}},
		"ungrouped":        {Record: primary, Blocks: []EmittedBlock{{Record: NewKeyedItem(l, "a", ts, text{})}}},
		"payload to plain": {Record: primary, Blocks: []EmittedBlock{{Record: NewKeyedItem(n, "a", ts, text{}), ExpansionPayload: json.RawMessage(`{}`)}}},
	}
	for name, result := range cases {
		err := registry.ValidateExpansion(primary, result)
		var validation *ValidationError
		if !errors.As(err, &validation) || validation.Stage != StageRecord {
			t.Errorf("%s: expected a host record validation error, got %v", name, err)
		}
	}
	valid := ExpansionResult{Record: primary, Blocks: []EmittedBlock{
		{Record: collection("a", "b")},
		{Record: NewKeyedItem(r, "nested", ts, text{}), ExpansionPayload: json.RawMessage(`{"texts":[]}`)},
	}}
	if err := registry.ValidateExpansion(primary, valid); err != nil || len(valid.Members()) != 3 {
		t.Fatalf("valid expansion: %v, %d members", err, len(valid.Members()))
	}
}

func TestRuntimeValidation(t *testing.T) {
	n, l, r := newTypes()
	registry, err := NewRegistry([]ContentType{n, l, r})
	if err != nil {
		t.Fatal(err)
	}
	primary := NewKeyedItem(r, "primary", ts, text{"ref"})
	invalidMembers, err := NewCollection[map[string]any](l, []KeyedRecord{
		NewKeyedItem(l, "a", ts, map[string]any{"text": "ok"}),
		NewKeyedItem(l, "b", ts, map[string]any{"text": 1}),
	})
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		stage    ValidationStage
		location string
		path     string
	}
	cases := map[string]struct {
		err  error
		want want
	}{
		"unknown content field": {
			err:  errOnly(registry.DecodeContent(n.Name(), json.RawMessage(`{"text":"x","extra":1}`))),
			want: want{StageContent, "", "extra"},
		},
		"stored member content": {
			err:  errOnly(registry.DecodeRecord(l.Name(), json.RawMessage(`{"items":[{"timestamp":"2026-10-25T10:00:00Z","key":"a","content":{}}]}`))),
			want: want{StageContent, `member 0 (key "a")`, "text"},
		},
		"payload": {
			err:  registry.ValidatePayload(r.Name(), json.RawMessage(`{"texts":["a",1]}`)),
			want: want{StagePayload, "", "texts.1"},
		},
		"returned primary": {
			err:  registry.ValidateExpansion(primary, ExpansionResult{Record: NewKeyedItem(r, "primary", ts, map[string]any{"text": "x", "url": "y"})}),
			want: want{StageContent, "", "url"},
		},
		"grouped member": {
			err:  registry.ValidateExpansion(primary, ExpansionResult{Record: primary, Blocks: []EmittedBlock{{Record: invalidMembers}}}),
			want: want{StageContent, `block 0 > member 1 (key "b")`, "text"},
		},
	}
	for name, tc := range cases {
		var validation *ValidationError
		if !errors.As(tc.err, &validation) {
			t.Errorf("%s: expected a validation error, got %v", name, tc.err)
			continue
		}
		got := want{validation.Stage, validation.Location.String(), ""}
		if len(validation.Fields) > 0 {
			got.path = validation.Fields[0].Path
		}
		if got != tc.want {
			t.Errorf("%s: got %+v, want %+v (%v)", name, got, tc.want, validation)
		}
	}
	if err := registry.ValidatePayload(n.Name(), json.RawMessage(`{}`)); err == nil {
		t.Error("payload accepted for a non-expandable type")
	}
	if err := registry.ValidatePayload("Missing/Type", json.RawMessage(`{}`)); !errors.Is(err, ErrUnknownContentType) {
		t.Errorf("unknown type: %v", err)
	}
}

func errOnly[T any](_ T, err error) error { return err }

// TestNestedExpansionLocation simulates a cascading host: an upstream
// expansion emits a nested reference (block 1), whose own expansion emits an
// invalid grouped member (block 0, member 1). The host adds the upstream
// block step to the downstream error while unwinding.
func TestNestedExpansionLocation(t *testing.T) {
	n, l, r := newTypes()
	registry, err := NewRegistry([]ContentType{n, l, r})
	if err != nil {
		t.Fatal(err)
	}
	invalid, err := NewCollection[map[string]any](l, []KeyedRecord{
		NewKeyedItem(l, "a", ts, map[string]any{"text": "ok"}),
		NewKeyedItem(l, "b", ts, map[string]any{"text": 1}),
	})
	if err != nil {
		t.Fatal(err)
	}
	nested := NewKeyedItem(r, "nested", ts, text{"nested"})
	downstream := registry.ValidateExpansion(nested, ExpansionResult{Record: nested, Blocks: []EmittedBlock{{Record: invalid}}})
	err = locateError(fmt.Errorf("cascade: %w", downstream), atBlock(1))

	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("expected a validation error, got %v", err)
	}
	want := Location{atBlock(1), atBlock(0), atMember(1, "b")}
	if !slices.Equal(validation.Location, want) {
		t.Fatalf("location %v, want %v", validation.Location, want)
	}
}
