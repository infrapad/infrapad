// Package block implements the `infrapad block` command and its sub-commands.
package block

import (
	"encoding/json"
	"fmt"

	"github.com/infrapad/infrapad/cli/pkg/content"
	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"github.com/spf13/cobra"
)

// NewCmd builds the `block` command tree.
func NewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "block",
		Short: "Manage blocks within a document",
	}

	cmd.AddCommand(
		newAddCmd(),
		newUpdateCmd(),
		newGetCmd(),
		newListCmd(),
		newHistoryCmd(),
	)
	return cmd
}

// buildBlock constructs a protobuf Block from CLI flags.
func buildBlock(blockType, contentJSON string) (*pb.Block, error) {
	block := &pb.Block{Type: blockType}

	var raw map[string]any
	if err := json.Unmarshal([]byte(contentJSON), &raw); err != nil {
		return nil, fmt.Errorf("invalid --content JSON: %w", err)
	}

	record, err := content.FromContent(raw)
	if err != nil {
		return nil, err
	}
	block.Record = record

	return block, nil
}
