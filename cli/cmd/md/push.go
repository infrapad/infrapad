package md

import (
	"fmt"
	"os"

	"github.com/infrapad/infrapad/cli/pkg/cliutil"
	"github.com/infrapad/infrapad/cli/pkg/markdown"
	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
)

func newPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push local changes back to the server",
		Long: `Push detects which blocks have been modified locally by comparing
with the current server state. Only changed existing blocks and new blocks
(block=new) are pushed. The file is refreshed from the server afterwards.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, _ := cmd.Flags().GetString("file")

			// 1. Parse the local file.
			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("read file: %w", err)
			}

			doc, err := markdown.Parse(data)
			if err != nil {
				return fmt.Errorf("parse: %w", err)
			}

			// 2. Connect to the server and fetch current blocks for diffing.
			c, err := cliutil.NewClient()
			if err != nil {
				return err
			}
			defer c.Close()

			docName := doc.Meta.DocID

			serverBlocks, err := c.ListBlocks(cmd.Context(), docName)
			if err != nil {
				return fmt.Errorf("list server blocks: %w", err)
			}

			remote := markdown.NewRemoteDoc(docName, serverBlocks)

			// 3. Detect changed and new blocks.
			actions, err := markdown.DiffBlocks(doc, remote)
			if err != nil {
				return err
			}

			if len(actions) == 0 {
				fmt.Fprintf(cmd.OutOrStderr(), "No changes detected\n")
				return nil
			}

			// 4. Push changed/new blocks.
			for _, a := range actions {
				contentStruct, err := structpb.NewStruct(a.Block.Content)
				if err != nil {
					return fmt.Errorf("convert content to struct: %w", err)
				}

				block := &pb.Block{
					Type:    a.Block.Meta.Type,
					Content: contentStruct,
				}

				if a.IsNew {
					_, err = c.AddBlock(cmd.Context(), docName, block)
					if err != nil {
						return fmt.Errorf("add block: %w", err)
					}
					fmt.Fprintf(cmd.OutOrStderr(), "Block added\n")
				} else {
					bn := int32(a.Block.Meta.BlockNumber)
					_, err = c.UpdateBlock(cmd.Context(), docName, bn, block)
					if err != nil {
						return fmt.Errorf("update block: %w", err)
					}
					fmt.Fprintf(cmd.OutOrStderr(), "Block %d updated\n", a.Block.Meta.BlockNumber)
				}
			}

			// 5. Pull to get the latest version of the file after save.
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
	_ = cmd.MarkFlagRequired("file")

	return cmd
}
