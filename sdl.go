package gqlschema

import (
	"fmt"
	"strings"
)

// SDLOptions configures SDL rendering. A nil *SDLOptions (or the zero value)
// includes all schema descriptions.
type SDLOptions struct {
	// OmitDescriptions drops descriptions from the output. Descriptions are
	// toggled structurally during printing, not stripped from rendered text.
	OmitDescriptions bool
}

// SDL renders the schema as GraphQL SDL, in the style of graphql-js's
// printSchema: built-in scalars, directives, and introspection types are
// omitted, and an explicit schema block appears only when a root type name is
// non-default.
func (s *Schema) SDL(opts *SDLOptions) string {
	withDescriptions := opts == nil || !opts.OmitDescriptions
	return printSchema(s, withDescriptions)
}

var builtinScalars = map[string]bool{
	"Int": true, "Float": true, "String": true, "Boolean": true, "ID": true,
}

var builtinDirectives = map[string]bool{
	"skip": true, "include": true, "deprecated": true,
	"specifiedBy": true, "oneOf": true,
}

func printSchema(s *Schema, withDescriptions bool) string {
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
func schemaBlock(s *Schema) string {
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

func rootName(t *RootType) string {
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
	// A value ending in `"` or `\` would glue into the closing quotes of the
	// single-line form (`"""x""""` does not parse), so those use the multi-line
	// form too, matching graphql-js.
	if !strings.Contains(clean, "\n") &&
		!strings.HasSuffix(clean, `"`) && !strings.HasSuffix(clean, `\`) {
		p.line(indent + `"""` + clean + `"""`)
		return
	}
	p.line(indent + `"""`)
	for _, ln := range strings.Split(clean, "\n") {
		p.line(indent + ln)
	}
	p.line(indent + `"""`)
}

func (p *printer) printType(t Type) {
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
		// `{}` with no members is invalid SDL, so an empty type prints bare.
		if len(t.EnumValues) == 0 {
			p.line("enum " + t.Name)
			return
		}
		p.line("enum " + t.Name + " {")
		for _, ev := range t.EnumValues {
			p.desc(ev.Description, "  ")
			p.line("  " + ev.Name + deprecation(ev.IsDeprecated, ev.DeprecationReason))
		}
		p.line("}")
	case "INPUT_OBJECT":
		p.desc(t.Description, "")
		head := "input " + t.Name
		if t.IsOneOf {
			head += " @oneOf"
		}
		if len(t.InputFields) == 0 {
			p.line(head)
			return
		}
		p.line(head + " {")
		for _, in := range t.InputFields {
			p.desc(in.Description, "  ")
			p.line("  " + inputValueSDL(in))
		}
		p.line("}")
	}
}

func (p *printer) printField(f Field) {
	p.desc(f.Description, "  ")
	line := p.argsSDL("  "+f.Name, f.Args, "  ")
	line += ": " + renderType(&f.Type)
	line += deprecation(f.IsDeprecated, f.DeprecationReason)
	p.line(line)
}

func (p *printer) printDirective(d Directive) {
	p.desc(d.Description, "")
	line := p.argsSDL("directive @"+d.Name, d.Args, "")
	if d.IsRepeatable {
		line += " repeatable"
	}
	line += " on " + strings.Join(d.Locations, " | ")
	p.line(line)
}

// argsSDL appends an argument list to head. Arguments normally render inline,
// but when descriptions are on and any argument has one, they go one per line
// so the descriptions have somewhere to live; the emitted lines end with the
// closing paren returned as the new head for the caller to continue.
func (p *printer) argsSDL(head string, args []InputValue, indent string) string {
	if len(args) == 0 {
		return head
	}
	described := false
	for _, a := range args {
		if p.withDescriptions && a.Description != nil && *a.Description != "" {
			described = true
			break
		}
	}
	if !described {
		return head + "(" + argList(args) + ")"
	}
	p.line(head + "(")
	for i, a := range args {
		if i > 0 && a.Description != nil && *a.Description != "" {
			p.blank()
		}
		p.desc(a.Description, indent+"  ")
		p.line(indent + "  " + inputValueSDL(a))
	}
	return indent + ")"
}

func argList(args []InputValue) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = inputValueSDL(a)
	}
	return strings.Join(parts, ", ")
}

func implements(ifaces []TypeRef) string {
	if len(ifaces) == 0 {
		return ""
	}
	names := make([]string, len(ifaces))
	for i, r := range ifaces {
		names[i] = renderType(&r)
	}
	return strings.Join(names, " & ")
}

func inputValueSDL(in InputValue) string {
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
func renderType(t *TypeRef) string {
	switch t.Kind {
	case "NON_NULL", "LIST":
		if t.OfType == nil {
			// The introspection TypeRef fragment only recurses 9 levels, so a
			// type wrapped deeper than that arrives truncated. No real schema
			// does this; emit a reserved marker that downstream parsers reject
			// loudly rather than panicking here.
			return "__TRUNCATED__"
		}
		if t.Kind == "NON_NULL" {
			return renderType(t.OfType) + "!"
		}
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
