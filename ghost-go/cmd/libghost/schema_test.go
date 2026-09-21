package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/invopop/jsonschema"
)

// The JSON documents the C API takes and returns, described as a JSON Schema
// generated from the Go types that encode and decode them. The committed
// schema, libghost.schema.json, is what a host application generates its own
// types from; this test fails when it no longer matches the Go types, so the
// two cannot drift apart. Regenerate it with
//
//	go test ./cmd/libghost -run TestSchemaIsCurrent -update

var update = flag.Bool("update", false, "rewrite libghost.schema.json from the Go types")

const schemaFile = "libghost.schema.json"

// schemaPath is the schema beside this file, as seen from the module root
// (buildSchema moves there to read the Go comments).
const schemaPath = "cmd/libghost/" + schemaFile

// documents are the schema's roots: what each C entry point takes or returns.
// An error from any call that returns JSON is an Error document instead.
var documents = []struct {
	name string
	v    any
}{
	{"ghost_enroll.request", enrollRequestJSON{}},
	{"ghost_enroll.response", enrollResponse{}},
	{"ghost_start.config", startConfig{}},
	{"ghost_set_policy.policy", policyRequest{}},
	{"ghost_status_json", statusView{}},
	{"ghost_metrics_json", metricsView{}},
	{"ghost_next_event_json", eventJSON{}},
	{"error", errorResponse{}},
}

// typeNames gives the unexported document types the names a host sees.
var typeNames = map[reflect.Type]string{
	reflect.TypeFor[enrollRequestJSON](): "EnrollRequest",
	reflect.TypeFor[enrollResponse]():    "EnrollResponse",
	reflect.TypeFor[enrollCreds]():       "EnrollCreds",
	reflect.TypeFor[startConfig]():       "StartConfig",
	reflect.TypeFor[credsConfig]():       "Creds",
	reflect.TypeFor[turnConfig]():        "TurnServer",
	reflect.TypeFor[exitConfig]():        "ExitConfig",
	reflect.TypeFor[metricsConfig]():     "MetricsConfig",
	reflect.TypeFor[policyRequest]():     "Policy",
	reflect.TypeFor[localPolicy]():       "LocalPolicy",
	reflect.TypeFor[statusView]():        "Status",
	reflect.TypeFor[peerView]():          "Peer",
	reflect.TypeFor[exitView]():          "ExitStatus",
	reflect.TypeFor[metricsView]():       "Metrics",
	reflect.TypeFor[eventJSON]():         "Event",
	reflect.TypeFor[errorResponse]():     "Error",
}

// schemaName is the definition name of a type.
func schemaName(rt reflect.Type) string {
	if n, ok := typeNames[rt]; ok {
		return n
	}
	return rt.Name()
}

// markNullable says so where encoding/json writes null: a nil pointer, slice
// or map in a field without omitempty (Status.exit for a node with no exit,
// say). The reflector declares those fields as the type alone.
func markNullable(defs jsonschema.Definitions, roots []reflect.Type) {
	seen := map[reflect.Type]bool{}
	var walk func(rt reflect.Type)
	var fields func(def *jsonschema.Schema, rt reflect.Type)
	walk = func(rt reflect.Type) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Map {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] || rt == reflect.TypeFor[time.Time]() {
			return
		}
		seen[rt] = true
		if def, ok := defs[schemaName(rt)]; ok {
			fields(def, rt)
		}
	}
	fields = func(def *jsonschema.Schema, rt reflect.Type) {
		for i := range rt.NumField() {
			f := rt.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" || (!f.IsExported() && !f.Anonymous) {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			if f.Anonymous && name == "" {
				fields(def, f.Type) // flattened, as encoding/json does
				continue
			}
			walk(f.Type)
			if name == "" {
				name = f.Name
			}
			k := f.Type.Kind()
			if strings.Contains(opts, "omitempty") || (k != reflect.Pointer && k != reflect.Slice && k != reflect.Map) {
				continue
			}
			prop, ok := def.Properties.Get(name)
			if !ok {
				continue
			}
			desc := prop.Description
			prop.Description = ""
			def.Properties.Set(name, &jsonschema.Schema{
				AnyOf:       []*jsonschema.Schema{prop, {Type: "null"}},
				Description: desc,
			})
		}
	}
	for _, rt := range roots {
		walk(rt)
	}
}

// buildSchema reflects every document into one schema: the types under
// $defs, and x-libghost-documents naming which type each entry point speaks.
func buildSchema(t *testing.T) []byte {
	t.Helper()
	r := &jsonschema.Reflector{
		// Inputs are decoded with DisallowUnknownFields; outputs only ever
		// carry their own fields. Either way, nothing else is allowed.
		AllowAdditionalProperties: false,
		Namer:                     schemaName,
	}
	// Field and type descriptions come from the Go doc comments, across the
	// module (metrics and proto types appear in the outputs). They are read
	// here rather than with the reflector's AddGoComments, which names a
	// package by its directory with the OS's separator (so on Windows a
	// package below the module root, signal/proto say, lost its comments and
	// the schema differed from Linux's) and skips unexported types, which
	// every document type here is.
	t.Chdir("../..")
	comments, err := moduleComments(module, reflect.TypeFor[startConfig]().PkgPath())
	if err != nil {
		t.Fatalf("read the Go comments: %v", err)
	}
	r.LookupComment = func(rt reflect.Type, field string) string {
		key := rt.PkgPath() + "." + rt.Name()
		if field != "" {
			key += "." + field
		}
		return comments[key]
	}

	defs := jsonschema.Definitions{}
	roots := map[string]string{}
	var types []reflect.Type
	for _, d := range documents {
		s := r.Reflect(d.v)
		for name, def := range s.Definitions {
			defs[name] = def
		}
		roots[d.name] = s.Ref
		types = append(types, reflect.TypeOf(d.v))
	}
	markNullable(defs, types)

	doc := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://github.com/Imposter/ghost/ghost-go/cmd/libghost/" + schemaFile,
		"title":                "libghost " + Version,
		"description":          "The JSON documents of the libghost C API (docs/ffi.md). Generated from the Go types by TestSchemaIsCurrent: do not edit.",
		"x-libghost-abi":       Version,
		"x-libghost-documents": roots,
		"$defs":                defs,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// module is the ghost-go module path.
const module = "github.com/Imposter/ghost/ghost-go"

// moduleComments reads the doc comments of every type and field in the module
// below the working directory, keyed as reflect names them:
// importpath.Type and importpath.Type.Field. Import paths are built with
// forward slashes whatever the OS. This package's own types are also filed
// under self, the package path reflect reports for them in a test binary.
func moduleComments(module, self string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(".", func(dir string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if name := d.Name(); dir != "." && (strings.HasPrefix(name, ".") || name == "testdata") {
			return filepath.SkipDir
		}
		pkg := module
		if dir != "." {
			pkg += "/" + filepath.ToSlash(dir)
		}
		comments := map[string]string{}
		if err := addDirComments(comments, dir); err != nil {
			return err
		}
		for key, text := range comments {
			out[pkg+"."+key] = text
			if pkg == module+"/cmd/libghost" {
				out[self+"."+key] = text
			}
		}
		return nil
	})
	return out, err
}

// addDirComments files the doc comments of one directory's types and their
// fields by name (Type, Type.Field).
func addDirComments(into map[string]string, dir string) error {
	fset := token.NewFileSet()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			if gd, ok := decl.(*ast.GenDecl); ok {
				addTypeComments(into, gd)
			}
		}
	}
	return nil
}

// addTypeComments files one declaration's type and field comments.
func addTypeComments(into map[string]string, gd *ast.GenDecl) {
	for _, spec := range gd.Specs {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		text := ts.Doc.Text()
		if text == "" {
			text = gd.Doc.Text()
		}
		if text != "" {
			into[ts.Name.Name] = strings.TrimSpace(text)
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			continue
		}
		for _, field := range st.Fields.List {
			text := field.Doc.Text()
			if text == "" {
				text = field.Comment.Text()
			}
			if text == "" {
				continue
			}
			for _, name := range field.Names {
				into[ts.Name.Name+"."+name.Name] = strings.TrimSpace(text)
			}
		}
	}
}

func TestSchemaIsCurrent(t *testing.T) {
	got := buildSchema(t)
	if *update {
		if err := os.WriteFile(schemaPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("%v (generate it with -update)", err)
	}
	// Git may have checked the file out with CRLF line endings.
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatalf("%s is stale: the Go types changed. Regenerate it with\n"+
			"  go test ./cmd/libghost -run TestSchemaIsCurrent -update", schemaFile)
	}
}

// Every document names a definition, and the inputs refuse unknown fields
// the way DisallowUnknownFields does.
func TestSchemaShape(t *testing.T) {
	var doc struct {
		Documents map[string]string                     `json:"x-libghost-documents"`
		Defs      map[string]map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(buildSchema(t), &doc); err != nil {
		t.Fatal(err)
	}
	for _, d := range documents {
		ref := doc.Documents[d.name]
		name := strings.TrimPrefix(ref, "#/$defs/")
		def, ok := doc.Defs[name]
		if !ok {
			t.Errorf("%s: %q is not a definition", d.name, ref)
			continue
		}
		if string(def["additionalProperties"]) != "false" {
			t.Errorf("%s (%s) allows unknown fields", d.name, name)
		}
	}
	// Spot checks that the descriptions came through from the comments: this
	// package's, and one from a package below the module root (whose
	// comments once went missing on Windows only, so the schema depended on
	// the OS it was generated on).
	if !strings.Contains(string(doc.Defs["StartConfig"]["description"]), "ghost_start") {
		t.Errorf("StartConfig has no description from its Go comment: %s", doc.Defs["StartConfig"]["description"])
	}
	var policy struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(mustJSON(t, doc.Defs["ExitPolicy"]), &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Properties["allow"].Description == "" {
		t.Error("ExitPolicy (signal/proto) has no field descriptions")
	}
}

// Where libghost can write null, the schema says so.
func TestSchemaNullable(t *testing.T) {
	var doc struct {
		Defs map[string]struct {
			Properties map[string]map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(buildSchema(t), &doc); err != nil {
		t.Fatal(err)
	}
	nullable := func(def, prop string) bool {
		return strings.Contains(string(doc.Defs[def].Properties[prop]["anyOf"]), `"null"`)
	}
	for _, c := range []struct {
		def, prop string
		want      bool
	}{
		{"Status", "exit", true},            // *exitView, no omitempty
		{"ExitStatus", "local_allow", true}, // []string, no omitempty
		{"Status", "peer_id", false},        // a string is never null
		{"Policy", "allow", false},          // omitempty: absent, not null
		{"Totals", "connections", false},    // flattened from Counts
		{"Metrics", "connections", true},    // []metrics.Connection
	} {
		if got := nullable(c.def, c.prop); got != c.want {
			t.Errorf("%s.%s nullable = %v, want %v", c.def, c.prop, got, c.want)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
