package block

import (
	"github.com/infrapad/infrapad/cli/pkg/cliutil"
	"github.com/infrapad/infrapad/cli/pkg/output"
	"github.com/spf13/cobra"
)

func newGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Get a block by number",
		RunE: func(cmd *cobra.Command, args []string) error {
			parent, _ := cmd.Flags().GetString("document")
			blockNumber, _ := cmd.Flags().GetInt32("block-number")

			c, err := cliutil.NewClient()
			if err != nil {
				return err
			}
			defer c.Close()

			result, err := c.GetBlock(cmd.Context(), parent, blockNumber)
			if err != nil {
				return err
			}
			return cliutil.NewPrinter().PrintResource(result, output.BlockColumns())
		},
	}

	cmd.Flags().String("document", "", "Parent document name (required)")
	cmd.Flags().Int32("block-number", 0, "Block number (required)")
	_ = cmd.MarkFlagRequired("document")
	_ = cmd.MarkFlagRequired("block-number")

	return cmd
}
