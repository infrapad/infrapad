package cmd

import (
	"fmt"
	"os"

	"github.com/infrapad/infrapad/cli/cmd/block"
	"github.com/infrapad/infrapad/cli/cmd/document"
	"github.com/infrapad/infrapad/cli/cmd/md"
	"github.com/infrapad/infrapad/cli/pkg/cliutil"
	"github.com/infrapad/infrapad/cli/pkg/output"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "infrapad",
	Short: "Infrapad CLI",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return output.Format(cliutil.OutputFormat).Validate()
	},
}

func init() {
	defaultAddr := "localhost:50061"
	if s := os.Getenv("GRPC_ADDR"); s != "" {
		defaultAddr = s
	}
	rootCmd.PersistentFlags().StringVar(&cliutil.GRPCAddr, "grpc-addr", defaultAddr, "gRPC server address")
	rootCmd.PersistentFlags().StringVarP(&cliutil.OutputFormat, "output", "o", "table", "Output format: table, json")

	rootCmd.AddCommand(document.NewCmd(), block.NewCmd(), md.NewCmd())
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
