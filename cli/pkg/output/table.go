package output

import (
	"fmt"
	"io"
	"strings"

	"google.golang.org/protobuf/proto"
)

func printTable(w io.Writer, msgs []proto.Message, columns []Column) error {
	if len(columns) == 0 {
		return nil
	}

	// Split columns into inline (normal) and full-row columns.
	var inline []Column
	var fullRow []Column
	for _, c := range columns {
		if c.FullRow {
			fullRow = append(fullRow, c)
		} else {
			inline = append(inline, c)
		}
	}

	if len(inline) == 0 {
		return nil
	}

	widths := make([]int, len(inline))
	for i, c := range inline {
		widths[i] = len(c.Header)
	}

	type row struct {
		cells    []string
		fullRows []string // rendered full-row blocks
	}

	rows := make([]row, len(msgs))
	for r, msg := range msgs {
		cells := make([]string, len(inline))
		for c, col := range inline {
			cells[c] = col.Value(msg)
			if len(cells[c]) > widths[c] {
				widths[c] = len(cells[c])
			}
		}
		var fr []string
		for _, col := range fullRow {
			v := col.Value(msg)
			if v != "" {
				fr = append(fr, renderFullRow(col.Header, v))
			}
		}
		rows[r] = row{cells: cells, fullRows: fr}
	}

	fmts := make([]string, len(inline))
	for i, width := range widths {
		if i == len(inline)-1 {
			fmts[i] = "%s"
		} else {
			fmts[i] = fmt.Sprintf("%%-%ds", width)
		}
	}

	headers := make([]string, len(inline))
	for i, c := range inline {
		headers[i] = fmt.Sprintf(fmts[i], strings.ToUpper(c.Header))
	}
	if _, err := fmt.Fprintln(w, strings.Join(headers, "   ")); err != nil {
		return err
	}

	for _, r := range rows {
		cells := make([]string, len(inline))
		for i, val := range r.cells {
			cells[i] = fmt.Sprintf(fmts[i], val)
		}
		if _, err := fmt.Fprintln(w, strings.Join(cells, "   ")); err != nil {
			return err
		}
		for _, block := range r.fullRows {
			if _, err := fmt.Fprint(w, block); err != nil {
				return err
			}
		}
	}

	return nil
}

// renderFullRow formats a full-row value as a fenced block.
func renderFullRow(header, value string) string {
	var b strings.Builder
	b.WriteString("```")
	b.WriteString(strings.ToUpper(header))
	b.WriteString("\n")
	for _, line := range strings.Split(value, "\n") {
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("```\n")
	return b.String()
}
