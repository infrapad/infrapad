package md

import (
	"errors"
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
		Long: "Pull a document from the server and write it as infrapad-flavoured markdown.\n\n" +
			"The Markdown is rendered by the server and written unchanged. With --file only,\n" +
			"the document ID is read from the existing file's frontmatter and the file is refreshed.",
		RunE: func(cmd *cobra.Command, args []string) error {
			docName, _ := cmd.Flags().GetString("document")
			filePath, _ := cmd.Flags().GetString("file")

			// An explicit --document takes precedence; otherwise discover the
			// document ID from the existing file's frontmatter.
			if docName == "" {
				if filePath == "" {
					return fmt.Errorf("either --document or --file (pointing to a previously pulled file) is required")
				}
				id, err := documentIDFromFile(filePath)
				if err != nil {
					return err
				}
				docName = id
			}

			c, err := cliutil.NewClient()
			if err != nil {
				return err
			}
			defer c.Close()

			// Export completes before touching the destination, so failures
			// leave an existing file unchanged.
			out, err := c.ExportDocumentMarkdown(cmd.Context(), docName)
			if err != nil {
				return err
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

	cmd.Flags().String("document", "", "Document name or ID (required for new files)")
	cmd.Flags().String("file", "", "Output file path (writes to stdout if omitted)")

	return cmd
}

// documentIDFromFile reads the document ID from the frontmatter of a
// previously pulled file without interpreting its blocks.
func documentIDFromFile(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file to extract document ID: %w", err)
	}
	meta, err := markdown.ReadFrontmatter(data)
	if errors.Is(err, markdown.ErrMissingDocumentID) {
		return "", fmt.Errorf("file %s has no document ID in frontmatter; use --document to specify", filePath)
	}
	if err != nil {
		return "", fmt.Errorf("read frontmatter of %s: %w", filePath, err)
	}
	return meta.DocumentID, nil
}
