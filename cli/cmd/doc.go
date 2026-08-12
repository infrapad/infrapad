package cmd

import (
	"fmt"

	"github.com/infrapad/infrapad/cli/pkg/client"
	"github.com/infrapad/infrapad/cli/pkg/output"
	"github.com/spf13/cobra"
)

var docCmd = &cobra.Command{
	Use:   "doc",
	Short: "Manage documents",
}

var docCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new document",
	RunE: func(cmd *cobra.Command, args []string) error {
		title, _ := cmd.Flags().GetString("title")
		namespace, _ := cmd.Flags().GetString("namespace")

		c, err := client.New(grpcAddr)
		if err != nil {
			return err
		}
		defer c.Close()

		doc, err := c.CreateDoc(cmd.Context(), title, namespace)
		if err != nil {
			return err
		}
		output.PrintDoc(doc)
		return nil
	},
}

var docGetCmd = &cobra.Command{
	Use:   "get [name]",
	Short: "Get a document by name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := client.New(grpcAddr)
		if err != nil {
			return err
		}
		defer c.Close()

		doc, err := c.GetDoc(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		output.PrintDoc(doc)
		return nil
	},
}

var docListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all documents",
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := client.New(grpcAddr)
		if err != nil {
			return err
		}
		defer c.Close()

		docs, err := c.ListDocs(cmd.Context())
		if err != nil {
			return err
		}
		for _, d := range docs {
			fmt.Printf("%s\t%s\t%s\n", d.GetName(), d.GetNamespace(), d.GetTitle())
		}
		return nil
	},
}

func init() {
	docCreateCmd.Flags().String("title", "", "Document title (required)")
	docCreateCmd.Flags().String("namespace", "", "Document namespace (required)")
	_ = docCreateCmd.MarkFlagRequired("title")
	_ = docCreateCmd.MarkFlagRequired("namespace")

	docCmd.AddCommand(docCreateCmd, docGetCmd, docListCmd)
	rootCmd.AddCommand(docCmd)
}
