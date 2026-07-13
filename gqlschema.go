// Package gqlschema fetches a GraphQL schema from an endpoint via an
// introspection query and renders it as SDL, in a form genqlient can consume.
//
// Fetch runs the introspection query and decodes the result into a Schema;
// Schema.SDL renders it. The gqlschema command in cmd/gqlschema wraps this
// package as a CLI.
package gqlschema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// introspectionQuery is the standard full introspection query, including
// deprecated fields and arguments, directives, isRepeatable, specifiedByURL,
// isOneOf, the schema description, and input-value deprecation.
const introspectionQuery = `query IntrospectionQuery {
  __schema {
    description
    queryType { name }
    mutationType { name }
    subscriptionType { name }
    types { ...FullType }
    directives {
      name
      description
      isRepeatable
      locations
      args(includeDeprecated: true) { ...InputValue }
    }
  }
}
fragment FullType on __Type {
  kind
  name
  description
  specifiedByURL
  isOneOf
  fields(includeDeprecated: true) {
    name
    description
    args(includeDeprecated: true) { ...InputValue }
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

type gqlError struct {
	Message string `json:"message"`
}

type introspectionResponse struct {
	Data struct {
		Schema Schema `json:"__schema"`
	} `json:"data"`
	Errors []gqlError `json:"errors"`
}

// Schema is a GraphQL schema decoded from an introspection response.
type Schema struct {
	Description      *string     `json:"description"`
	QueryType        *RootType   `json:"queryType"`
	MutationType     *RootType   `json:"mutationType"`
	SubscriptionType *RootType   `json:"subscriptionType"`
	Types            []Type      `json:"types"`
	Directives       []Directive `json:"directives"`
}

// RootType names one of the schema's root operation types.
type RootType struct {
	Name string `json:"name"`
}

// Type is a named type definition: object, interface, union, enum, input
// object, or scalar, discriminated by Kind.
type Type struct {
	Kind           string       `json:"kind"`
	Name           string       `json:"name"`
	Description    *string      `json:"description"`
	SpecifiedByURL *string      `json:"specifiedByURL"`
	IsOneOf        bool         `json:"isOneOf"`
	Fields         []Field      `json:"fields"`
	InputFields    []InputValue `json:"inputFields"`
	Interfaces     []TypeRef    `json:"interfaces"`
	EnumValues     []EnumValue  `json:"enumValues"`
	PossibleTypes  []TypeRef    `json:"possibleTypes"`
}

// Field is an output field of an object or interface type.
type Field struct {
	Name              string       `json:"name"`
	Description       *string      `json:"description"`
	Args              []InputValue `json:"args"`
	Type              TypeRef      `json:"type"`
	IsDeprecated      bool         `json:"isDeprecated"`
	DeprecationReason *string      `json:"deprecationReason"`
}

// InputValue is a field argument, directive argument, or input object field.
type InputValue struct {
	Name              string  `json:"name"`
	Description       *string `json:"description"`
	Type              TypeRef `json:"type"`
	DefaultValue      *string `json:"defaultValue"`
	IsDeprecated      bool    `json:"isDeprecated"`
	DeprecationReason *string `json:"deprecationReason"`
}

// EnumValue is one value of an enum type.
type EnumValue struct {
	Name              string  `json:"name"`
	Description       *string `json:"description"`
	IsDeprecated      bool    `json:"isDeprecated"`
	DeprecationReason *string `json:"deprecationReason"`
}

// TypeRef is a reference to a type, possibly wrapped in NON_NULL or LIST.
type TypeRef struct {
	Kind   string   `json:"kind"`
	Name   *string  `json:"name"`
	OfType *TypeRef `json:"ofType"`
}

// Directive is a directive definition.
type Directive struct {
	Name         string       `json:"name"`
	Description  *string      `json:"description"`
	IsRepeatable bool         `json:"isRepeatable"`
	Locations    []string     `json:"locations"`
	Args         []InputValue `json:"args"`
}

// Options configures Fetch. A nil *Options (or the zero value) uses POST and
// a client with a 60-second timeout.
type Options struct {
	// Client makes the introspection request. When nil, a client with a
	// 60-second timeout is used.
	Client *http.Client
	// Method is the HTTP method for the request, POST when empty.
	Method string
	// Headers are set on the request, e.g. auth tokens. Content-Type and
	// Accept default to application/json and can be overridden here.
	Headers map[string]string
}

var defaultClient = &http.Client{Timeout: 60 * time.Second}

// Fetch runs the introspection query against endpoint and decodes the schema.
// GraphQL errors returned in a 200 body are surfaced as errors.
func Fetch(ctx context.Context, endpoint string, opts *Options) (*Schema, error) {
	raw, err := post(ctx, endpoint, opts, introspectionQuery)
	if err != nil {
		return nil, err
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

// post sends a GraphQL query per opts and returns the raw response body of a
// 200 response.
func post(ctx context.Context, endpoint string, opts *Options, query string) ([]byte, error) {
	if opts == nil {
		opts = &Options{}
	}
	method := opts.Method
	if method == "" {
		method = http.MethodPost
	}

	body, _ := json.Marshal(map[string]string{"query": query})
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	client := opts.Client
	if client == nil {
		client = defaultClient
	}
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
	return raw, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Sort orders every named collection alphabetically, mirroring graphql-js's
// lexicographicSortSchema, so SDL output stays stable when a server returns
// definitions in a different order between runs.
func (s *Schema) Sort() {
	slices.SortFunc(s.Directives, func(a, b Directive) int { return strings.Compare(a.Name, b.Name) })
	for i := range s.Directives {
		sortArgs(s.Directives[i].Args)
	}
	slices.SortFunc(s.Types, func(a, b Type) int { return strings.Compare(a.Name, b.Name) })
	for i := range s.Types {
		t := &s.Types[i]
		slices.SortFunc(t.Fields, func(a, b Field) int { return strings.Compare(a.Name, b.Name) })
		for j := range t.Fields {
			sortArgs(t.Fields[j].Args)
		}
		sortArgs(t.InputFields)
		slices.SortFunc(t.EnumValues, func(a, b EnumValue) int { return strings.Compare(a.Name, b.Name) })
		sortRefs(t.Interfaces)
		sortRefs(t.PossibleTypes)
	}
}

func sortArgs(args []InputValue) {
	slices.SortFunc(args, func(a, b InputValue) int { return strings.Compare(a.Name, b.Name) })
}

func sortRefs(refs []TypeRef) {
	slices.SortFunc(refs, func(a, b TypeRef) int { return strings.Compare(deref(a.Name), deref(b.Name)) })
}
