package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Single-line descriptions ending in `"` or `\` cannot use the single-line
// block form (`"""x""""` does not parse) and must fall back to multi-line.
func TestDescriptionTrailingQuote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"trailing quote", `Use "foo"`, "\"\"\"\nUse \"foo\"\n\"\"\"\n"},
		{"trailing backslash", `ends with \`, "\"\"\"\nends with \\\n\"\"\"\n"},
		{"plain stays single-line", "plain", `"""plain"""` + "\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &printer{withDescriptions: true}
			p.desc(ptr(c.in), "")
			if got := p.b.String(); got != c.want {
				t.Errorf("desc(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// `{}` with no members is invalid SDL; empty enums and input objects print
// bare, matching the existing object/interface behaviour.
func TestEmptyEnumAndInput(t *testing.T) {
	s := &schema{QueryType: &typeName{"Query"}, Types: []fullType{
		{Kind: "ENUM", Name: "EmptyEnum"},
		{Kind: "INPUT_OBJECT", Name: "EmptyInput"},
	}}
	sdl := printSchema(s, true)
	for _, want := range []string{"enum EmptyEnum\n", "input EmptyInput\n"} {
		if !strings.Contains(sdl, want) {
			t.Errorf("missing %q in:\n%s", want, sdl)
		}
	}
	if strings.Contains(sdl, "{") {
		t.Errorf("empty type printed with braces:\n%s", sdl)
	}
}

// A oneOf input object keeps its @oneOf directive, and isOneOf decodes from
// the introspection JSON.
func TestOneOfInput(t *testing.T) {
	var ft fullType
	if err := json.Unmarshal([]byte(`{"kind":"INPUT_OBJECT","name":"MoneyInput","isOneOf":true,
		"inputFields":[{"name":"amount","type":{"kind":"SCALAR","name":"Int"},"defaultValue":null}]}`), &ft); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !ft.IsOneOf {
		t.Fatal("isOneOf not decoded")
	}
	s := &schema{QueryType: &typeName{"Query"}, Types: []fullType{ft}}
	if sdl := printSchema(s, true); !strings.Contains(sdl, "input MoneyInput @oneOf {") {
		t.Errorf("@oneOf missing:\n%s", sdl)
	}
}

// When any argument has a description, the argument list goes one per line so
// the descriptions survive; undescribed argument lists stay inline.
func TestArgDescriptions(t *testing.T) {
	id := typeRef{Kind: "SCALAR", Name: ptr("ID")}
	s := &schema{QueryType: &typeName{"QueryRoot"}, Types: []fullType{
		{Kind: "OBJECT", Name: "QueryRoot", Fields: []field{
			{Name: "product", Args: []inputValue{
				{Name: "id", Description: ptr("The product id."), Type: id},
				{Name: "handle", Type: id},
			}, Type: id},
			{Name: "shop", Args: []inputValue{{Name: "id", Type: id}}, Type: id},
		}},
	}}

	want := `  product(
    """The product id."""
    id: ID
    handle: ID
  ): ID`
	sdl := printSchema(s, true)
	if !strings.Contains(sdl, want) {
		t.Errorf("multi-line args missing, want:\n%s\ngot:\n%s", want, sdl)
	}
	if !strings.Contains(sdl, "  shop(id: ID): ID") {
		t.Errorf("undescribed args should stay inline:\n%s", sdl)
	}

	// With descriptions disabled everything stays inline.
	noDesc := printSchema(s, false)
	if !strings.Contains(noDesc, "  product(id: ID, handle: ID): ID") {
		t.Errorf("-no-descriptions args should be inline:\n%s", noDesc)
	}
}

// Directive arguments get the same multi-line treatment.
func TestDirectiveArgDescriptions(t *testing.T) {
	s := &schema{QueryType: &typeName{"Query"}, Directives: []directive{
		{Name: "tag", Locations: []string{"FIELD"}, Args: []inputValue{
			{Name: "name", Description: ptr("Tag name."), Type: typeRef{Kind: "SCALAR", Name: ptr("String")}},
		}},
	}}
	want := `directive @tag(
  """Tag name."""
  name: String
) on FIELD`
	if sdl := printSchema(s, true); !strings.Contains(sdl, want) {
		t.Errorf("want:\n%s\ngot:\n%s", want, sdl)
	}
}

// A wrapper truncated by the introspection query's TypeRef depth limit renders
// a reserved marker instead of panicking on the nil ofType.
func TestRenderTypeTruncated(t *testing.T) {
	for _, kind := range []string{"NON_NULL", "LIST"} {
		got := renderType(&typeRef{Kind: kind})
		if !strings.Contains(got, "__TRUNCATED__") {
			t.Errorf("renderType(%s, nil ofType) = %q, want __TRUNCATED__ marker", kind, got)
		}
	}
}

// -out appends .graphql only when the path has no extension.
func TestOutputPath(t *testing.T) {
	cases := []struct{ in, want string }{
		{"schema", "schema.graphql"},
		{"out/admin/unstable", "out/admin/unstable.graphql"},
		{"schema.gql", "schema.gql"},
		{"schema.graphql", "schema.graphql"},
	}
	for _, c := range cases {
		if got := outputPath(c.in); got != c.want {
			t.Errorf("outputPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// sortSchema orders every named collection alphabetically, like graphql-js's
// lexicographicSortSchema. Pinned as a golden over the shared sample fixture.
func TestSortSchema(t *testing.T) {
	var s schema
	if err := decodeSample(&s); err != nil {
		t.Fatalf("decode sample: %v", err)
	}
	sortSchema(&s)

	want := `schema {
  query: QueryRoot
  mutation: Mutation
}

directive @myDirective(level: Int = 1) repeatable on FIELD | OBJECT

scalar DateTime

interface Node {
  id: ID!
}

type Product implements Node {
  id: ID!
  status: ProductStatus
  tags: [String!]!
}

input ProductInput {
  count: Int = 0
  title: String!
}

enum ProductStatus {
  ACTIVE
  DRAFT @deprecated
}

type QueryRoot {
  legacyField: String @deprecated(reason: "Use product instead.")
  product(id: ID!): Product
}

union SearchResult = Product | QueryRoot
`
	if got := printSchema(&s, false); got != want {
		t.Errorf("sorted output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// Sorting also orders arguments, union members, and interface lists.
func TestSortSchemaInner(t *testing.T) {
	str := typeRef{Kind: "SCALAR", Name: ptr("String")}
	s := schema{QueryType: &typeName{"Query"}, Types: []fullType{
		{Kind: "OBJECT", Name: "Query",
			Interfaces: []typeRef{{Kind: "INTERFACE", Name: ptr("B")}, {Kind: "INTERFACE", Name: ptr("A")}},
			Fields: []field{{Name: "f", Args: []inputValue{
				{Name: "zeta", Type: str}, {Name: "alpha", Type: str},
			}, Type: str}}},
		{Kind: "UNION", Name: "U", PossibleTypes: []typeRef{
			{Kind: "OBJECT", Name: ptr("Z")}, {Kind: "OBJECT", Name: ptr("A")},
		}},
	}}
	sortSchema(&s)
	sdl := printSchema(&s, false)
	for _, want := range []string{
		"type Query implements A & B {",
		"f(alpha: String, zeta: String): String",
		"union U = A | Z",
	} {
		if !strings.Contains(sdl, want) {
			t.Errorf("missing %q in:\n%s", want, sdl)
		}
	}
}
