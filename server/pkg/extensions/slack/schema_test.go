package slack_test

import (
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/infrapad/infrapad/server/pkg/content"
	"github.com/infrapad/infrapad/server/pkg/extensions/aiagent"
	"github.com/infrapad/infrapad/server/pkg/extensions/slack"
)

// TestGeneratedSchemas checks the structural constraints carried by the
// generated embedded schemas; runtime validation goes through the registry
// (see slack_test.go).
func TestGeneratedSchemas(t *testing.T) {
	agent := aiagent.NewMessageType()
	types := slack.ContentTypes(agent)
	thread, conversation := types[0].(*slack.ThreadType), types[1]

	cases := []struct {
		name   string
		schema content.Schema
		data   string
		err    string // empty when valid
	}{
		{"thread reference", thread.ContentSchema(), `{"url": "` + threadURL + `"}`, ""},
		{"thread reference not a permalink", thread.ContentSchema(), `{"url": "https://workspace.example.test/x"}`, "url"},
		{"message missing text", conversation.ContentSchema(), `{"author": "Alice"}`, "text"},
		{"message unknown field", conversation.ContentSchema(), `{"author": "Alice", "text": "x", "ts": 1}`, "ts: field not allowed"},
		{"snapshot", thread.PayloadSchema(), `{"agents": {"U3": "investigation-agent"}, "messages": [{"ts": "1788873542.372739", "user": "U1", "text": "hi", "user_name": "Alice", "readable_text": "hi", "previews": [{"title": "T", "title_link": "https://example.com", "product_name": "P", "updated_at": "2026-09-08T13:19:02Z", "text": "b", "fields": [{"label": "L", "value": "V"}]}]}]}`, ""},
		{"snapshot empty messages", thread.PayloadSchema(), `{"messages": []}`, ""},
		{"snapshot blank agent name", thread.PayloadSchema(), `{"agents": {"U3": " "}, "messages": []}`, "agents"},
		{"snapshot nested unknown field", thread.PayloadSchema(), `{"messages": [{"ts": "1.2", "text": "b", "extra": 1}]}`, "messages.0.extra: field not allowed"},
		{"agent message extras", agent.ContentSchema(), `{"agent": "a", "text": "b", "extras": {"slack": {"ids": [1, "2"], "thread": {"ts": "1.2"}}}}`, ""},
		{"agent message unknown field", agent.ContentSchema(), `{"agent": "a", "text": "b", "extra": {}}`, "extra: field not allowed"},
	}
	ctx := cuecontext.New()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			definition := ctx.CompileBytes(tc.schema.Source).LookupPath(cue.ParsePath(tc.schema.Definition))
			err := definition.Unify(ctx.CompileString(tc.data)).Validate(cue.Concrete(true))
			switch {
			case tc.err == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
				t.Fatalf("expected error containing %q, got %v", tc.err, err)
			}
		})
	}
}
