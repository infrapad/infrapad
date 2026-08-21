package md

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/infrapad/infrapad/cli/pkg/cliutil"
	"github.com/infrapad/infrapad/cli/pkg/markdown"
	"github.com/spf13/cobra"
)

func newPullCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Pull a document from the server and write it as infrapad-flavoured markdown",
		RunE: func(cmd *cobra.Command, args []string) error {
			docName, _ := cmd.Flags().GetString("doc")
			filePath, _ := cmd.Flags().GetString("file")

			// If --doc is not provided, try to extract the doc ID from an existing file.
			if docName == "" {
				if filePath == "" {
					return fmt.Errorf("either --doc or --file (pointing to a previously pulled file) is required")
				}
				data, err := os.ReadFile(filePath)
				if err != nil {
					return fmt.Errorf("read file to extract doc ID: %w", err)
				}
				doc, err := markdown.Parse(data)
				if err != nil {
					return fmt.Errorf("parse file to extract doc ID: %w", err)
				}
				if doc.Meta.DocID == "" {
					return fmt.Errorf("file %s has no doc ID in frontmatter; use --doc to specify", filePath)
				}
				docName = doc.Meta.DocID
			}

			c, err := cliutil.NewClient()
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

	cmd.Flags().String("doc", "", "Document name or ID (required for new files)")
	cmd.Flags().String("file", "", "Output file path (writes to stdout if omitted)")

	return cmd
}
