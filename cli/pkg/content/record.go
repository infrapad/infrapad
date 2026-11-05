// Package content provides client-side content helpers for CLI workflows,
// including adapters for the public block record envelope (Block.record).
// It does not implement server-side extension contracts or validation.
//
// The server exposes each block's host record verbatim: standalone records
// as {"timestamp", "content"[, "key"]}, grouped records as {"items": [...]}.
// Ordinary writes submit only {"content": …}; the server assigns
// timestamps and never accepts keys or collection membership.
package content

import (
	"fmt"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"google.golang.org/protobuf/types/known/structpb"
)

// InnerContent returns the inner content of a standalone record, or nil
// for grouped records and blocks without a record.
func InnerContent(b *pb.Block) map[string]any {
	if b.GetRecord() == nil {
		return nil
	}
	content, _ := b.GetRecord().AsMap()["content"].(map[string]any)
	return content
}

// IsGrouped reports whether the block holds a grouped record.
func IsGrouped(b *pb.Block) bool {
	if b.GetRecord() == nil {
		return false
	}
	_, ok := b.GetRecord().GetFields()["items"]
	return ok
}

// FromContent builds the record of an ordinary write from inner content.
func FromContent(content map[string]any) (*structpb.Struct, error) {
	if content == nil {
		content = map[string]any{}
	}
	record, err := structpb.NewStruct(map[string]any{"content": content})
	if err != nil {
		return nil, fmt.Errorf("convert content to record: %w", err)
	}
	return record, nil
}
