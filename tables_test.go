package gqlschema

import (
	"strings"
	"testing"
)

// A trimmed real Shopify `query`-argument description: table glued to the
// paragraphs around it, and a truncated row with 2 of 6 cells.
const brokenTable = "A filter made up of terms, connectives, modifiers, and comparators.\n" +
	"| name | type | description | acceptable_values | default_value | example_use |\n" +
	"| ---- | ---- | ---- | ---- | ---- | ---- |\n" +
	"| default | string | Filter by a case-insensitive search. | | | - `query=Bob Norman` |\n" +
	"| status | string |\n" +
	"You can apply one or more filters to a query."

const fixedTable = "A filter made up of terms, connectives, modifiers, and comparators.\n" +
	"\n" +
	"| name | type | description | acceptable_values | default_value | example_use |\n" +
	"| ---- | ---- | ---- | ---- | ---- | ---- |\n" +
	"| default | string | Filter by a case-insensitive search. | | | - `query=Bob Norman` |\n" +
	"| status | string | | | | |\n" +
	"\n" +
	"You can apply one or more filters to a query."

func TestFixMarkdownTables(t *testing.T) {
	got := fixMarkdownTables(brokenTable)
	if got != fixedTable {
		t.Errorf("fixMarkdownTables mismatch\n--- got ---\n%s\n--- want ---\n%s", got, fixedTable)
	}

	// Idempotent: fixing fixed text changes nothing.
	if again := fixMarkdownTables(got); again != got {
		t.Errorf("not idempotent\n--- first ---\n%s\n--- second ---\n%s", got, again)
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

// Tables at the very start or end of a description gain no stray blank lines.
func TestFixMarkdownTablesAtEdges(t *testing.T) {
	in := "| a | b |\n| - | - |\n| 1 | 2 |"
	if got := fixMarkdownTables(in); got != in {
		t.Errorf("edge table changed:\n%s", got)
	}

	blankAround := "text\n\n| a | b |\n| - | - |\n| 1 | 2 |\n\ntext"
	if got := fixMarkdownTables(blankAround); got != blankAround {
		t.Errorf("already-fixed table changed:\n%s", got)
	}
}

// FixMarkdownTables reaches descriptions in every position: types, fields,
// field args, input fields, enum values, directives, and directive args.
func TestFixMarkdownTablesWalks(t *testing.T) {
	broken := "intro\n| a | b |\n| - | - |\n| 1 |"
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
	want := "intro\n\n| a | b |\n| - | - |\n| 1 | |"
	for i, d := range descs {
		if *d != want {
			t.Errorf("description %d not fixed:\n%s", i, *d)
		}
	}
	if strings.Contains(*descs[0], "| 1 |\n") {
		t.Error("short row not padded")
	}
}
