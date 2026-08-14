package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/infrapad/infrapad/cli/pkg/client"
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

var mdPullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Pull a document from the server and write it as infrapad-flavoured markdown",
	RunE: func(cmd *cobra.Command, args []string) error {
		docName, _ := cmd.Flags().GetString("doc")
		filePath, _ := cmd.Flags().GetString("file")

		c, err := client.New(grpcAddr)
		if err != nil {
			return err
		}
		defer c.Close()

		doc, err := c.GetDoc(cmd.Context(), docName)
		if err != nil {
			return fmt.Errorf("get doc: %w", err)
		}

		blocks, err := c.ListBlocks(cmd.Context(), docName)
		if err != nil {
			return fmt.Errorf("list blocks: %w", err)
		}

		out, err := markdown.Render(doc, blocks)
		if err != nil {
			return fmt.Errorf("render: %w", err)
		}

		if filePath == "" {
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}

		if err := os.WriteFile(filePath, []byte(out), 0o644); err != nil {
			return fmt.Errorf("write file: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStderr(), "Written to %s\n", filePath)
		return nil
	},
}

func init() {
	mdParseCmd.Flags().String("file", "", "Path to the markdown file (required)")
	_ = mdParseCmd.MarkFlagRequired("file")

	mdPullCmd.Flags().String("doc", "", "Document name or ID (required)")
	mdPullCmd.Flags().String("file", "", "Output file path (writes to stdout if omitted)")
	_ = mdPullCmd.MarkFlagRequired("doc")

	mdCmd.AddCommand(mdParseCmd, mdPullCmd)
	rootCmd.AddCommand(mdCmd)
}
