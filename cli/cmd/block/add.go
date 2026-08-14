package block

import (
	"github.com/infrapad/infrapad/cli/pkg/cliutil"
	"github.com/infrapad/infrapad/cli/pkg/output"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a block to a document",
		RunE: func(cmd *cobra.Command, args []string) error {
			parent, _ := cmd.Flags().GetString("doc")
			blockType, _ := cmd.Flags().GetString("type")
			contentJSON, _ := cmd.Flags().GetString("content")

			block, err := buildBlock(blockType, contentJSON)
			if err != nil {
				return err
			}

			c, err := cliutil.NewClient()
			if err != nil {
				return err
			}
			defer c.Close()

			result, err := c.AddBlock(cmd.Context(), parent, block)
			if err != nil {
				return err
			}
			return cliutil.NewPrinter().PrintResource(result, output.BlockColumns())
		},
	}

	cmd.Flags().String("doc", "", "Parent document name (required)")
	cmd.Flags().String("type", "", "Block type (required)")
	cmd.Flags().String("content", "{}", "Block content as JSON object")
	_ = cmd.MarkFlagRequired("doc")
	_ = cmd.MarkFlagRequired("type")

	return cmd
}
