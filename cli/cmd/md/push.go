package md

import (
	"fmt"
	"os"
	"strconv"

	"github.com/infrapad/infrapad/cli/pkg/cliutil"
	"github.com/infrapad/infrapad/cli/pkg/markdown"
	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
)

func newPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push local changes to a block back to the server",
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, _ := cmd.Flags().GetString("file")
			blockFlag, _ := cmd.Flags().GetString("block")

			isNew := blockFlag == "new"

			// 1. Parse the file to extract the referenced block(s).
			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("read file: %w", err)
			}

			doc, err := markdown.Parse(data)
			if err != nil {
				return fmt.Errorf("parse: %w", err)
			}

			var targets []*markdown.ParsedBlock
			if isNew {
				for i := range doc.Blocks {
					if doc.Blocks[i].Meta.IsNew {
						targets = append(targets, &doc.Blocks[i])
					}
				}
				if len(targets) == 0 {
					return fmt.Errorf("new block not found in %s", filePath)
				}
			} else {
				blockNumber, err := strconv.ParseInt(blockFlag, 10, 32)
				if err != nil {
					return fmt.Errorf("invalid block flag %q: must be a number or \"new\"", blockFlag)
				}
				for i := range doc.Blocks {
					if int64(doc.Blocks[i].Meta.BlockNumber) == blockNumber {
						targets = append(targets, &doc.Blocks[i])
						break
					}
				}
				if len(targets) == 0 {
					return fmt.Errorf("block %d not found in %s", blockNumber, filePath)
				}
			}

			// 2. Save the block(s) to the server.
			c, err := cliutil.NewClient()
			if err != nil {
				return err
			}
			defer c.Close()

			docName := doc.Meta.DocID
			for _, found := range targets {
				contentStruct, err := structpb.NewStruct(found.Content)
				if err != nil {
					return fmt.Errorf("convert content to struct: %w", err)
				}

				block := &pb.Block{
					Type:    found.Meta.Type,
					Content: contentStruct,
				}

				if isNew {
					_, err = c.AddBlock(cmd.Context(), docName, block)
					if err != nil {
						return fmt.Errorf("add block: %w", err)
					}
					fmt.Fprintf(cmd.OutOrStderr(), "Block added\n")
				} else {
					blockNumber, _ := strconv.ParseInt(blockFlag, 10, 32)
					_, err = c.UpdateBlock(cmd.Context(), docName, int32(blockNumber), block)
					if err != nil {
						return fmt.Errorf("update block: %w", err)
					}
					fmt.Fprintf(cmd.OutOrStderr(), "Block %d updated\n", blockNumber)
				}
			}

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
	cmd.Flags().String("block", "", "Block number to push, or \"new\" for a new block (required)")
	_ = cmd.MarkFlagRequired("file")
	_ = cmd.MarkFlagRequired("block")

	return cmd
}
