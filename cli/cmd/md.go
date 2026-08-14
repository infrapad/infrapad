package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/infrapad/infrapad/cli/pkg/markdown"
	"github.com/spf13/cobra"
)

var mdCmd = &cobra.Command{
	Use:   "md",
	Short: "Markdown utilities",
}

var mdParseCmd = &cobra.Command{
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
		fmt.Printf("  doc:       %s\n", doc.Meta.DocID)
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
			for _, line := range splitLines(strings.Trim(b.Content, "\n")) {
				fmt.Printf("    %s\n", line)
			}
		}

		return nil
	},
}

// splitLines splits s into lines without losing empty ones.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := []string{}
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start <= len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func init() {
	mdParseCmd.Flags().String("file", "", "Path to the markdown file (required)")
	_ = mdParseCmd.MarkFlagRequired("file")

	mdCmd.AddCommand(mdParseCmd)
	rootCmd.AddCommand(mdCmd)
}
