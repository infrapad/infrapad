package output

import (
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
)

// DocColumns returns the default table columns for documents.
func DocColumns() []Column {
	return []Column{
		{Header: "ID", Value: func(m proto.Message) string {
			// Strip the "documents/" resource prefix for readability.
			return strings.TrimPrefix(m.(*pb.Document).GetName(), "documents/")
		}},
		{Header: "Namespace", Value: func(m proto.Message) string { return m.(*pb.Document).GetNamespace() }},
		{Header: "Title", Value: func(m proto.Message) string { return m.(*pb.Document).GetTitle() }},
		{Header: "Status", Value: func(m proto.Message) string { return m.(*pb.Document).GetStatus() }},
		{Header: "Created At", Value: func(m proto.Message) string {
			ts := m.(*pb.Document).GetCreatedAt()
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
		{Header: "Content", FullRow: true, Value: func(m proto.Message) string {
			block := m.(*pb.Block)
			content := block.GetContent()
			if content == nil {
				return ""
			}
			return blockContentYAML(block.GetType(), content.AsMap())
		}},
	}
}

// blockContentYAML converts block content to YAML with smart styling:
// multi-line strings use literal block scalar style (|), and leaf
// sequences (containing only scalars) use flow style ([a, b]).
func blockContentYAML(_ string, data map[string]any) string {
	var node yaml.Node
	if err := node.Encode(data); err != nil {
		out, _ := yaml.Marshal(data)
		return strings.TrimRight(string(out), "\n")
	}
	SetSmartYAMLStyle(&node)
	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&node); err != nil {
		// Fallback to JSON on error.
		j, _ := json.Marshal(data)
		return string(j)
	}
	enc.Close()
	return strings.TrimRight(buf.String(), "\n")
}

// SetSmartYAMLStyle walks a yaml.Node tree and applies styling heuristics:
// - multi-line strings get literal block scalar style (|)
// - leaf sequences (only scalars) get flow style ([a, b])
func SetSmartYAMLStyle(n *yaml.Node) {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!str" && strings.Contains(n.Value, "\n") {
		n.Style = yaml.LiteralStyle
	}
	if n.Kind == yaml.SequenceNode && allScalar(n) {
		n.Style = yaml.FlowStyle
		return
	}
	for _, child := range n.Content {
		SetSmartYAMLStyle(child)
	}
}

// allScalar returns true if every element of a sequence node is a scalar.
func allScalar(n *yaml.Node) bool {
	for _, child := range n.Content {
		if child.Kind != yaml.ScalarNode {
			return false
		}
	}
	return true
}
