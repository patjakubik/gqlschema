// Command gqlschema fetches a GraphQL schema from an endpoint via an
// introspection query and writes it out as SDL, in a form genqlient can consume.
//
// It writes a single <out>.graphql. Schema descriptions are included by default
// and omitted with -no-descriptions.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

// introspectionQuery is the standard full introspection query, including
// deprecated fields, directives, isRepeatable, specifiedByURL, and input-value
// deprecation.
const introspectionQuery = `query IntrospectionQuery {
  __schema {
    queryType { name }
    mutationType { name }
    subscriptionType { name }
    types { ...FullType }
    directives {
      name
      description
      isRepeatable
      locations
      args { ...InputValue }
    }
  }
}
fragment FullType on __Type {
  kind
  name
  description
  specifiedByURL
  fields(includeDeprecated: true) {
    name
    description
    args { ...InputValue }
    type { ...TypeRef }
    isDeprecated
    deprecationReason
  }
  inputFields(includeDeprecated: true) { ...InputValue }
  interfaces { ...TypeRef }
  enumValues(includeDeprecated: true) {
    name
    description
    isDeprecated
    deprecationReason
  }
  possibleTypes { ...TypeRef }
}
fragment InputValue on __InputValue {
  name
  description
  type { ...TypeRef }
  defaultValue
  isDeprecated
  deprecationReason
}
fragment TypeRef on __Type {
  kind
  name
  ofType { kind name ofType { kind name ofType { kind name ofType { kind name
  ofType { kind name ofType { kind name ofType { kind name ofType { kind name } } } } } } } }
}`

// --- introspection response types ---

type gqlError struct {
	Message string `json:"message"`
}

type introspectionResponse struct {
	Data struct {
		Schema schema `json:"__schema"`
	} `json:"data"`
	Errors []gqlError `json:"errors"`
}

type schema struct {
	QueryType        *typeName   `json:"queryType"`
	MutationType     *typeName   `json:"mutationType"`
	SubscriptionType *typeName   `json:"subscriptionType"`
	Types            []fullType  `json:"types"`
	Directives       []directive `json:"directives"`
}

type typeName struct {
	Name string `json:"name"`
}

type fullType struct {
	Kind           string       `json:"kind"`
	Name           string       `json:"name"`
	Description    *string      `json:"description"`
	SpecifiedByURL *string      `json:"specifiedByURL"`
	Fields         []field      `json:"fields"`
	InputFields    []inputValue `json:"inputFields"`
	Interfaces     []typeRef    `json:"interfaces"`
	EnumValues     []enumValue  `json:"enumValues"`
	PossibleTypes  []typeRef    `json:"possibleTypes"`
}

type field struct {
	Name              string       `json:"name"`
	Description       *string      `json:"description"`
	Args              []inputValue `json:"args"`
	Type              typeRef      `json:"type"`
	IsDeprecated      bool         `json:"isDeprecated"`
	DeprecationReason *string      `json:"deprecationReason"`
}

type inputValue struct {
	Name              string  `json:"name"`
	Description       *string `json:"description"`
	Type              typeRef `json:"type"`
	DefaultValue      *string `json:"defaultValue"`
	IsDeprecated      bool    `json:"isDeprecated"`
	DeprecationReason *string `json:"deprecationReason"`
}

type enumValue struct {
	Name              string  `json:"name"`
	Description       *string `json:"description"`
	IsDeprecated      bool    `json:"isDeprecated"`
	DeprecationReason *string `json:"deprecationReason"`
}

type typeRef struct {
	Kind   string   `json:"kind"`
	Name   *string  `json:"name"`
	OfType *typeRef `json:"ofType"`
}

type directive struct {
	Name         string       `json:"name"`
	Description  *string      `json:"description"`
	IsRepeatable bool         `json:"isRepeatable"`
	Locations    []string     `json:"locations"`
	Args         []inputValue `json:"args"`
}

// --- repeatable -header flag ---

type headerFlag []string

func (h *headerFlag) String() string { return strings.Join(*h, ", ") }
func (h *headerFlag) Set(v string) error {
	*h = append(*h, v)
	return nil
}

func main() {
	var (
		endpoint = flag.String("endpoint", "", "GraphQL endpoint URL (required)")
		out      = flag.String("out", "schema", "output path prefix; writes <out>.graphql")
		method   = flag.String("method", "POST", "HTTP method for the introspection request")
		noDesc   = flag.Bool("no-descriptions", false, "omit schema descriptions from the output")
		stamp    = flag.Bool("stamp", false, "prepend a header comment with the generator, version, endpoint, and timestamp")
		headers  headerFlag
	)
	flag.Var(&headers, "header", "HTTP header as 'Key: Value' (repeatable)")
	flag.Parse()

	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "error: -endpoint is required")
		flag.Usage()
		os.Exit(2)
	}

	sch, err := fetchSchema(*endpoint, *method, headers)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	banner := ""
	if *stamp {
		banner = headerComment(*endpoint, generatorVersion(), time.Now())
	}

	path := *out + ".graphql"
	if err := writeFile(path, banner+printSchema(sch, !*noDesc)); err != nil {
		fmt.Fprintf(os.Stderr, "error writing schema: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %s\n", path)
}

// headerComment builds the SDL comment block prepended to output when -stamp is
// set. version and the timestamp are parameters so they can be pinned in tests.
func headerComment(endpoint, version string, generatedAt time.Time) string {
	return fmt.Sprintf(
		"# Code generated by gqlschema %s (github.com/patjakubik/gqlschema); DO NOT EDIT.\n"+
			"# Endpoint:  %s\n"+
			"# Generated: %s\n\n",
		version, endpoint, generatedAt.UTC().Format(time.RFC3339))
}

// generatorVersion reports the module version gqlschema was built at. It is set
// automatically by the Go toolchain when the tool is installed via `go install
// …@version` or pinned as a `go tool`; a local `go run .` build reports "dev".
func generatorVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

func writeFile(path, content string) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func fetchSchema(endpoint, method string, headers []string) (*schema, error) {
	body, _ := json.Marshal(map[string]string{"query": introspectionQuery})

	req, err := http.NewRequest(strings.ToUpper(method), endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for _, h := range headers {
		k, v, ok := strings.Cut(h, ":")
		if !ok {
			return nil, fmt.Errorf("bad -header %q, expected 'Key: Value'", h)
		}
		req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("endpoint returned %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var ir introspectionResponse
	if err := json.Unmarshal(raw, &ir); err != nil {
		return nil, fmt.Errorf("decoding introspection response: %w", err)
	}
	// GraphQL servers (Shopify included) return errors in a 200 body, so check
	// this before assuming data is present.
	if len(ir.Errors) > 0 {
		return nil, fmt.Errorf("introspection returned errors: %s", ir.Errors[0].Message)
	}
	if ir.Data.Schema.QueryType == nil {
		return nil, fmt.Errorf("no __schema in response (is introspection enabled?)")
	}
	return &ir.Data.Schema, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- SDL printer ---

var builtinScalars = map[string]bool{
	"Int": true, "Float": true, "String": true, "Boolean": true, "ID": true,
}

var builtinDirectives = map[string]bool{
	"skip": true, "include": true, "deprecated": true,
	"specifiedBy": true, "oneOf": true,
}

func printSchema(s *schema, withDescriptions bool) string {
	p := &printer{withDescriptions: withDescriptions}

	if blk := schemaBlock(s); blk != "" {
		p.line(blk)
		p.blank()
	}

	for _, d := range s.Directives {
		if builtinDirectives[d.Name] {
			continue
		}
		p.printDirective(d)
		p.blank()
	}

	for _, t := range s.Types {
		if strings.HasPrefix(t.Name, "__") || builtinScalars[t.Name] {
			continue
		}
		p.printType(t)
		p.blank()
	}

	return strings.TrimRight(p.b.String(), "\n") + "\n"
}

// schemaBlock emits an explicit `schema { ... }` only when a root type name is
// non-default (e.g. Shopify's query root is "QueryRoot"), matching graphql-js.
func schemaBlock(s *schema) string {
	q := rootName(s.QueryType)
	m := rootName(s.MutationType)
	sub := rootName(s.SubscriptionType)
	if q == "Query" && (m == "" || m == "Mutation") && (sub == "" || sub == "Subscription") {
		return ""
	}
	var lines []string
	if q != "" {
		lines = append(lines, "  query: "+q)
	}
	if m != "" {
		lines = append(lines, "  mutation: "+m)
	}
	if sub != "" {
		lines = append(lines, "  subscription: "+sub)
	}
	return "schema {\n" + strings.Join(lines, "\n") + "\n}"
}

func rootName(t *typeName) string {
	if t == nil {
		return ""
	}
	return t.Name
}

type printer struct {
	b                strings.Builder
	withDescriptions bool
}

func (p *printer) line(s string) { p.b.WriteString(s); p.b.WriteByte('\n') }
func (p *printer) blank()        { p.b.WriteByte('\n') }

func (p *printer) desc(d *string, indent string) {
	if !p.withDescriptions || d == nil || *d == "" {
		return
	}
	clean := strings.ReplaceAll(*d, `"""`, `\"""`)
	if !strings.Contains(clean, "\n") {
		p.line(indent + `"""` + clean + `"""`)
		return
	}
	p.line(indent + `"""`)
	for _, ln := range strings.Split(clean, "\n") {
		p.line(indent + ln)
	}
	p.line(indent + `"""`)
}

func (p *printer) printType(t fullType) {
	switch t.Kind {
	case "SCALAR":
		p.desc(t.Description, "")
		line := "scalar " + t.Name
		if t.SpecifiedByURL != nil && *t.SpecifiedByURL != "" {
			line += fmt.Sprintf(` @specifiedBy(url: %q)`, *t.SpecifiedByURL)
		}
		p.line(line)
	case "OBJECT", "INTERFACE":
		p.desc(t.Description, "")
		keyword := "type"
		if t.Kind == "INTERFACE" {
			keyword = "interface"
		}
		head := keyword + " " + t.Name
		if impl := implements(t.Interfaces); impl != "" {
			head += " implements " + impl
		}
		if len(t.Fields) == 0 {
			p.line(head)
			return
		}
		p.line(head + " {")
		for i, f := range t.Fields {
			if i > 0 && p.withDescriptions && f.Description != nil && *f.Description != "" {
				p.blank()
			}
			p.printField(f)
		}
		p.line("}")
	case "UNION":
		p.desc(t.Description, "")
		names := make([]string, len(t.PossibleTypes))
		for i, pt := range t.PossibleTypes {
			names[i] = deref(pt.Name)
		}
		if len(names) == 0 {
			p.line("union " + t.Name)
			return
		}
		p.line("union " + t.Name + " = " + strings.Join(names, " | "))
	case "ENUM":
		p.desc(t.Description, "")
		p.line("enum " + t.Name + " {")
		for _, ev := range t.EnumValues {
			p.desc(ev.Description, "  ")
			p.line("  " + ev.Name + deprecation(ev.IsDeprecated, ev.DeprecationReason))
		}
		p.line("}")
	case "INPUT_OBJECT":
		p.desc(t.Description, "")
		p.line("input " + t.Name + " {")
		for _, in := range t.InputFields {
			p.desc(in.Description, "  ")
			p.line("  " + inputValueSDL(in))
		}
		p.line("}")
	}
}

func (p *printer) printField(f field) {
	p.desc(f.Description, "  ")
	line := "  " + f.Name
	if len(f.Args) > 0 {
		line += "(" + argList(f.Args) + ")"
	}
	line += ": " + renderType(&f.Type)
	line += deprecation(f.IsDeprecated, f.DeprecationReason)
	p.line(line)
}

func (p *printer) printDirective(d directive) {
	p.desc(d.Description, "")
	line := "directive @" + d.Name
	if len(d.Args) > 0 {
		line += "(" + argList(d.Args) + ")"
	}
	if d.IsRepeatable {
		line += " repeatable"
	}
	line += " on " + strings.Join(d.Locations, " | ")
	p.line(line)
}

func argList(args []inputValue) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = inputValueSDL(a)
	}
	return strings.Join(parts, ", ")
}

func implements(ifaces []typeRef) string {
	if len(ifaces) == 0 {
		return ""
	}
	names := make([]string, len(ifaces))
	for i, r := range ifaces {
		names[i] = renderType(&r)
	}
	return strings.Join(names, " & ")
}

func inputValueSDL(in inputValue) string {
	s := in.Name + ": " + renderType(&in.Type)
	if in.DefaultValue != nil && *in.DefaultValue != "" {
		s += " = " + *in.DefaultValue
	}
	s += deprecation(in.IsDeprecated, in.DeprecationReason)
	return s
}

func deprecation(isDep bool, reason *string) string {
	if !isDep {
		return ""
	}
	if reason != nil && *reason != "" && *reason != "No longer supported" {
		return fmt.Sprintf(` @deprecated(reason: %q)`, *reason)
	}
	return " @deprecated"
}

// renderType walks the NON_NULL / LIST wrappers to produce e.g. [Foo!]!
func renderType(t *typeRef) string {
	switch t.Kind {
	case "NON_NULL":
		return renderType(t.OfType) + "!"
	case "LIST":
		return "[" + renderType(t.OfType) + "]"
	default:
		return deref(t.Name)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
