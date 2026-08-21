package md

import (
	"fmt"
	"os"
	"strings"

	"github.com/infrapad/infrapad/cli/pkg/markdown"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newParseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "parse",
		Short: "Parse an infrapad-flavoured markdown file and print its structure",
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, _ := cmd.Flags().GetString("file")

			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("read file: %w", err)
			}

			doc, err := markdown.Parse(data)
			if err != nil {
				return fmt.Errorf("parse: %w", err)
			}

			// Print document metadata.
			fmt.Println("Document:")
			fmt.Printf("  document:  %s\n", doc.Meta.DocumentID)
			fmt.Printf("  title:     %s\n", doc.Meta.Title)
			fmt.Printf("  namespace: %s\n", doc.Meta.Namespace)
			fmt.Printf("  status:    %s\n", doc.Meta.Status)
			fmt.Println()

			// Print blocks.
			fmt.Printf("Blocks (%d):\n", len(doc.Blocks))
			for i, b := range doc.Blocks {
				if i > 0 {
					fmt.Println()
				}
				fmt.Printf("  --- block %d ---\n", b.Meta.BlockNumber)
				fmt.Printf("  type:   %s\n", b.Meta.Type)
				fmt.Printf("  block:  %d\n", b.Meta.BlockNumber)
				fmt.Printf("  rev:    %d\n", b.Meta.RevisionNumber)
				fmt.Printf("  author: %s\n", b.Meta.AuthorID)
				fmt.Println("  content:")
				// Indent each content line for readability.
				var contentStr string
				if b.Meta.Type == "markdown" {
					contentStr = b.Content["text"].(string)
				} else {
					out, _ := yaml.Marshal(b.Content)
					contentStr = string(out)
				}
				for _, line := range splitLines(strings.Trim(contentStr, "\n")) {
					fmt.Printf("    %s\n", line)
				}
			}

			return nil
		},
	}

	cmd.Flags().String("file", "", "Path to the markdown file (required)")
	_ = cmd.MarkFlagRequired("file")

	return cmd
}
