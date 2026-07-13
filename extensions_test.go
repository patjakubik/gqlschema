package gqlschema

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// extServer fakes a Shopify-like endpoint: the discovery query reports
// requiredAccess on __Type/__Field, gidTypes on __InputValue, and an
// object-typed annotations field (which must be skipped); the follow-up
// extension query gets element values back.
func extServer(t *testing.T) *httptest.Server {
	scalar := `{"kind":"SCALAR"}`
	listOfScalar := `{"kind":"LIST","ofType":{"kind":"NON_NULL","ofType":{"kind":"SCALAR"}}}`
	object := `{"kind":"NON_NULL","ofType":{"kind":"LIST","ofType":{"kind":"OBJECT"}}}`
	discovery := `{"data":{
	  "t":{"fields":[{"name":"kind","type":` + scalar + `},{"name":"requiredAccess","type":` + scalar + `}]},
	  "f":{"fields":[{"name":"name","type":` + scalar + `},{"name":"requiredAccess","type":` + scalar + `},{"name":"annotations","type":` + object + `}]},
	  "iv":{"fields":[{"name":"name","type":` + scalar + `},{"name":"gidTypes","type":` + listOfScalar + `}]},
	  "ev":{"fields":[{"name":"name","type":` + scalar + `}]},
	  "d":{"fields":[{"name":"name","type":` + scalar + `}]}
	}}`
	values := `{"data":{"__schema":{
	  "types":[
	    {"name":"Customer","requiredAccess":"read_customers scope",
	     "fields":[{"name":"orders","requiredAccess":"read_orders scope",
	                "args":[{"name":"id","gidTypes":["Order"]},{"name":"first","gidTypes":null}]}],
	     "inputFields":null,"enumValues":null},
	    {"name":"Plain","requiredAccess":null,"fields":[{"name":"x","requiredAccess":null,"args":[]}],"inputFields":null,"enumValues":null},
	    {"name":"__Type","requiredAccess":"internal","fields":null,"inputFields":null,"enumValues":null},
	    {"name":"String","requiredAccess":"internal","fields":null,"inputFields":null,"enumValues":null}
	  ],
	  "directives":[{"name":"custom","args":[{"name":"a","gidTypes":["App"]}]},
	                {"name":"deprecated","args":[{"name":"reason","gidTypes":["X"]}]}]
	}}}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if o, c := strings.Count(req.Query, "{"), strings.Count(req.Query, "}"); o != c {
			t.Errorf("unbalanced braces in query (%d open, %d close): %s", o, c, req.Query)
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(req.Query, "__InputValue") && strings.Contains(req.Query, "fragment W") {
			w.Write([]byte(discovery))
			return
		}
		// The extension query must not select the object-typed field.
		if strings.Contains(req.Query, "annotations") {
			t.Errorf("object-typed extension selected without sub-selections: %s", req.Query)
		}
		w.Write([]byte(values))
	}))
}

func TestFetchExtensions(t *testing.T) {
	srv := extServer(t)
	defer srv.Close()

	ext, err := FetchExtensions(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("FetchExtensions: %v", err)
	}

	want := map[string]string{
		"Customer":             `"read_customers scope"`,
		"Customer.orders":      `"read_orders scope"`,
		"Customer.orders(id:)": `["Order"]`,
		"@custom(a:)":          `["App"]`,
	}
	for coord, val := range want {
		got := ext[coord]
		if got == nil {
			t.Errorf("missing coordinate %q in %v", coord, ext)
			continue
		}
		var found bool
		for _, v := range got {
			if strings.ReplaceAll(string(v), " ", "") == strings.ReplaceAll(val, " ", "") {
				found = true
			}
		}
		if !found {
			t.Errorf("%q = %v, want value %s", coord, got, val)
		}
	}

	// Sparseness and filtering: nulls, meta-types, builtin scalars, and
	// builtin directives never appear.
	for _, absent := range []string{"Plain", "Plain.x", "Customer.orders(first:)", "__Type", "String", "@deprecated", "@deprecated(reason:)"} {
		if _, ok := ext[absent]; ok {
			t.Errorf("coordinate %q should be absent, got %v", absent, ext[absent])
		}
	}
}

// A vanilla spec-only server yields an empty map and no second request.
func TestFetchExtensionsVanilla(t *testing.T) {
	requests := 0
	scalar := `{"kind":"SCALAR"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte(`{"data":{
		  "t":{"fields":[{"name":"kind","type":` + scalar + `},{"name":"name","type":` + scalar + `}]},
		  "f":{"fields":[{"name":"name","type":` + scalar + `}]},
		  "iv":{"fields":[{"name":"name","type":` + scalar + `}]},
		  "ev":{"fields":[{"name":"name","type":` + scalar + `}]},
		  "d":{"fields":[{"name":"name","type":` + scalar + `}]}
		}}`))
	}))
	defer srv.Close()

	ext, err := FetchExtensions(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("FetchExtensions: %v", err)
	}
	if len(ext) != 0 {
		t.Errorf("expected empty extensions, got %v", ext)
	}
	if requests != 1 {
		t.Errorf("expected only the discovery request, got %d", requests)
	}
}

func TestIsScalarish(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{`{"kind":"SCALAR"}`, true},
		{`{"kind":"ENUM"}`, true},
		{`{"kind":"NON_NULL","ofType":{"kind":"SCALAR"}}`, true},
		{`{"kind":"LIST","ofType":{"kind":"NON_NULL","ofType":{"kind":"SCALAR"}}}`, true},
		{`{"kind":"OBJECT"}`, false},
		{`{"kind":"NON_NULL","ofType":{"kind":"LIST","ofType":{"kind":"OBJECT"}}}`, false},
		{`{"kind":"NON_NULL"}`, false},
	}
	for _, c := range cases {
		if got := isScalarish(json.RawMessage(c.in)); got != c.want {
			t.Errorf("isScalarish(%s) = %v, want %v", c.in, got, c.want)
		}
	}
}

// An access-denied extension field is dropped and the query retried.
func TestFetchExtensionsDeniedField(t *testing.T) {
	scalar := `{"kind":"SCALAR"}`
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(req.Query, "fragment W") {
			w.Write([]byte(`{"data":{
			  "t":{"fields":[{"name":"componentName","type":` + scalar + `},{"name":"requiredAccess","type":` + scalar + `}]},
			  "f":{"fields":[]},"iv":{"fields":[]},"ev":{"fields":[]},"d":{"fields":[]}
			}}`))
			return
		}
		attempts++
		if strings.Contains(req.Query, "componentName") {
			w.Write([]byte(`{"errors":[{"message":"Access denied for componentName field."}]}`))
			return
		}
		w.Write([]byte(`{"data":{"__schema":{"types":[{"name":"T","requiredAccess":"scope"}],"directives":[]}}}`))
	}))
	defer srv.Close()

	ext, err := FetchExtensions(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("FetchExtensions: %v", err)
	}
	if attempts != 2 {
		t.Errorf("expected retry after denial, got %d attempts", attempts)
	}
	if ext["T"] == nil || string(ext["T"]["requiredAccess"]) != `"scope"` {
		t.Errorf("surviving field not fetched: %v", ext)
	}
}

// Annotate merges extension values into descriptions at every coordinate kind,
// appending to existing descriptions and creating them where absent.
func TestAnnotate(t *testing.T) {
	str := TypeRef{Kind: "SCALAR", Name: ptr("String")}
	s := &Schema{
		QueryType: &RootType{"Query"},
		Types: []Type{{Kind: "OBJECT", Name: "Customer", Description: ptr("A customer."),
			Fields: []Field{{Name: "orders", Type: str,
				Args: []InputValue{{Name: "id", Type: str}}}}}},
	}
	s.Annotate(Extensions{
		"Customer":             {"requiredAccess": json.RawMessage(`"read_customers scope"`), "isProtected": json.RawMessage(`true`)},
		"Customer.orders":      {"requiredAccess": json.RawMessage(`"read_orders scope"`)},
		"Customer.orders(id:)": {"gidTypes": json.RawMessage(`["Order"]`)},
	})

	if got, want := *s.Types[0].Description, "A customer.\n\n- isProtected: true\n- requiredAccess: read_customers scope"; got != want {
		t.Errorf("type description = %q, want %q", got, want)
	}
	if got, want := *s.Types[0].Fields[0].Description, "- requiredAccess: read_orders scope"; got != want {
		t.Errorf("field description = %q, want %q", got, want)
	}
	if got, want := *s.Types[0].Fields[0].Args[0].Description, `- gidTypes: ["Order"]`; got != want {
		t.Errorf("arg description = %q, want %q", got, want)
	}

	// The annotated schema prints as valid SDL with the metadata inline.
	sdl := s.SDL(nil)
	if !strings.Contains(sdl, "- requiredAccess: read_customers scope") {
		t.Errorf("annotation missing from SDL:\n%s", sdl)
	}
}
