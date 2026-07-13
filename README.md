# gqlschema
 
Fetch a GraphQL schema from a live endpoint via an introspection query and write it out as SDL — in a form [genqlient](https://github.com/Khan/genqlient) can consume directly.
 
genqlient deliberately does not introspect remote endpoints; it expects an SDL schema file on disk ([Khan/genqlient#4](https://github.com/Khan/genqlient/issues/4)). `gqlschema` fills that gap so the whole codegen chain can run from `go generate`, version-pinned, with no Node or npm dependency in the pipeline.
 
## Features
 
- Standard full introspection query (deprecated fields, directives, `isRepeatable`, `specifiedByURL`, `@oneOf` input objects, input-value deprecation).
- Emits SDL, not raw introspection JSON.
- Writes a single file named by `-out`; descriptions — including field-argument descriptions — are included by default and omitted with `-no-descriptions`.
- Handles servers whose root types are non-default (for example Shopify's `QueryRoot`).
- Surfaces GraphQL errors returned in a `200` body instead of failing opaquely.
- Repeatable `-header` flag for auth and any other request headers.
- Optional `-sort` (mirroring graphql-js's `lexicographicSortSchema`) so output stays stable when a server returns definitions in a different order between runs — useful when committing the schema to track changes over time.
- Optional `-fix-tables` to normalize GitHub-flavored-Markdown tables inside descriptions: GFM tables cannot interrupt a paragraph, and Shopify's generated `query`-argument docs ship without the required blank lines and with truncated rows. It also aligns every column to its widest cell so tables read as tables in the raw file. The fix is whitespace and empty-cell padding only — cell content is never changed.
- Optional `-extensions` capture of the server's non-spec introspection fields — Shopify exposes access scopes (`requiredAccess`), protected-data classes, accepted GID types, and input-size caps this way. `-extensions=json` writes a `<out>.extensions.json` sidecar keyed by schema coordinate, `-extensions=inline` merges the metadata into the SDL descriptions so it renders in IDE hovers and GraphiQL, `both` does both. Extension fields are auto-discovered from the server's meta-types, so the flag is vendor-neutral and a no-op on spec-only servers.
- Optional `-stamp` header comment recording the generator, version, endpoint, and timestamp.
- Standard library only — no third-party dependencies.

## Install
 
As a pinned project tool (recommended, requires Go 1.24+):
 
```sh
go get -tool github.com/patjakubik/gqlschema/cmd/gqlschema@latest
```
 
This adds a `tool` directive to your `go.mod`, so everyone on the project and CI runs the same version. Invoke it with `go tool gqlschema`.
 
Or as a global binary:
 
```sh
go install github.com/patjakubik/gqlschema/cmd/gqlschema@latest
```
 
## Usage
 
```
gqlschema -endpoint <url> [flags]
 
  -endpoint string   GraphQL endpoint URL (required)
  -out string        output path; .graphql is appended when the path has no extension (default "schema")
  -no-descriptions   omit schema descriptions from the output
  -sort              sort types, fields, arguments, and enum values alphabetically for stable diffs
  -fix-tables        normalize markdown tables in descriptions so they render and read well (blank lines around tables, short rows padded, columns aligned)
  -extensions value  fetch the server's non-spec introspection fields: 'json' (sidecar), 'inline' (into SDL descriptions), or 'both'
  -stamp             prepend a header comment with the generator, version, endpoint, and timestamp
  -method string     HTTP method for the introspection request (default "POST")
  -header value      HTTP header as 'Key: Value' (repeatable)
```
 
### Example: Shopify Admin API
 
```sh
gqlschema \
  -endpoint https://your-shop.myshopify.com/admin/api/unstable/graphql \
  -header "X-Shopify-Access-Token: $SHOPIFY_ACCESS_TOKEN" \
  -fix-tables \
  -extensions=inline
```
 
Writes `schema.graphql` with two Shopify-specific problems handled:

- `-fix-tables` repairs the markdown tables Shopify embeds in `query`-argument
  docs — upstream they ship without the blank lines GFM requires and with
  truncated rows, so they render as a wall of pipes; fixed and column-aligned
  they read as tables both rendered and in the raw file.
- `-extensions=inline` merges Shopify's non-spec introspection metadata into
  the descriptions, so access scopes, protected customer data classes, and
  accepted GID types show up in IDE hovers and GraphiQL:

```graphql
"""
Represents information about a customer of the shop...

- isProtected: true
- protectedSubject: customer
- requiredAccess: `read_customers` access scope.
"""
type Customer implements CommentEventSubject & HasEvents & ... {

  """Returns a `Abandonment` resource by ID."""
  abandonment(
    """
    The ID of the `Abandonment` to return.

    - gidTypes: ["Abandonment"]
    """
    id: ID!
  ): Abandonment
```

Both flags are opt-in and vendor-neutral: table fixing only touches actual
markdown tables, and extensions are auto-discovered, so the same command is a
plain introspection against servers that have neither. Pass `-no-descriptions`
to omit descriptions (annotations included), or `-extensions=json` to keep the
metadata in a `schema.extensions.json` sidecar instead.

## Use as a library

The root package exposes the same fetch and print pipeline for Go programs
that introspect endpoints directly — several endpoints in one process, a
custom `http.Client`, or the decoded schema itself:

```go
import "github.com/patjakubik/gqlschema"

sch, err := gqlschema.Fetch(ctx, "https://your-shop.myshopify.com/admin/api/unstable/graphql", &gqlschema.Options{
	Headers: map[string]string{"X-Shopify-Access-Token": token},
})
if err != nil {
	return err
}
ext, err := gqlschema.FetchExtensions(ctx, endpoint, opts) // optional: non-spec metadata
if err != nil {
	return err
}
sch.Sort()               // optional: stable ordering, like graphql-js lexicographicSortSchema
sch.Annotate(ext)        // optional: merge extension metadata into descriptions
sch.FixMarkdownTables()  // optional: make markdown tables in descriptions render
sdl := sch.SDL(nil) // or &gqlschema.SDLOptions{OmitDescriptions: true}
```

`Fetch` takes a `context.Context` and honours `Options.Client` (a client with
a 60-second timeout is used when nil). The returned `Schema` is the decoded
introspection result with exported fields, so it can also be inspected or
transformed before printing.

`FetchExtensions(ctx, endpoint, opts)` returns the server's non-spec
introspection metadata as a map of [schema coordinates](https://github.com/graphql/graphql-spec/pull/794)
(`Customer.orders(id:)`) to raw JSON values. It discovers what the meta-types
expose, queries only scalar-typed extension fields, drops access-denied ones
automatically, and returns an empty map on spec-only servers. SDL cannot represent this
metadata structurally; pair `FetchExtensions` with `Schema.Annotate(ext)` to
merge it into descriptions, or persist the map as a sidecar.

## Wiring into genqlient
 
Point genqlient's `schema:` at the generated file and chain both steps in `go generate`. genqlient ignores descriptions, so the default output works directly; pass `-no-descriptions` if you'd rather feed it a smaller description-free file:
 
```go
//go:generate go tool gqlschema -endpoint https://your-shop.myshopify.com/admin/api/unstable/graphql -header "X-Shopify-Access-Token: $(SHOPIFY_ACCESS_TOKEN)"
//go:generate go tool genqlient
```
 
```yaml
# genqlient.yaml
schema: schema.graphql
operations:
  - "queries/**/*.graphql"
generated: generated.go
```
 
Then `go generate ./...` refreshes the schema and regenerates the client in sequence.

Keep your operation documents out of the schema's directory. genqlient's `operations` globs and the generated schema both use the `.graphql` extension, so a repo-wide `**/*.graphql` would sweep the schema file in and fail to parse it as an operation - scope the glob to where your query files live.
 
## Descriptions
 
By default `gqlschema` keeps all schema descriptions — on types, fields, enum values, input fields, and field arguments (argument lists with described arguments print one argument per line, the way graphql-js does) — which is useful for browsing or diffing what the API documents. Pass `-no-descriptions` to omit them: genqlient ignores descriptions, so this produces a smaller file to feed codegen.
 
Descriptions are toggled structurally during printing, so the description-stripped output is not produced by removing text after the fact — there is no risk of a stray `"""` inside a default value or description corrupting the result.
 
## Notes and caveats
 
- **API version compatibility.** The introspection query requests `specifiedByURL`, `isRepeatable`, `isOneOf`, and input-value `isDeprecated`. Recent GraphQL servers (including current Shopify API versions) support these, but a much older endpoint may reject them and fail introspection. If that happens, remove those fields from the query.
- **Directive applications are not recoverable.** Introspection exposes directive *definitions* but not directives *applied* to fields, so the generated SDL will not be byte-identical to a hand-maintained schema that uses custom directives. This is a limitation of GraphQL introspection itself, not of this tool, and it does not affect genqlient, which only needs types and fields.
- **Introspection must be enabled.** Many APIs disable introspection in production. Point the tool at an environment where it is available.

## License
 
MIT
