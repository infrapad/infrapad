package doc

import (
	"github.com/infrapad/infrapad/cli/pkg/cliutil"
	"github.com/infrapad/infrapad/cli/pkg/output"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all documents",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := cliutil.NewClient()
			if err != nil {
				return err
			}
			defer c.Close()

			docs, err := c.ListDocs(cmd.Context())
			if err != nil {
				return err
			}
			msgs := make([]proto.Message, len(docs))
			for i, d := range docs {
				msgs[i] = d
			}
			return cliutil.NewPrinter().PrintResourceList(msgs, output.DocColumns())
		},
	}
}
