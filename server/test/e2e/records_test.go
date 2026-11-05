package e2e

// Helpers for the public record envelope (Block.record): ordinary writes
// carry {"content": …} plus an optional "timestamp" echo; reads expose the
// serialized host record verbatim.

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
)

// contentRecord builds an ordinary-write record carrying only content.
func contentRecord(t *testing.T, content map[string]any) *structpb.Struct {
	t.Helper()
	return mustStruct(t, map[string]any{"content": content})
}

// timestampedRecord builds an ordinary-write record with a timestamp echo.
func timestampedRecord(t *testing.T, content map[string]any, ts time.Time) *structpb.Struct {
	t.Helper()
	return mustStruct(t, map[string]any{"content": content, "timestamp": ts.Format(time.RFC3339Nano)})
}

// recordContent returns the inner content of a standalone record.
func recordContent(b *pb.Block) map[string]any {
	content, _ := b.GetRecord().AsMap()["content"].(map[string]any)
	return content
}

// recordItems returns the members of a grouped record.
func recordItems(b *pb.Block) []map[string]any {
	raw, _ := b.GetRecord().AsMap()["items"].([]any)
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		m, _ := item.(map[string]any)
		items = append(items, m)
	}
	return items
}

// recordTimestamp returns the event timestamp of a standalone record
// (the first member's for grouped records); zero when absent.
func recordTimestamp(t *testing.T, b *pb.Block) time.Time {
	t.Helper()
	m := b.GetRecord().AsMap()
	if items := recordItems(b); len(items) > 0 {
		m = items[0]
	}
	raw, _ := m["timestamp"].(string)
	if raw == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("block %d: invalid record timestamp %q: %v", b.GetBlockNumber(), raw, err)
	}
	return ts
}
