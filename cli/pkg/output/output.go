package output

import (
	"encoding/json"
	"fmt"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
)

// PrintDoc prints document details to stdout.
func PrintDoc(doc *pb.Doc) {
	fmt.Printf("name: %s\n", doc.GetName())
	fmt.Printf("title: %s\n", doc.GetTitle())
	fmt.Printf("namespace: %s\n", doc.GetNamespace())
	fmt.Printf("status: %s\n", doc.GetStatus())
	if len(doc.GetBlocks()) > 0 {
		fmt.Printf("blocks: %d\n", len(doc.GetBlocks()))
		for _, b := range doc.GetBlocks() {
			fmt.Printf("  block=%d  rev=%d  type=%s\n", b.GetBlockNumber(), b.GetRevisionNumber(), b.GetType())
		}
	}
}

// PrintBlock prints block details to stdout.
func PrintBlock(block *pb.Block) {
	fmt.Printf("block_number: %d\n", block.GetBlockNumber())
	fmt.Printf("revision_number: %d\n", block.GetRevisionNumber())
	fmt.Printf("type: %s\n", block.GetType())
	switch c := block.GetContent().(type) {
	case *pb.Block_Markdown:
		fmt.Printf("text: %s\n", c.Markdown.GetText())
	case *pb.Block_AlertsMatcher:
		matchers := c.AlertsMatcher.GetLabelsMatchers()
		fmt.Printf("matchers_count: %d\n", len(matchers))
		for i, m := range matchers {
			data, _ := json.Marshal(labelMatcherToMap(m))
			fmt.Printf("  matcher[%d]: %s\n", i, string(data))
		}
	}
}

func labelMatcherToMap(m *pb.LabelsMatcher) map[string][]string {
	result := make(map[string][]string)
	for k, v := range m.GetLabels() {
		result[k] = v.GetValues()
	}
	return result
}
