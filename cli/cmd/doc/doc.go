// Package doc implements the `infrapad doc` command and its sub-commands.
package doc

import "github.com/spf13/cobra"

// NewCmd builds the `doc` command tree.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doc",
		Short: "Manage documents",
	}

	cmd.AddCommand(newCreateCmd(), newGetCmd(), newListCmd())
	return cmd
}
