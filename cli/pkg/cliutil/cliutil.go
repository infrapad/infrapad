// Package cliutil holds state and helpers shared across the CLI's command
// packages (e.g. the global --api-url and --output flags).
package cliutil

import (
	"os"

	"github.com/infrapad/infrapad/cli/pkg/client"
	"github.com/infrapad/infrapad/cli/pkg/output"
)

var (
	// APIURL is bound to the persistent --api-url flag on the root command.
	APIURL string
	// OutputFormat is bound to the persistent --output flag on the root command.
	OutputFormat string
)

// NewClient returns an HTTP client configured from the global --api-url flag.
func NewClient() (*client.Client, error) {
	return client.New(APIURL)
}

// NewPrinter returns a Printer configured from the global --output flag.
func NewPrinter() *output.Printer {
	return output.NewPrinter(os.Stdout, output.Format(OutputFormat))
}
