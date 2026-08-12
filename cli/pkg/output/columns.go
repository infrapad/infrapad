package output

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
)

// DocColumns returns the default table columns for documents.
func DocColumns() []Column {
	return []Column{
		{Header: "ID", Value: func(m proto.Message) string {
			// Strip the "docs/" resource prefix for readability.
			return strings.TrimPrefix(m.(*pb.Doc).GetName(), "docs/")
		}},
		{Header: "Namespace", Value: func(m proto.Message) string { return m.(*pb.Doc).GetNamespace() }},
		{Header: "Title", Value: func(m proto.Message) string { return m.(*pb.Doc).GetTitle() }},
		{Header: "Status", Value: func(m proto.Message) string { return m.(*pb.Doc).GetStatus() }},
		{Header: "Created At", Value: func(m proto.Message) string {
			ts := m.(*pb.Doc).GetCreatedAt()
			if ts == nil {
				return ""
			}
			return ts.AsTime().Format("2006-01-02 15:04:05")
		}},
	}
}

// BlockColumns returns the default table columns for blocks.
func BlockColumns() []Column {
	return []Column{
		{Header: "Block", Value: func(m proto.Message) string {
			return fmt.Sprintf("%d", m.(*pb.Block).GetBlockNumber())
		}},
		{Header: "Rev", Value: func(m proto.Message) string {
			return fmt.Sprintf("%d", m.(*pb.Block).GetRevisionNumber())
		}},
		{Header: "Type", Value: func(m proto.Message) string { return m.(*pb.Block).GetType() }},
	}
}
