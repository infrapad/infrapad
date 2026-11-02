package content

import (
	"context"
	"encoding/json"
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

func TestCheckExpansionIdentities(t *testing.T) {
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
		if _, err := registry.CheckExpansion(primary, result); err == nil {
			t.Errorf("%s: accepted", name)
		} else {
			t.Logf("%s: %v", name, err)
		}
	}
	valid := ExpansionResult{Record: primary, Blocks: []EmittedBlock{
		{Record: collection("a", "b")},
		{Record: NewKeyedItem(r, "nested", ts, text{}), ExpansionPayload: json.RawMessage(`{"texts":[]}`)},
	}}
	members, err := registry.CheckExpansion(primary, valid)
	if err != nil || len(members) != 3 {
		t.Fatalf("valid expansion: %v, %d members", err, len(members))
	}
}
