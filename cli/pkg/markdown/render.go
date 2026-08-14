package markdown

import (
	"fmt"
	"strings"

	"github.com/infrapad/infrapad/cli/pkg/output"
	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"gopkg.in/yaml.v3"
)

// Render produces an infrapad-flavoured markdown document from a Doc and its
// blocks. The output follows the format described in
// docs/infrapad-markdown-example.md.
func Render(doc *pb.Doc, blocks []*pb.Block) (string, error) {
	var sb strings.Builder

	// --- YAML frontmatter ---
	docID := strings.TrimPrefix(doc.GetName(), "docs/")
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("doc: %s\n", docID))
	sb.WriteString(fmt.Sprintf("title: %s\n", doc.GetTitle()))
	sb.WriteString(fmt.Sprintf("namespace: %s\n", doc.GetNamespace()))
	sb.WriteString(fmt.Sprintf("status: %s\n", doc.GetStatus()))
	sb.WriteString("---\n")

	// --- Blocks ---
	for _, block := range blocks {
		sb.WriteString(renderBlock(block))
	}

	return sb.String(), nil
}

func renderBlock(block *pb.Block) string {
	var sb strings.Builder

	// Directive line.
	sb.WriteString(fmt.Sprintf("::infrapad_block{block=%d rev=%d type=%s",
		block.GetBlockNumber(), block.GetRevisionNumber(), block.GetType()))
	if author := block.GetAuthorId(); author != "" {
		sb.WriteString(fmt.Sprintf(" author=%s", author))
	}
	sb.WriteString("}\n")

	// Content.
	content := block.GetContent()
	if content == nil {
		return sb.String()
	}

	m := content.AsMap()
	if block.GetType() == "markdown" {
		// Markdown blocks store content in a "text" field.
		if text, ok := m["text"].(string); ok {
			sb.WriteString(text)
			if !strings.HasSuffix(text, "\n") {
				sb.WriteString("\n")
			}
		}
	} else {
		// Non-markdown blocks are rendered as a YAML code fence.
		yamlContent, err := renderYAML(m)
		if err != nil {
			// Fallback: just dump the map.
			yamlContent = fmt.Sprintf("%v", m)
		}
		sb.WriteString("```yaml\n")
		sb.WriteString(yamlContent)
		if !strings.HasSuffix(yamlContent, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("```\n")
	}

	return sb.String()
}

// renderYAML converts a map to YAML using the same smart styling as
// the table output (literal block scalars for multi-line strings, flow
// style for leaf sequences).
func renderYAML(data map[string]any) (string, error) {
	var node yaml.Node
	if err := node.Encode(data); err != nil {
		return "", err
	}
	output.SetSmartYAMLStyle(&node)

	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&node); err != nil {
		return "", err
	}
	enc.Close()
	return buf.String(), nil
}
