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
// generated embedded schemas. Operation-time validation is a separate task.
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
		{"thread reference", thread.ContentSchema(), `{"url": "https://slack.example/x"}`, ""},
		{"message missing text", conversation.ContentSchema(), `{"author": "Alice"}`, "text"},
		{"message unknown field", conversation.ContentSchema(), `{"author": "Alice", "text": "x", "ts": 1}`, "ts: field not allowed"},
		{"snapshot", thread.PayloadSchema(), `{"messages": [{"id": "1", "timestamp": "2026-10-25T10:00:00Z", "author": "a", "text": "b", "agent": true}]}`, ""},
		{"snapshot nested unknown field", thread.PayloadSchema(), `{"messages": [{"id": "1", "timestamp": "2026-10-25T10:00:00Z", "author": "a", "text": "b", "extra": 1}]}`, "messages.0.extra: field not allowed"},
		{"snapshot malformed timestamp", thread.PayloadSchema(), `{"messages": [{"id": "1", "timestamp": "yesterday", "author": "a", "text": "b"}]}`, "time.Time"},
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
