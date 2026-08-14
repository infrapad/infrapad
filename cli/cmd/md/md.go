// Package md implements the `infrapad md` command and its sub-commands.
package md

import "github.com/spf13/cobra"

// NewCmd builds the `md` command tree.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "md",
		Short: "Markdown utilities",
	}

	cmd.AddCommand(newParseCmd(), newPushCmd(), newPullCmd())
	return cmd
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
