package block

import (
	"github.com/infrapad/infrapad/cli/pkg/cliutil"
	"github.com/infrapad/infrapad/cli/pkg/output"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
)

func newHistoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show revision history for a block",
		RunE: func(cmd *cobra.Command, args []string) error {
			parent, _ := cmd.Flags().GetString("doc")
			blockNumber, _ := cmd.Flags().GetInt32("block-number")

			c, err := cliutil.NewClient()
			if err != nil {
				return err
			}
			defer c.Close()

			blocks, err := c.ListBlockHistory(cmd.Context(), parent, blockNumber)
			if err != nil {
				return err
			}
			msgs := make([]proto.Message, len(blocks))
			for i, b := range blocks {
				msgs[i] = b
			}
			return cliutil.NewPrinter().PrintResourceList(msgs, output.BlockColumns())
		},
	}

	cmd.Flags().String("doc", "", "Parent document name (required)")
	cmd.Flags().Int32("block-number", 0, "Block number (required)")
	_ = cmd.MarkFlagRequired("doc")
	_ = cmd.MarkFlagRequired("block-number")

	return cmd
}
