package doc

import (
	"github.com/infrapad/infrapad/cli/pkg/cliutil"
	"github.com/infrapad/infrapad/cli/pkg/output"
	"github.com/spf13/cobra"
)

func newGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get [name]",
		Short: "Get a document by name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := cliutil.NewClient()
			if err != nil {
				return err
			}
			defer c.Close()

			doc, err := c.GetDoc(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return cliutil.NewPrinter().PrintResource(doc, output.DocColumns())
		},
	}
}
