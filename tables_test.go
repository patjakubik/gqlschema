package gqlschema

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A trimmed real Shopify `query`-argument description: table glued to the
// paragraphs around it, and a truncated row with 2 of 6 cells.
const brokenTable = "A filter made up of terms, connectives, modifiers, and comparators.\n" +
	"| name | type | description | acceptable_values | default_value | example_use |\n" +
	"| ---- | ---- | ---- | ---- | ---- | ---- |\n" +
	"| default | string | Filter by a case-insensitive search. | | | - `query=Bob Norman` |\n" +
	"| status | string |\n" +
	"You can apply one or more filters to a query."

func TestFixMarkdownTables(t *testing.T) {
	got := fixMarkdownTables(brokenTable)
	lines := strings.Split(got, "\n")

	// Blank lines separate the table from the surrounding paragraphs.
	if lines[1] != "" {
		t.Errorf("missing blank line before table:\n%s", got)
	}
	if lines[len(lines)-2] != "" {
		t.Errorf("missing blank line after table:\n%s", got)
	}

	// All four table lines are aligned: same width, pipes in the same columns.
	table := lines[2:6]
	pipeCols := func(s string) []int {
		var cols []int
		for i, r := range []rune(s) {
			if r == '|' {
				cols = append(cols, i)
			}
		}
		return cols
	}
	want := pipeCols(table[0])
	if len(want) != 7 { // 6 columns
		t.Fatalf("header should have 7 pipes, got %d: %q", len(want), table[0])
	}
	for _, ln := range table[1:] {
		if utf8.RuneCountInString(ln) != utf8.RuneCountInString(table[0]) {
			t.Errorf("row width differs from header:\n%q\n%q", table[0], ln)
		}
		got := pipeCols(ln)
		if len(got) != len(want) {
			t.Errorf("row has %d pipes, header %d: %q", len(got), len(want), ln)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("pipe %d at col %d, header at %d: %q", i, got[i], want[i], ln)
				break
			}
		}
	}

	// Cell content is preserved.
	for _, cell := range []string{"Filter by a case-insensitive search.", "- `query=Bob Norman`", "acceptable_values"} {
		if !strings.Contains(got, cell) {
			t.Errorf("cell content lost: %q", cell)
		}
	}

	// Idempotent: fixing fixed text changes nothing.
	if again := fixMarkdownTables(got); again != got {
		t.Errorf("not idempotent\n--- first ---\n%s\n--- second ---\n%s", got, again)
	}
}

// A small exact golden: blank lines inserted, short row padded, columns
// aligned, separator rebuilt at the aligned width.
func TestFixMarkdownTablesGolden(t *testing.T) {
	in := "x\n| a | bb |\n| - | - |\n| 1 |"
	want := "x\n" +
		"\n" +
		"| a   | bb  |\n" +
		"| --- | --- |\n" +
		"| 1   |     |"
	if got := fixMarkdownTables(in); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if again := fixMarkdownTables(want); again != want {
		t.Errorf("not idempotent:\n%s", again)
	}
}

// GFM alignment colons in the separator survive realignment.
func TestFixMarkdownTablesAlignmentColons(t *testing.T) {
	in := "| aaa | bbb | ccc |\n| :-- | --: | :-: |\n| 1 | 2 | 3 |"
	want := "| aaa | bbb | ccc |\n| :-- | --: | :-: |\n| 1   | 2   | 3   |"
	if got := fixMarkdownTables(in); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFixMarkdownTablesPassthrough(t *testing.T) {
	cases := []string{
		"no table here",
		"pipes | but | no table",
		"",
		"| lone pipe row with no separator |",
		"ends with newline\n",
	}
	for _, c := range cases {
		if got := fixMarkdownTables(c); got != c {
			t.Errorf("fixMarkdownTables(%q) = %q, want unchanged", c, got)
		}
	}
}

// FixMarkdownTables reaches descriptions in every position: types, fields,
// field args, input fields, enum values, directives, and directive args.
func TestFixMarkdownTablesWalks(t *testing.T) {
	broken := "intro\n| a | b |\n| - | - |\n| 1 |"
	fixed := "intro\n\n| a   | b   |\n| --- | --- |\n| 1   |     |"
	str := TypeRef{Kind: "SCALAR", Name: ptr("String")}
	s := &Schema{
		QueryType: &RootType{"Query"},
		Directives: []Directive{{
			Name: "d", Description: ptr(broken), Locations: []string{"FIELD"},
			Args: []InputValue{{Name: "a", Description: ptr(broken), Type: str}},
		}},
		Types: []Type{
			{Kind: "OBJECT", Name: "Query", Description: ptr(broken), Fields: []Field{{
				Name: "f", Description: ptr(broken), Type: str,
				Args: []InputValue{{Name: "arg", Description: ptr(broken), Type: str}},
			}}},
			{Kind: "INPUT_OBJECT", Name: "In", InputFields: []InputValue{
				{Name: "x", Description: ptr(broken), Type: str},
			}},
			{Kind: "ENUM", Name: "E", EnumValues: []EnumValue{
				{Name: "V", Description: ptr(broken)},
			}},
		},
	}
	s.FixMarkdownTables()

	descs := []*string{
		s.Directives[0].Description,
		s.Directives[0].Args[0].Description,
		s.Types[0].Description,
		s.Types[0].Fields[0].Description,
		s.Types[0].Fields[0].Args[0].Description,
		s.Types[1].InputFields[0].Description,
		s.Types[2].EnumValues[0].Description,
	}
	for i, d := range descs {
		if *d != fixed {
			t.Errorf("description %d not fixed:\ngot:\n%s\nwant:\n%s", i, *d, fixed)
		}
	}
}
