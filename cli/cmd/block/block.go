// Package block implements the `infrapad block` command and its sub-commands.
package block

import (
	"encoding/json"
	"fmt"

	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
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

	s, err := structpb.NewStruct(raw)
	if err != nil {
		return nil, fmt.Errorf("convert content to struct: %w", err)
	}
	block.Content = s

	return block, nil
}
