package doc

import (
	"github.com/infrapad/infrapad/cli/pkg/cliutil"
	"github.com/infrapad/infrapad/cli/pkg/output"
	"github.com/spf13/cobra"
)

func newCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new document",
		RunE: func(cmd *cobra.Command, args []string) error {
			title, _ := cmd.Flags().GetString("title")
			namespace, _ := cmd.Flags().GetString("namespace")

			c, err := cliutil.NewClient()
			if err != nil {
				return err
			}
			defer c.Close()

			doc, err := c.CreateDoc(cmd.Context(), title, namespace)
			if err != nil {
				return err
			}
			return cliutil.NewPrinter().PrintResource(doc, output.DocColumns())
		},
	}

	cmd.Flags().String("title", "", "Document title (required)")
	cmd.Flags().String("namespace", "", "Document namespace (required)")
	_ = cmd.MarkFlagRequired("title")
	_ = cmd.MarkFlagRequired("namespace")

	return cmd
}
