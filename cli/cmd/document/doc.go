// Package document implements the `infrapad document` command and its sub-commands.
package document

import "github.com/spf13/cobra"

// NewCmd builds the `document` command tree.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "document",
		Short: "Manage documents",
	}

	cmd.AddCommand(newCreateCmd(), newGetCmd(), newListCmd())
	return cmd
}
