package e2e

// Shared Slack snapshot fixture builders for lifecycle and rejection scenarios.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
)

// slackOrigin is the wall-clock origin of the fixture's Slack messages.
var slackOrigin = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

// slackSnapshot builds a Slack thread snapshot with the investigation
// agent mapped to user U3.
func slackSnapshot(t *testing.T, messages ...map[string]any) *structpb.Struct {
	t.Helper()
	list := make([]any, len(messages))
	for n, m := range messages {
		list[n] = m
	}
	return mustStruct(t, map[string]any{
		"agents":   map[string]any{"U3": "investigation-agent"},
		"messages": list,
	})
}

func slackMessage(minute int, user, name, text string) map[string]any {
	m := map[string]any{"ts": slackTS(minute), "user": user, "text": text}
	if name != "" {
		m["user_name"] = name
	}
	return m
}

func slackID(minute int) string { return strings.ReplaceAll(slackTS(minute), ".", "") }

func slackTS(minute int) string {
	return fmt.Sprintf("%d.000000", slackOrigin.Add(time.Duration(minute)*time.Minute).Unix())
}
