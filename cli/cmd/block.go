package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/infrapad/infrapad/cli/pkg/client"
	"github.com/infrapad/infrapad/cli/pkg/output"
	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

var blockCmd = &cobra.Command{
	Use:   "block",
	Short: "Manage blocks within a document",
}

// --- add ---

var blockAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a block to a document",
	RunE: func(cmd *cobra.Command, args []string) error {
		parent, _ := cmd.Flags().GetString("doc")
		blockType, _ := cmd.Flags().GetString("type")
		contentJSON, _ := cmd.Flags().GetString("content")

		block, err := buildBlock(blockType, contentJSON)
		if err != nil {
			return err
		}

		c, err := client.New(grpcAddr)
		if err != nil {
			return err
		}
		defer c.Close()

		result, err := c.AddBlock(cmd.Context(), parent, block)
		if err != nil {
			return err
		}
		return newPrinter().PrintResource(result, output.BlockColumns())
	},
}

// --- update ---

var blockUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update an existing block",
	RunE: func(cmd *cobra.Command, args []string) error {
		parent, _ := cmd.Flags().GetString("doc")
		blockNumber, _ := cmd.Flags().GetInt32("block-number")
		blockType, _ := cmd.Flags().GetString("type")
		contentJSON, _ := cmd.Flags().GetString("content")

		block, err := buildBlock(blockType, contentJSON)
		if err != nil {
			return err
		}

		c, err := client.New(grpcAddr)
		if err != nil {
			return err
		}
		defer c.Close()

		result, err := c.UpdateBlock(cmd.Context(), parent, blockNumber, block)
		if err != nil {
			return err
		}
		return newPrinter().PrintResource(result, output.BlockColumns())
	},
}

// --- get ---

var blockGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get a block by number",
	RunE: func(cmd *cobra.Command, args []string) error {
		parent, _ := cmd.Flags().GetString("doc")
		blockNumber, _ := cmd.Flags().GetInt32("block-number")

		c, err := client.New(grpcAddr)
		if err != nil {
			return err
		}
		defer c.Close()

		result, err := c.GetBlock(cmd.Context(), parent, blockNumber)
		if err != nil {
			return err
		}
		return newPrinter().PrintResource(result, output.BlockColumns())
	},
}

// --- list ---

var blockListCmd = &cobra.Command{
	Use:   "list",
	Short: "List blocks for a document",
	RunE: func(cmd *cobra.Command, args []string) error {
		parent, _ := cmd.Flags().GetString("doc")

		c, err := client.New(grpcAddr)
		if err != nil {
			return err
		}
		defer c.Close()

		blocks, err := c.ListBlocks(cmd.Context(), parent)
		if err != nil {
			return err
		}
		msgs := make([]proto.Message, len(blocks))
		for i, b := range blocks {
			msgs[i] = b
		}
		return newPrinter().PrintResourceList(msgs, output.BlockColumns())
	},
}

// --- history ---

var blockHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Show revision history for a block",
	RunE: func(cmd *cobra.Command, args []string) error {
		parent, _ := cmd.Flags().GetString("doc")
		blockNumber, _ := cmd.Flags().GetInt32("block-number")

		c, err := client.New(grpcAddr)
		if err != nil {
			return err
		}
		defer c.Close()

		blocks, err := c.ListBlockHistory(cmd.Context(), parent, blockNumber)
		if err != nil {
			return err
		}
		msgs := make([]proto.Message, len(blocks))
		for i, b := range blocks {
			msgs[i] = b
		}
		return newPrinter().PrintResourceList(msgs, output.BlockColumns())
	},
}

func init() {
	// add
	blockAddCmd.Flags().String("doc", "", "Parent document name (required)")
	blockAddCmd.Flags().String("type", "", "Block type (required)")
	blockAddCmd.Flags().String("content", "{}", "Block content as JSON object")
	_ = blockAddCmd.MarkFlagRequired("doc")
	_ = blockAddCmd.MarkFlagRequired("type")

	// update
	blockUpdateCmd.Flags().String("doc", "", "Parent document name (required)")
	blockUpdateCmd.Flags().Int32("block-number", 0, "Block number to update (required)")
	blockUpdateCmd.Flags().String("type", "", "Block type (required)")
	blockUpdateCmd.Flags().String("content", "{}", "Block content as JSON object")
	_ = blockUpdateCmd.MarkFlagRequired("doc")
	_ = blockUpdateCmd.MarkFlagRequired("block-number")
	_ = blockUpdateCmd.MarkFlagRequired("type")

	// get
	blockGetCmd.Flags().String("doc", "", "Parent document name (required)")
	blockGetCmd.Flags().Int32("block-number", 0, "Block number (required)")
	_ = blockGetCmd.MarkFlagRequired("doc")
	_ = blockGetCmd.MarkFlagRequired("block-number")

	// list
	blockListCmd.Flags().String("doc", "", "Parent document name (required)")
	_ = blockListCmd.MarkFlagRequired("doc")

	// history
	blockHistoryCmd.Flags().String("doc", "", "Parent document name (required)")
	blockHistoryCmd.Flags().Int32("block-number", 0, "Block number (required)")
	_ = blockHistoryCmd.MarkFlagRequired("doc")
	_ = blockHistoryCmd.MarkFlagRequired("block-number")

	blockCmd.AddCommand(blockAddCmd, blockUpdateCmd, blockGetCmd, blockListCmd, blockHistoryCmd)
	rootCmd.AddCommand(blockCmd)
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
