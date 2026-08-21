package block

import (
	"github.com/infrapad/infrapad/cli/pkg/cliutil"
	"github.com/infrapad/infrapad/cli/pkg/output"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List blocks for a document",
		RunE: func(cmd *cobra.Command, args []string) error {
			parent, _ := cmd.Flags().GetString("document")

			c, err := cliutil.NewClient()
			if err != nil {
				return err
			}
			defer c.Close()

			blocks, err := c.ListBlocks(cmd.Context(), parent)
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

	cmd.Flags().String("document", "", "Parent document name (required)")
	_ = cmd.MarkFlagRequired("document")

	return cmd
}
