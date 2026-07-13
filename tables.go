package gqlschema

import "strings"

// FixMarkdownTables normalizes GitHub-flavored-Markdown tables inside every
// schema description so they render as tables: it inserts a blank line between
// a table and adjacent paragraph text (GFM tables cannot interrupt a
// paragraph) and pads rows that have fewer cells than their header. Cell
// content is never changed, and descriptions without tables pass through
// untouched. Shopify's generated `query`-argument docs need both fixes. The
// transform is idempotent.
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
		if n := len(out); n > 0 && strings.TrimSpace(out[n-1]) != "" {
			out = append(out, "")
		}
		ncols := cellCount(lines[i])
		out = append(out, lines[i], lines[i+1])
		i += 2
		for i < len(lines) && isTableRow(lines[i]) && !isSeparatorRow(lines[i]) {
			out = append(out, padRow(lines[i], ncols))
			i++
		}
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

func cellCount(row string) int {
	return strings.Count(row, "|") - 1
}

// padRow appends empty cells to a row that is shorter than its header, so
// e.g. `| status | string |` under a 6-column header gains four empty cells.
func padRow(row string, ncols int) string {
	got := cellCount(row)
	if got >= ncols {
		return row
	}
	return strings.TrimRight(row, " \t") + strings.Repeat(" |", ncols-got)
}
