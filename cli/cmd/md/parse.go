package md

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/infrapad/infrapad/shared/markdown"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

func newParseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "parse",
		Short: "Parse an infrapad-flavoured markdown file and print its structure",
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, _ := cmd.Flags().GetString("file")

			data, err := os.ReadFile(filePath)
			if err != nil {
				return fmt.Errorf("read file: %w", err)
			}

			doc, err := markdown.Parse(string(data))
			if err != nil {
				return fmt.Errorf("parse: %w", err)
			}
			return printDocument(cmd.OutOrStdout(), doc)
		},
	}

	cmd.Flags().String("file", "", "Path to the markdown file (required)")
	_ = cmd.MarkFlagRequired("file")

	return cmd
}

// printDocument writes the human-readable structure of doc.
func printDocument(w io.Writer, doc markdown.Document) error {
	fm := doc.Frontmatter
	fmt.Fprintln(w, "Document:")
	fmt.Fprintf(w, "  document:  %s\n", fm.DocumentID)
	fmt.Fprintf(w, "  title:     %s\n", fm.Title)
	fmt.Fprintf(w, "  namespace: %s\n", fm.Namespace)
	fmt.Fprintf(w, "  status:    %s\n", fm.Status)
	fmt.Fprintln(w)

	fmt.Fprintf(w, "Blocks (%d):\n", len(doc.Blocks))
	for i, b := range doc.Blocks {
		if i > 0 {
			fmt.Fprintln(w)
		}
		if err := printBlock(w, b); err != nil {
			return fmt.Errorf("block %d: %w", b.Index, err)
		}
	}
	return nil
}

// printBlock writes the directive metadata, the optional structured header
// (generic YAML; `{}` when present but empty, `none` when absent), and the
// body text of b.
func printBlock(w io.Writer, b markdown.Block) error {
	d := b.Directive
	field := func(name, value string) { fmt.Fprintf(w, "  %-10s %s\n", name+":", value) }

	fmt.Fprintf(w, "  --- block #%d (line %d) ---\n", b.Index, b.Line)
	field("type", d.Type)
	if d.New {
		field("block", markdown.NewBlockValue)
	} else {
		field("block", strconv.Itoa(d.BlockNumber))
		field("rev", strconv.Itoa(d.Revision))
	}
	if d.Author != "" {
		field("author", d.Author)
	}
	if d.Parent != nil {
		field("parent", strconv.Itoa(*d.Parent))
	}
	if d.Timestamp != nil {
		field("timestamp", d.Timestamp.Format(time.RFC3339Nano))
	}

	switch {
	case !b.HasHeader:
		field("header", "none")
	case len(b.Header) == 0:
		field("header", "{}")
	default:
		var out strings.Builder
		enc := yaml.NewEncoder(&out)
		enc.SetIndent(2)
		if err := enc.Encode(b.Header); err != nil {
			return fmt.Errorf("render header: %w", err)
		}
		if err := enc.Close(); err != nil {
			return fmt.Errorf("render header: %w", err)
		}
		fmt.Fprintln(w, "  header:")
		printIndented(w, out.String())
	}

	fmt.Fprintln(w, "  body:")
	printIndented(w, b.Body)
	return nil
}

// printIndented writes text indented for inspection, without trailing line
// breaks (the blank lines separating blocks).
func printIndented(w io.Writer, text string) {
	for _, line := range splitLines(strings.TrimRight(text, "\n")) {
		if line == "" {
			fmt.Fprintln(w)
			continue
		}
		fmt.Fprintf(w, "    %s\n", line)
	}
}
