package document

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

			documents, err := c.ListDocuments(cmd.Context())
			if err != nil {
				return err
			}
			msgs := make([]proto.Message, len(documents))
			for i, d := range documents {
				msgs[i] = d
			}
			return cliutil.NewPrinter().PrintResourceList(msgs, output.DocumentColumns())
		},
	}
}
