package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/infrapad/infrapad/cli/pkg/client"
	"github.com/infrapad/infrapad/cli/pkg/output"
	pb "github.com/infrapad/infrapad/proto/gen/go/infrapad/v1alpha1"
	"github.com/spf13/cobra"
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
		text, _ := cmd.Flags().GetString("text")
		matchersJSON, _ := cmd.Flags().GetString("matchers")

		block, err := buildBlock(blockType, text, matchersJSON)
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
		output.PrintBlock(result)
		return nil
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
		text, _ := cmd.Flags().GetString("text")
		matchersJSON, _ := cmd.Flags().GetString("matchers")

		block, err := buildBlock(blockType, text, matchersJSON)
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
		output.PrintBlock(result)
		return nil
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
		output.PrintBlock(result)
		return nil
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
		for _, b := range blocks {
			fmt.Printf("block=%d  rev=%d  type=%s\n", b.GetBlockNumber(), b.GetRevisionNumber(), b.GetType())
		}
		return nil
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
		for _, b := range blocks {
			output.PrintBlock(b)
			fmt.Println("---")
		}
		return nil
	},
}

func init() {
	// add
	blockAddCmd.Flags().String("doc", "", "Parent document name (required)")
	blockAddCmd.Flags().String("type", "", "Block type: markdown, alerts_matcher (required)")
	blockAddCmd.Flags().String("text", "", "Markdown text (for type=markdown)")
	blockAddCmd.Flags().String("matchers", "", "JSON array of label matchers (for type=alerts_matcher)")
	_ = blockAddCmd.MarkFlagRequired("doc")
	_ = blockAddCmd.MarkFlagRequired("type")

	// update
	blockUpdateCmd.Flags().String("doc", "", "Parent document name (required)")
	blockUpdateCmd.Flags().Int32("block-number", 0, "Block number to update (required)")
	blockUpdateCmd.Flags().String("type", "", "Block type: markdown, alerts_matcher (required)")
	blockUpdateCmd.Flags().String("text", "", "Markdown text (for type=markdown)")
	blockUpdateCmd.Flags().String("matchers", "", "JSON array of label matchers (for type=alerts_matcher)")
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
func buildBlock(blockType, text, matchersJSON string) (*pb.Block, error) {
	block := &pb.Block{Type: blockType}

	switch blockType {
	case "markdown":
		block.Content = &pb.Block_Markdown{
			Markdown: &pb.MarkdownContent{Text: text},
		}
	case "alerts_matcher":
		var raw []map[string][]string
		if err := json.Unmarshal([]byte(matchersJSON), &raw); err != nil {
			return nil, fmt.Errorf("invalid --matchers JSON: %w", err)
		}
		var matchers []*pb.LabelsMatcher
		for _, m := range raw {
			labels := make(map[string]*pb.LabelValues)
			for k, v := range m {
				labels[k] = &pb.LabelValues{Values: v}
			}
			matchers = append(matchers, &pb.LabelsMatcher{Labels: labels})
		}
		block.Content = &pb.Block_AlertsMatcher{
			AlertsMatcher: &pb.AlertsMatcherContent{
				LabelsMatchers: matchers,
			},
		}
	default:
		return nil, fmt.Errorf("unsupported block type: %s", blockType)
	}

	return block, nil
}
