package gqlschema

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// decodeSample unmarshals the sample introspection response into a Schema,
// mirroring what Fetch does after the HTTP round-trip.
func decodeSample(s *Schema) error {
	var ir introspectionResponse
	if err := json.Unmarshal([]byte(sample), &ir); err != nil {
		return err
	}
	*s = ir.Data.Schema
	return nil
}

// A trimmed introspection response with Shopify-like traits: a non-default
// "QueryRoot" query root, a deprecated field, wrapping types, an enum with a
// deprecated value, a union, an input object with a default, a custom scalar,
// and a custom directive.
const sample = `{
  "data": { "__schema": {
    "queryType": { "name": "QueryRoot" },
    "mutationType": { "name": "Mutation" },
    "subscriptionType": null,
    "types": [
      { "kind": "OBJECT", "name": "QueryRoot", "description": "The root.",
        "fields": [
          { "name": "product", "description": "A product.",
            "args": [ { "name": "id", "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "ID" } }, "defaultValue": null } ],
            "type": { "kind": "OBJECT", "name": "Product" }, "isDeprecated": false, "deprecationReason": null },
          { "name": "legacyField", "description": null, "args": [],
            "type": { "kind": "SCALAR", "name": "String" }, "isDeprecated": true, "deprecationReason": "Use product instead." }
        ], "inputFields": null, "interfaces": [], "enumValues": null, "possibleTypes": null },
      { "kind": "OBJECT", "name": "Product", "description": null,
        "fields": [
          { "name": "id", "description": null, "args": [], "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "ID" } }, "isDeprecated": false, "deprecationReason": null },
          { "name": "tags", "description": null, "args": [], "type": { "kind": "NON_NULL", "ofType": { "kind": "LIST", "ofType": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "String" } } } }, "isDeprecated": false, "deprecationReason": null },
          { "name": "status", "description": null, "args": [], "type": { "kind": "ENUM", "name": "ProductStatus" }, "isDeprecated": false, "deprecationReason": null }
        ], "inputFields": null, "interfaces": [ { "kind": "INTERFACE", "name": "Node" } ], "enumValues": null, "possibleTypes": null },
      { "kind": "INTERFACE", "name": "Node", "description": "Has an id.",
        "fields": [ { "name": "id", "description": null, "args": [], "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "ID" } }, "isDeprecated": false, "deprecationReason": null } ],
        "inputFields": null, "interfaces": [], "enumValues": null, "possibleTypes": [ { "kind": "OBJECT", "name": "Product" } ] },
      { "kind": "ENUM", "name": "ProductStatus", "description": null, "fields": null, "inputFields": null, "interfaces": null,
        "enumValues": [
          { "name": "ACTIVE", "description": "Live.", "isDeprecated": false, "deprecationReason": null },
          { "name": "DRAFT", "description": null, "isDeprecated": true, "deprecationReason": "No longer supported" }
        ], "possibleTypes": null },
      { "kind": "UNION", "name": "SearchResult", "description": null, "fields": null, "inputFields": null, "interfaces": null, "enumValues": null,
        "possibleTypes": [ { "kind": "OBJECT", "name": "Product" }, { "kind": "OBJECT", "name": "QueryRoot" } ] },
      { "kind": "INPUT_OBJECT", "name": "ProductInput", "description": null, "fields": null, "interfaces": null, "enumValues": null, "possibleTypes": null,
        "inputFields": [ { "name": "title", "description": "The title.", "type": { "kind": "NON_NULL", "ofType": { "kind": "SCALAR", "name": "String" } }, "defaultValue": null },
                         { "name": "count", "description": null, "type": { "kind": "SCALAR", "name": "Int" }, "defaultValue": "0" } ] },
      { "kind": "SCALAR", "name": "DateTime", "description": "An ISO-8601 datetime.", "specifiedByURL": null },
      { "kind": "SCALAR", "name": "String", "description": null },
      { "kind": "OBJECT", "name": "__Schema", "description": null, "fields": [], "interfaces": [], "enumValues": null, "possibleTypes": null }
    ],
    "directives": [
      { "name": "myDirective", "description": "Custom.", "isRepeatable": true, "locations": [ "FIELD", "OBJECT" ],
        "args": [ { "name": "level", "description": null, "type": { "kind": "SCALAR", "name": "Int" }, "defaultValue": "1" } ] },
      { "name": "include", "description": null, "isRepeatable": false, "locations": [ "FIELD" ], "args": [] }
    ]
  } }
}`

func TestFetchAndPrint(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("X-Shopify-Access-Token")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sample))
	}))
	defer srv.Close()

	sch, err := Fetch(context.Background(), srv.URL, &Options{
		Headers: map[string]string{"X-Shopify-Access-Token": "test-token"},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotAuth != "test-token" {
		t.Errorf("header not sent, got %q", gotAuth)
	}

	sdl := sch.SDL(nil)
	noDesc := sch.SDL(&SDLOptions{OmitDescriptions: true})

	wantIn := []string{
		"schema {\n  query: QueryRoot\n  mutation: Mutation\n}",
		"type Product implements Node {",
		"tags: [String!]!",
		"product(id: ID!): Product",
		`legacyField: String @deprecated(reason: "Use product instead.")`,
		"DRAFT @deprecated\n", // default reason collapses to bare @deprecated
		"union SearchResult = Product | QueryRoot",
		"input ProductInput {",
		"count: Int = 0",
		"scalar DateTime",
		"directive @myDirective(level: Int = 1) repeatable on FIELD | OBJECT",
		`"""The root."""`,
	}
	for _, w := range wantIn {
		if !strings.Contains(sdl, w) {
			t.Errorf("SDL missing:\n%s\n---full---\n%s", w, sdl)
		}
	}

	// Filtering: built-in scalar String, introspection type __Schema, and the
	// built-in include directive must not appear as definitions.
	for _, bad := range []string{"scalar String", "type __Schema", "directive @include"} {
		if strings.Contains(sdl, bad) {
			t.Errorf("SDL should not contain %q", bad)
		}
	}

	// The OmitDescriptions output drops descriptions but keeps structure.
	if strings.Contains(noDesc, `"""`) {
		t.Errorf("OmitDescriptions SDL still has descriptions:\n%s", noDesc)
	}
	if !strings.Contains(noDesc, "type Product implements Node {") {
		t.Errorf("OmitDescriptions SDL lost structure:\n%s", noDesc)
	}
}

// wantSchema is the exact expected printer output for the sample fixture with
// descriptions. TestGolden pins it so any unintended formatting change across the
// whole schema is caught at once, complementing the targeted substring checks. If
// you change the printer intentionally, update this literal to match.
const wantSchema = `schema {
  query: QueryRoot
  mutation: Mutation
}

"""Custom."""
directive @myDirective(level: Int = 1) repeatable on FIELD | OBJECT

"""The root."""
type QueryRoot {
  """A product."""
  product(id: ID!): Product
  legacyField: String @deprecated(reason: "Use product instead.")
}

type Product implements Node {
  id: ID!
  tags: [String!]!
  status: ProductStatus
}

"""Has an id."""
interface Node {
  id: ID!
}

enum ProductStatus {
  """Live."""
  ACTIVE
  DRAFT @deprecated
}

union SearchResult = Product | QueryRoot

input ProductInput {
  """The title."""
  title: String!
  count: Int = 0
}

"""An ISO-8601 datetime."""
scalar DateTime
`

// wantSchemaNoDesc is the same fixture printed with OmitDescriptions.
const wantSchemaNoDesc = `schema {
  query: QueryRoot
  mutation: Mutation
}

directive @myDirective(level: Int = 1) repeatable on FIELD | OBJECT

type QueryRoot {
  product(id: ID!): Product
  legacyField: String @deprecated(reason: "Use product instead.")
}

type Product implements Node {
  id: ID!
  tags: [String!]!
  status: ProductStatus
}

interface Node {
  id: ID!
}

enum ProductStatus {
  ACTIVE
  DRAFT @deprecated
}

union SearchResult = Product | QueryRoot

input ProductInput {
  title: String!
  count: Int = 0
}

scalar DateTime
`

func TestGolden(t *testing.T) {
	var s Schema
	if err := decodeSample(&s); err != nil {
		t.Fatalf("decode sample: %v", err)
	}

	cases := []struct {
		name string
		opts *SDLOptions
		want string
	}{
		{"with descriptions", nil, wantSchema},
		{"no descriptions", &SDLOptions{OmitDescriptions: true}, wantSchemaNoDesc},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := s.SDL(c.opts); got != c.want {
				t.Errorf("printer output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, c.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }

// deprecation reasons must be escaped as GraphQL string literals. gqlfetch uses
// a raw fmt %s here and produces invalid SDL when a reason contains a quote or a
// newline (as several Shopify reasons do); we use %q.
func TestDeprecationEscaping(t *testing.T) {
	cases := []struct {
		name   string
		isDep  bool
		reason *string
		want   string
	}{
		{"not deprecated", false, ptr("whatever"), ""},
		{"bare when nil reason", true, nil, " @deprecated"},
		{"bare when empty reason", true, ptr(""), " @deprecated"},
		{"default reason collapses", true, ptr("No longer supported"), " @deprecated"},
		{"simple reason", true, ptr("Use foo instead."), ` @deprecated(reason: "Use foo instead.")`},
		{"reason with quote", true, ptr(`Use "foo" instead`), ` @deprecated(reason: "Use \"foo\" instead")`},
		{"reason with newline", true, ptr("line one\nline two"), ` @deprecated(reason: "line one\nline two")`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := deprecation(c.isDep, c.reason); got != c.want {
				t.Errorf("deprecation(%v, %v) = %q, want %q", c.isDep, c.reason, got, c.want)
			}
		})
	}
}

func TestRenderType(t *testing.T) {
	// [Foo!]! -> NON_NULL(LIST(NON_NULL(Foo)))
	nn := func(of TypeRef) TypeRef { return TypeRef{Kind: "NON_NULL", OfType: &of} }
	list := func(of TypeRef) TypeRef { return TypeRef{Kind: "LIST", OfType: &of} }
	named := func(n string) TypeRef { return TypeRef{Kind: "SCALAR", Name: ptr(n)} }

	cases := []struct {
		name string
		t    TypeRef
		want string
	}{
		{"named", named("Foo"), "Foo"},
		{"non-null", nn(named("Foo")), "Foo!"},
		{"list", list(named("Foo")), "[Foo]"},
		{"non-null list of non-null", nn(list(nn(named("Foo")))), "[Foo!]!"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := renderType(&c.t); got != c.want {
				t.Errorf("renderType = %q, want %q", got, c.want)
			}
		})
	}
}

// schemaBlock is emitted only when a root type name is non-default.
func TestSchemaBlock(t *testing.T) {
	cases := []struct {
		name string
		s    Schema
		want string
	}{
		{
			"default roots -> no block",
			Schema{QueryType: &RootType{"Query"}, MutationType: &RootType{"Mutation"}, SubscriptionType: &RootType{"Subscription"}},
			"",
		},
		{
			"query only, default -> no block",
			Schema{QueryType: &RootType{"Query"}},
			"",
		},
		{
			"non-default query root -> block",
			Schema{QueryType: &RootType{"QueryRoot"}, MutationType: &RootType{"Mutation"}},
			"schema {\n  query: QueryRoot\n  mutation: Mutation\n}",
		},
		{
			"non-default mutation root -> block",
			Schema{QueryType: &RootType{"Query"}, MutationType: &RootType{"MutationRoot"}},
			"schema {\n  query: Query\n  mutation: MutationRoot\n}",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := schemaBlock(&c.s); got != c.want {
				t.Errorf("schemaBlock = %q, want %q", got, c.want)
			}
		})
	}
}

// A multi-line description prints as a block; embedded triple-quotes are escaped.
func TestDescriptionRendering(t *testing.T) {
	p := &printer{withDescriptions: true}
	p.desc(ptr("line one\nline two"), "")
	got := p.b.String()
	want := "\"\"\"\nline one\nline two\n\"\"\"\n"
	if got != want {
		t.Errorf("multiline desc = %q, want %q", got, want)
	}

	p2 := &printer{withDescriptions: true}
	p2.desc(ptr(`has """ inside`), "")
	if !strings.Contains(p2.b.String(), `\"""`) {
		t.Errorf("triple-quote not escaped: %q", p2.b.String())
	}

	// Descriptions disabled -> nothing emitted.
	p3 := &printer{withDescriptions: false}
	p3.desc(ptr("ignored"), "")
	if p3.b.String() != "" {
		t.Errorf("desc emitted while disabled: %q", p3.b.String())
	}
}

// specifiedByURL on a custom scalar becomes @specifiedBy(url:).
func TestSpecifiedByScalar(t *testing.T) {
	s := &Schema{
		QueryType: &RootType{"Query"},
		Types: []Type{
			{Kind: "SCALAR", Name: "DateTime", SpecifiedByURL: ptr("https://example.com/datetime")},
		},
	}
	sdl := s.SDL(nil)
	want := `scalar DateTime @specifiedBy(url: "https://example.com/datetime")`
	if !strings.Contains(sdl, want) {
		t.Errorf("missing %q in:\n%s", want, sdl)
	}
}

// All five spec built-in directives are filtered out; custom ones survive.
func TestBuiltinDirectivesFiltered(t *testing.T) {
	s := &Schema{
		QueryType: &RootType{"Query"},
		Directives: []Directive{
			{Name: "skip", Locations: []string{"FIELD"}},
			{Name: "include", Locations: []string{"FIELD"}},
			{Name: "deprecated", Locations: []string{"FIELD_DEFINITION"}},
			{Name: "specifiedBy", Locations: []string{"SCALAR"}},
			{Name: "oneOf", Locations: []string{"INPUT_OBJECT"}},
			{Name: "custom", Locations: []string{"FIELD"}},
		},
	}
	sdl := s.SDL(nil)
	for _, name := range []string{"@skip", "@include", "@deprecated", "@specifiedBy", "@oneOf"} {
		if strings.Contains(sdl, "directive "+name) {
			t.Errorf("built-in directive %s should be filtered, got:\n%s", name, sdl)
		}
	}
	if !strings.Contains(sdl, "directive @custom on FIELD") {
		t.Errorf("custom directive dropped:\n%s", sdl)
	}
}

// A repeatable directive prints the `repeatable` keyword.
func TestRepeatableDirective(t *testing.T) {
	s := &Schema{
		QueryType:  &RootType{"Query"},
		Directives: []Directive{{Name: "tag", IsRepeatable: true, Locations: []string{"FIELD", "OBJECT"}}},
	}
	if sdl := s.SDL(nil); !strings.Contains(sdl, "directive @tag repeatable on FIELD | OBJECT") {
		t.Errorf("repeatable keyword missing:\n%s", sdl)
	}
}

func TestFetchGraphQLErrorsInBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// 200 status but an errors array, as Shopify does.
		w.Write([]byte(`{"errors":[{"message":"Access denied"}]}`))
	}))
	defer srv.Close()

	_, err := Fetch(context.Background(), srv.URL, nil)
	if err == nil || !strings.Contains(err.Error(), "Access denied") {
		t.Errorf("expected GraphQL error surfaced, got %v", err)
	}
}

func TestFetchNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("nope"))
	}))
	defer srv.Close()

	_, err := Fetch(context.Background(), srv.URL, nil)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 error, got %v", err)
	}
}

func TestFetchMultipleHeaders(t *testing.T) {
	var gotA, gotB string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotA = r.Header.Get("X-A")
		gotB = r.Header.Get("X-B")
		w.Write([]byte(sample))
	}))
	defer srv.Close()

	opts := &Options{Headers: map[string]string{"X-A": "one", "X-B": "two"}}
	if _, err := Fetch(context.Background(), srv.URL, opts); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotA != "one" || gotB != "two" {
		t.Errorf("headers not both sent: X-A=%q X-B=%q", gotA, gotB)
	}
}

// Fetch honours the caller's context.
func TestFetchContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sample))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Fetch(ctx, srv.URL, nil); err == nil {
		t.Error("expected error from canceled context")
	}
}

// Fetch uses the caller's client when one is provided.
func TestFetchCustomClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sample))
	}))
	defer srv.Close()

	used := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		used = true
		return http.DefaultTransport.RoundTrip(r)
	})}
	if _, err := Fetch(context.Background(), srv.URL, &Options{Client: client}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !used {
		t.Error("custom client not used")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
