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

	cmd.Flags().String("doc", "", "Document name or ID (required)")
	cmd.Flags().String("file", "", "Output file path (writes to stdout if omitted)")
	_ = cmd.MarkFlagRequired("doc")

	return cmd
}
