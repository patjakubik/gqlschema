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
  -stamp             prepend a header comment with the generator, version, endpoint, and timestamp
  -method string     HTTP method for the introspection request (default "POST")
  -header value      HTTP header as 'Key: Value' (repeatable)
```
 
### Example: Shopify Admin API
 
```sh
gqlschema \
  -endpoint https://your-shop.myshopify.com/admin/api/unstable/graphql \
  -header "X-Shopify-Access-Token: $SHOPIFY_ACCESS_TOKEN"
```
 
Writes `schema.graphql` (pass `-no-descriptions` to omit descriptions from it).

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
sch.Sort() // optional: stable ordering, like graphql-js lexicographicSortSchema
sdl := sch.SDL(nil) // or &gqlschema.SDLOptions{OmitDescriptions: true}
```

`Fetch` takes a `context.Context` and honours `Options.Client` (a client with
a 60-second timeout is used when nil). The returned `Schema` is the decoded
introspection result with exported fields, so it can also be inspected or
transformed before printing.

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
