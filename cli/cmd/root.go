package cmd

import (
	"fmt"
	"os"

	"github.com/infrapad/infrapad/cli/pkg/output"
	"github.com/spf13/cobra"
)

var (
	grpcAddr     string
	outputFormat string
)

var rootCmd = &cobra.Command{
	Use:   "infrapad",
	Short: "Infrapad CLI",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return output.Format(outputFormat).Validate()
	},
}

func init() {
	defaultAddr := "localhost:50051"
	if s := os.Getenv("GRPC_ADDR"); s != "" {
		defaultAddr = s
	}
	rootCmd.PersistentFlags().StringVar(&grpcAddr, "grpc-addr", defaultAddr, "gRPC server address")
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "table", "Output format: table, json")
}

// newPrinter returns a Printer configured from the global --output flag.
func newPrinter() *output.Printer {
	return output.NewPrinter(os.Stdout, output.Format(outputFormat))
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
