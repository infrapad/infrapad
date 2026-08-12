package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var grpcAddr string

var rootCmd = &cobra.Command{
	Use:   "infrapad",
	Short: "Infrapad CLI",
}

func init() {
	defaultAddr := "localhost:50051"
	if s := os.Getenv("GRPC_ADDR"); s != "" {
		defaultAddr = s
	}
	rootCmd.PersistentFlags().StringVar(&grpcAddr, "grpc-addr", defaultAddr, "gRPC server address")
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
