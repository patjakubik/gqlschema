package gqlschema

import (
	"strings"
	"unicode/utf8"
)

// FixMarkdownTables normalizes GitHub-flavored-Markdown tables inside every
// schema description so they render as tables and read as tables in plain
// text: it inserts a blank line between a table and adjacent paragraph text
// (GFM tables cannot interrupt a paragraph), pads rows that have fewer cells
// than their header, and aligns every column to its widest cell so the pipes
// line up. Cell content is never changed, and descriptions without tables
// pass through untouched. Shopify's generated `query`-argument docs need all
// of this. The transform is idempotent.
func (s *Schema) FixMarkdownTables() {
	for i := range s.Directives {
		fixDescription(s.Directives[i].Description)
		fixArgDescriptions(s.Directives[i].Args)
	}
	for i := range s.Types {
		t := &s.Types[i]
		fixDescription(t.Description)
		for j := range t.Fields {
			fixDescription(t.Fields[j].Description)
			fixArgDescriptions(t.Fields[j].Args)
		}
		fixArgDescriptions(t.InputFields)
		for j := range t.EnumValues {
			fixDescription(t.EnumValues[j].Description)
		}
	}
}

func fixArgDescriptions(args []InputValue) {
	for i := range args {
		fixDescription(args[i].Description)
	}
}

func fixDescription(d *string) {
	if d == nil || *d == "" {
		return
	}
	*d = fixMarkdownTables(*d)
}

func fixMarkdownTables(text string) string {
	if !strings.Contains(text, "|") {
		return text
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		// A table starts at a header row immediately followed by a separator.
		if !isTableRow(lines[i]) || i+1 >= len(lines) || !isSeparatorRow(lines[i+1]) {
			out = append(out, lines[i])
			i++
			continue
		}
		header := splitCells(lines[i])
		sep := splitCells(lines[i+1])
		var rows [][]string
		i += 2
		for i < len(lines) && isTableRow(lines[i]) && !isSeparatorRow(lines[i]) {
			rows = append(rows, splitCells(lines[i]))
			i++
		}
		if n := len(out); n > 0 && strings.TrimSpace(out[n-1]) != "" {
			out = append(out, "")
		}
		out = append(out, renderTable(header, sep, rows)...)
		if i < len(lines) && strings.TrimSpace(lines[i]) != "" {
			out = append(out, "")
		}
	}
	return strings.Join(out, "\n")
}

func isTableRow(s string) bool {
	t := strings.TrimSpace(s)
	return len(t) > 1 && strings.HasPrefix(t, "|") && strings.HasSuffix(t, "|")
}

// isSeparatorRow reports a delimiter row like `| ---- | :--- |`.
func isSeparatorRow(s string) bool {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "|") || !strings.Contains(t, "-") {
		return false
	}
	return strings.TrimLeft(t, "-:| \t") == ""
}

func splitCells(row string) []string {
	t := strings.TrimSpace(row)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

// renderTable re-emits a table with short rows padded to the header's column
// count and every column aligned to its widest cell. GFM ignores the padding,
// so this only affects how the table reads as plain text.
func renderTable(header, sep []string, rows [][]string) []string {
	ncols := len(header)
	widths := make([]int, ncols)
	for j, c := range header {
		widths[j] = max(3, utf8.RuneCountInString(c))
	}
	for _, r := range rows {
		for j := 0; j < ncols && j < len(r); j++ {
			widths[j] = max(widths[j], utf8.RuneCountInString(r[j]))
		}
	}

	lines := make([]string, 0, len(rows)+2)
	lines = append(lines, renderRow(header, widths))
	lines = append(lines, renderRow(separatorCells(sep, widths), widths))
	for _, r := range rows {
		for len(r) < ncols {
			r = append(r, "")
		}
		lines = append(lines, renderRow(r, widths))
	}
	return lines
}

func renderRow(cells []string, widths []int) string {
	var b strings.Builder
	b.WriteString("|")
	for j, c := range cells {
		w := 0
		if j < len(widths) {
			w = widths[j]
		}
		b.WriteString(" ")
		b.WriteString(c)
		b.WriteString(strings.Repeat(" ", max(0, w-utf8.RuneCountInString(c))))
		b.WriteString(" |")
	}
	return b.String()
}

// separatorCells rebuilds the delimiter row at the aligned widths, keeping
// any GFM alignment colons from the original.
func separatorCells(sep []string, widths []int) []string {
	cells := make([]string, len(widths))
	for j, w := range widths {
		var orig string
		if j < len(sep) {
			orig = sep[j]
		}
		left := strings.HasPrefix(orig, ":")
		right := strings.HasSuffix(orig, ":") && len(orig) > 1
		n := w
		if left {
			n--
		}
		if right {
			n--
		}
		c := strings.Repeat("-", max(1, n))
		if left {
			c = ":" + c
		}
		if right {
			c += ":"
		}
		cells[j] = c
	}
	return cells
}
