// Package cliutil holds state and helpers shared across the CLI's command
// packages (e.g. the global --grpc-addr and --output flags).
package cliutil

import (
	"os"

	"github.com/infrapad/infrapad/cli/pkg/client"
	"github.com/infrapad/infrapad/cli/pkg/output"
)

var (
	// GRPCAddr is bound to the persistent --grpc-addr flag on the root command.
	GRPCAddr string
	// OutputFormat is bound to the persistent --output flag on the root command.
	OutputFormat string
)

// NewClient returns a gRPC client configured from the global --grpc-addr flag.
func NewClient() (*client.Client, error) {
	return client.New(GRPCAddr)
}

// NewPrinter returns a Printer configured from the global --output flag.
func NewPrinter() *output.Printer {
	return output.NewPrinter(os.Stdout, output.Format(OutputFormat))
}
