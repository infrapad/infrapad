package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/infrapad/infrapad/cli/pkg/client"
	"github.com/infrapad/infrapad/cli/pkg/markdown"
	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
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

var mdPushCmd = &cobra.Command{
	Use:   "push",
	Short: "Push local changes to a block back to the server",
	RunE: func(cmd *cobra.Command, args []string) error {
		filePath, _ := cmd.Flags().GetString("file")
		blockNumber, _ := cmd.Flags().GetInt32("block")

		// 1. Parse the file to extract the referenced block.
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("read file: %w", err)
		}

		doc, err := markdown.Parse(data)
		if err != nil {
			return fmt.Errorf("parse: %w", err)
		}

		var found *markdown.ParsedBlock
		for i := range doc.Blocks {
			if int32(doc.Blocks[i].Meta.BlockNumber) == blockNumber {
				found = &doc.Blocks[i]
				break
			}
		}
		if found == nil {
			return fmt.Errorf("block %d not found in %s", blockNumber, filePath)
		}

		// Build the content struct based on block type.
		var contentMap map[string]any
		if found.Meta.Type == "markdown" {
			contentMap = map[string]any{"text": found.Content}
		} else {
			// Non-markdown blocks have YAML content; parse it.
			var parsed map[string]any
			if err := yaml.Unmarshal([]byte(found.Content), &parsed); err != nil {
				return fmt.Errorf("parse block content as YAML: %w", err)
			}
			contentMap = parsed
		}

		contentStruct, err := structpb.NewStruct(contentMap)
		if err != nil {
			return fmt.Errorf("convert content to struct: %w", err)
		}

		block := &pb.Block{
			Type:    found.Meta.Type,
			Content: contentStruct,
		}

		// 2. Save the block to the server.
		c, err := client.New(grpcAddr)
		if err != nil {
			return err
		}
		defer c.Close()

		docName := doc.Meta.DocID
		_, err = c.UpdateBlock(cmd.Context(), docName, blockNumber, block)
		if err != nil {
			return fmt.Errorf("update block: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStderr(), "Block %d updated\n", blockNumber)

		// 3. Pull to get the latest version of the file after save.
		serverDoc, err := c.GetDoc(cmd.Context(), docName)
		if err != nil {
			return fmt.Errorf("get doc after update: %w", err)
		}

		blocks, err := c.ListBlocks(cmd.Context(), docName)
		if err != nil {
			return fmt.Errorf("list blocks after update: %w", err)
		}

		out, err := markdown.Render(serverDoc, blocks)
		if err != nil {
			return fmt.Errorf("render: %w", err)
		}

		if err := os.WriteFile(filePath, []byte(out), 0o644); err != nil {
			return fmt.Errorf("write file: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStderr(), "Written to %s\n", filePath)
		return nil
	},
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

	mdPushCmd.Flags().String("file", "", "Path to the markdown file (required)")
	mdPushCmd.Flags().Int32("block", 0, "Block number to push (required)")
	_ = mdPushCmd.MarkFlagRequired("file")
	_ = mdPushCmd.MarkFlagRequired("block")

	mdCmd.AddCommand(mdParseCmd, mdPullCmd, mdPushCmd)
	rootCmd.AddCommand(mdCmd)
}
