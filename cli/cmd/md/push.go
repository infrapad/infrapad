package md

import (
	"fmt"
	"os"

	"github.com/infrapad/infrapad/cli/pkg/cliutil"
	"github.com/infrapad/infrapad/cli/pkg/markdown"
	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

func newPushCmd() *cobra.Command {
	cmd := &cobra.Command{
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
			c, err := cliutil.NewClient()
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

	cmd.Flags().String("file", "", "Path to the markdown file (required)")
	cmd.Flags().Int32("block", 0, "Block number to push (required)")
	_ = cmd.MarkFlagRequired("file")
	_ = cmd.MarkFlagRequired("block")

	return cmd
}
