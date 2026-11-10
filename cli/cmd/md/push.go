package md

import (
	"errors"
	"fmt"
	"os"

	"github.com/infrapad/infrapad/cli/pkg/cliutil"
	"github.com/infrapad/infrapad/shared/markdown"
	"github.com/spf13/cobra"
)

func newPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push local changes back to the server",
		Long: "Push submits the whole Markdown file unchanged to the server, which interprets\n" +
			"the blocks, detects changed existing blocks and new blocks (block=new), and\n" +
			"applies them atomically. Omitted blocks are left untouched.\n\n" +
			"The file is refreshed with the Markdown returned by the server afterwards,\n" +
			"also when no changes were detected.",
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, _ := cmd.Flags().GetString("file")

			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("read file: %w", err)
			}

			// Only the frontmatter is inspected locally; block interpretation
			// is owned by the server.
			meta, err := markdown.ReadFrontmatter(data)
			if errors.Is(err, markdown.ErrMissingDocumentID) {
				return fmt.Errorf("file %s has no document ID in frontmatter", filePath)
			}
			if err != nil {
				return fmt.Errorf("read frontmatter of %s: %w", filePath, err)
			}

			c, err := cliutil.NewClient()
			if err != nil {
				return err
			}
			defer c.Close()

			resp, err := c.PushDocumentMarkdown(cmd.Context(), meta.DocumentID, string(data))
			if err != nil {
				return err
			}

			changes := resp.GetChanges()
			if len(changes) == 0 {
				fmt.Fprintf(cmd.OutOrStderr(), "No changes detected\n")
			}

			for _, change := range changes {
				switch change.GetAction() {
				case "added":
					fmt.Fprintf(cmd.OutOrStderr(), "Block added\n")
				case "updated":
					fmt.Fprintf(cmd.OutOrStderr(), "Block %d updated\n", change.GetBlockNumber())
				default:
					fmt.Fprintf(cmd.OutOrStderr(), "Block %d %s\n", change.GetBlockNumber(), change.GetAction())
				}
			}

			// Server changes are already committed; a write failure here
			// cannot undo them and is reported without retrying.
			if err := os.WriteFile(filePath, []byte(resp.GetMarkdown()), 0o644); err != nil {
				return fmt.Errorf("write file (server changes were applied): %w", err)
			}

			fmt.Fprintf(cmd.OutOrStderr(), "Written to %s\n", filePath)
			return nil
		},
	}

	cmd.Flags().String("file", "", "Path to the markdown file (required)")
	_ = cmd.MarkFlagRequired("file")

	return cmd
}
