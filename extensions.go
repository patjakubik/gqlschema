package gqlschema

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Extensions maps schema coordinates (`Type`, `Type.field`, `Type.field(arg:)`,
// `Enum.VALUE`, `@directive`, `@directive(arg:)`) to the non-spec introspection
// fields a server exposes for that element, e.g. Shopify's requiredAccess or
// gidTypes. Values are the raw JSON returned by the server; null, false, empty
// string, and empty array values are omitted to keep the result sparse.
type Extensions map[string]map[string]json.RawMessage

// specMetaFields lists the fields the GraphQL spec defines on each
// introspection meta-type; anything else a server exposes is an extension.
var specMetaFields = map[string]map[string]bool{
	"__Type": set("kind", "name", "description", "specifiedByURL", "isOneOf",
		"fields", "inputFields", "interfaces", "enumValues", "possibleTypes", "ofType"),
	"__Field":      set("name", "description", "args", "type", "isDeprecated", "deprecationReason"),
	"__InputValue": set("name", "description", "type", "defaultValue", "isDeprecated", "deprecationReason"),
	"__EnumValue":  set("name", "description", "isDeprecated", "deprecationReason"),
	"__Directive": set("name", "description", "locations", "args", "isRepeatable",
		"onOperation", "onFragment", "onField"), // last three: pre-2018 spec leftovers
}

func set(keys ...string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// discoveryQuery asks each meta-type what fields it exposes, so the extension
// query can be built from what this server actually supports.
const discoveryQuery = `{
  t: __type(name: "__Type") { fields { name type { ...W } } }
  f: __type(name: "__Field") { fields { name type { ...W } } }
  iv: __type(name: "__InputValue") { fields { name type { ...W } } }
  ev: __type(name: "__EnumValue") { fields { name type { ...W } } }
  d: __type(name: "__Directive") { fields { name type { ...W } } }
}
fragment W on __Type { kind ofType { kind ofType { kind ofType { kind } } } }`

// FetchExtensions discovers the non-spec introspection fields the endpoint
// supports on its meta-types and fetches their values for every schema element,
// keyed by schema coordinate. Only extension fields with scalar, enum, or
// list-of-scalar types are fetched — object-typed extensions would need
// selections that cannot be derived generically. Servers without extensions
// return an empty, non-nil map.
func FetchExtensions(ctx context.Context, endpoint string, opts *Options) (Extensions, error) {
	ext, err := discoverExtensions(ctx, endpoint, opts)
	if err != nil {
		return nil, fmt.Errorf("discovering meta-type extensions: %w", err)
	}
	if len(ext["__Type"])+len(ext["__Field"])+len(ext["__InputValue"])+
		len(ext["__EnumValue"])+len(ext["__Directive"]) == 0 {
		return Extensions{}, nil
	}

	var resp struct {
		Data struct {
			Schema struct {
				Types      []map[string]json.RawMessage `json:"types"`
				Directives []map[string]json.RawMessage `json:"directives"`
			} `json:"__schema"`
		} `json:"data"`
		Errors []gqlError `json:"errors"`
	}
	for {
		raw, err := post(ctx, endpoint, opts, buildExtensionQuery(ext))
		if err != nil {
			return nil, fmt.Errorf("fetching extensions: %w", err)
		}
		resp.Errors = nil
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("decoding extensions response: %w", err)
		}
		if len(resp.Errors) == 0 {
			break
		}
		// Some extension fields are themselves access-restricted (Shopify
		// denies componentName, for example). Drop the field the error names
		// and retry; the loop is bounded by the number of discovered fields.
		if !dropNamedField(ext, resp.Errors[0].Message) {
			return nil, fmt.Errorf("extensions query returned errors: %s", resp.Errors[0].Message)
		}
		if len(ext["__Type"])+len(ext["__Field"])+len(ext["__InputValue"])+
			len(ext["__EnumValue"])+len(ext["__Directive"]) == 0 {
			return Extensions{}, nil
		}
	}

	out := Extensions{}
	for _, t := range resp.Data.Schema.Types {
		name := rawString(t, "name")
		if strings.HasPrefix(name, "__") || builtinScalars[name] {
			continue
		}
		out.collect(name, t, ext["__Type"])
		for _, f := range rawList(t, "fields") {
			fc := name + "." + rawString(f, "name")
			out.collect(fc, f, ext["__Field"])
			for _, a := range rawList(f, "args") {
				out.collect(fc+"("+rawString(a, "name")+":)", a, ext["__InputValue"])
			}
		}
		for _, in := range rawList(t, "inputFields") {
			out.collect(name+"."+rawString(in, "name"), in, ext["__InputValue"])
		}
		for _, ev := range rawList(t, "enumValues") {
			out.collect(name+"."+rawString(ev, "name"), ev, ext["__EnumValue"])
		}
	}
	for _, d := range resp.Data.Schema.Directives {
		name := rawString(d, "name")
		if builtinDirectives[name] {
			continue
		}
		out.collect("@"+name, d, ext["__Directive"])
		for _, a := range rawList(d, "args") {
			out.collect("@"+name+"("+rawString(a, "name")+":)", a, ext["__InputValue"])
		}
	}
	return out, nil
}

// dropNamedField removes the first discovered extension field whose name
// appears in a GraphQL error message, reporting whether one was removed.
func dropNamedField(ext map[string][]string, errMsg string) bool {
	for meta, fields := range ext {
		for i, f := range fields {
			if strings.Contains(errMsg, f) {
				ext[meta] = append(fields[:i:i], fields[i+1:]...)
				return true
			}
		}
	}
	return false
}

// discoverExtensions returns, per meta-type, the sorted non-spec fields with
// scalar-ish types.
func discoverExtensions(ctx context.Context, endpoint string, opts *Options) (map[string][]string, error) {
	raw, err := post(ctx, endpoint, opts, discoveryQuery)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data map[string]*struct {
			Fields []struct {
				Name string          `json:"name"`
				Type json.RawMessage `json:"type"`
			} `json:"fields"`
		} `json:"data"`
		Errors []gqlError `json:"errors"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, err
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("%s", resp.Errors[0].Message)
	}

	aliases := map[string]string{"t": "__Type", "f": "__Field", "iv": "__InputValue", "ev": "__EnumValue", "d": "__Directive"}
	ext := map[string][]string{}
	for alias, meta := range aliases {
		node := resp.Data[alias]
		if node == nil {
			continue
		}
		for _, f := range node.Fields {
			if !specMetaFields[meta][f.Name] && isScalarish(f.Type) {
				ext[meta] = append(ext[meta], f.Name)
			}
		}
	}
	return ext, nil
}

// isScalarish reports whether a type reference unwraps (through NON_NULL and
// LIST) to a SCALAR or ENUM, i.e. can be queried without sub-selections.
func isScalarish(raw json.RawMessage) bool {
	var t struct {
		Kind   string          `json:"kind"`
		OfType json.RawMessage `json:"ofType"`
	}
	for {
		if json.Unmarshal(raw, &t) != nil {
			return false
		}
		switch t.Kind {
		case "SCALAR", "ENUM":
			return true
		case "NON_NULL", "LIST":
			if t.OfType == nil {
				return false
			}
			raw = t.OfType
		default:
			return false
		}
	}
}

// buildExtensionQuery assembles an introspection query selecting names (for
// coordinates) plus exactly the discovered extension fields.
func buildExtensionQuery(ext map[string][]string) string {
	sel := func(meta string) string {
		if len(ext[meta]) == 0 {
			return ""
		}
		return " " + strings.Join(ext[meta], " ")
	}
	var b strings.Builder
	b.WriteString("{ __schema { types { name")
	b.WriteString(sel("__Type"))
	b.WriteString(" fields(includeDeprecated: true) { name")
	b.WriteString(sel("__Field"))
	b.WriteString(" args(includeDeprecated: true) { name")
	b.WriteString(sel("__InputValue"))
	b.WriteString(" } } inputFields(includeDeprecated: true) { name")
	b.WriteString(sel("__InputValue"))
	b.WriteString(" } enumValues(includeDeprecated: true) { name")
	b.WriteString(sel("__EnumValue"))
	b.WriteString(" } } directives { name")
	b.WriteString(sel("__Directive"))
	b.WriteString(" args(includeDeprecated: true) { name")
	b.WriteString(sel("__InputValue"))
	b.WriteString(" } } } }")
	return b.String()
}

// collect copies the requested extension fields of one element into dst under
// coord, skipping empty values (null, false, "", []) to keep the map sparse.
func (dst Extensions) collect(coord string, node map[string]json.RawMessage, fields []string) {
	for _, f := range fields {
		v, ok := node[f]
		if !ok || emptyJSON(v) {
			continue
		}
		if dst[coord] == nil {
			dst[coord] = map[string]json.RawMessage{}
		}
		dst[coord][f] = v
	}
}

func emptyJSON(v json.RawMessage) bool {
	switch strings.TrimSpace(string(v)) {
	case "null", "false", `""`, "[]":
		return true
	}
	return false
}

func rawString(m map[string]json.RawMessage, key string) string {
	var s string
	json.Unmarshal(m[key], &s)
	return s
}

func rawList(m map[string]json.RawMessage, key string) []map[string]json.RawMessage {
	var l []map[string]json.RawMessage
	json.Unmarshal(m[key], &l)
	return l
}
