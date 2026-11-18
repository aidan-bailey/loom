// Package gen turns internal/web's route table, stream events and union types
// (web.Contract) into the app's typed contract: ui/src/api/gen.ts, the
// reference docs/specs/web-api.md, and api.fields, which pins every field,
// route, event and union value that has shipped. It reads Go types by
// reflection and writes what encoding/json writes; `go generate ./internal/web`
// runs it, and TestContract_GeneratedFilesAreFresh fails when a file is stale.
package gen

import (
	"encoding"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aidan-bailey/loom/internal/web"
)

// Output is what Generate produces: the three committed files.
type Output struct {
	// GenTS is internal/web/ui/src/api/gen.ts.
	GenTS []byte
	// WebAPI is docs/specs/web-api.md.
	WebAPI []byte
	// Fields is internal/web/api.fields: the committed lines, then the new
	// ones, sorted within the new.
	Fields []string
}

// Files are the paths of the three outputs, given the directory of
// internal/web (go generate runs in it, so the command passes ".").
func Files(webDir string) (genTS, webAPI, fields string) {
	return filepath.Join(webDir, "ui", "src", "api", "gen.ts"),
		filepath.Join(webDir, "..", "..", "docs", "specs", "web-api.md"),
		filepath.Join(webDir, "api.fields")
}

// ReadFields reads the lines of an api.fields file, leaving out blank ones.
func ReadFields(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var lines []string
	for _, l := range strings.Split(string(data), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// FormatFields is the content of an api.fields file: one line each, nothing
// for no lines.
func FormatFields(lines []string) []byte {
	if len(lines) == 0 {
		return nil
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// Dropped returns the committed lines fresh lacks: a field, route, event or
// union value that was removed, or whose type changed, since it shipped.
func Dropped(committed, fresh []string) []string {
	now := make(map[string]bool, len(fresh))
	for _, l := range fresh {
		now[l] = true
	}
	var gone []string
	for _, l := range committed {
		if !now[l] {
			gone = append(gone, l)
		}
	}
	return gone
}

var (
	timeType      = reflect.TypeFor[time.Time]()
	rawType       = reflect.TypeFor[json.RawMessage]()
	jsonMarshaler = reflect.TypeFor[json.Marshaler]()
	textMarshaler = reflect.TypeFor[encoding.TextMarshaler]()
	identifier    = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
)

// reserved are the names gen.ts declares itself: no type or union takes one.
var reserved = []string{"Method", "Outcome", "Transport", "StreamEvents", "Client"}

// field is one JSON field of a struct: its JSON name, its Go type, and
// whether encoding/json may leave it out.
type field struct {
	name     string
	typ      reflect.Type
	optional bool
}

// model is the contract after its types are walked: every named struct type
// the routes and events reach, with its JSON fields and TS name, and the
// unions.
type model struct {
	c      web.ContractSpec
	fields map[reflect.Type][]field
	names  map[reflect.Type]string
	sorted []reflect.Type
	unions []web.ContractUnion
}

// build walks every route's query, body and response type and every event's
// payload, and names what it reaches. A TS name that two types, or a type and
// a union, would share is an error.
func build(c web.ContractSpec) (*model, error) {
	m := &model{c: c, fields: map[reflect.Type][]field{}, names: map[reflect.Type]string{}}
	for t, u := range c.Unions {
		if t.Kind() != reflect.String {
			return nil, fmt.Errorf("union %s: %s is not a string type", u.Name, t)
		}
		m.unions = append(m.unions, u)
	}
	sort.Slice(m.unions, func(i, j int) bool { return m.unions[i].Name < m.unions[j].Name })
	taken := map[string]string{}
	for _, name := range reserved {
		taken[name] = "gen.ts itself"
	}
	for _, u := range m.unions {
		if u.Name == "" || len(u.Values) == 0 {
			return nil, fmt.Errorf("a union needs a name and values, not %+v", u)
		}
		if what, dup := taken[u.Name]; dup {
			return nil, fmt.Errorf("union %s: the TS name %s is already %s", u.Name, u.Name, what)
		}
		taken[u.Name] = "another union"
	}

	for _, r := range c.Routes {
		if r.Query != nil {
			if err := m.walkQuery(r.Query, r.Method+" "+r.Path+" query"); err != nil {
				return nil, err
			}
		}
	}
	for _, r := range c.Routes {
		for _, t := range []reflect.Type{r.Body, r.Resp} {
			if t == nil {
				continue
			}
			if err := m.walk(t, r.Method+" "+r.Path); err != nil {
				return nil, err
			}
		}
	}
	for _, e := range c.Events {
		if e.Data == nil {
			return nil, fmt.Errorf("event %s has no data type", e.Name)
		}
		if err := m.walk(e.Data, "event "+e.Name); err != nil {
			return nil, err
		}
	}

	types := make([]reflect.Type, 0, len(m.fields))
	for t := range m.fields {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool { return types[i].String() < types[j].String() })
	for _, t := range types {
		name, err := tsName(t)
		if err != nil {
			return nil, err
		}
		if what, dup := taken[name]; dup {
			return nil, fmt.Errorf("%s maps to the TS name %s, which %s already holds", t, name, what)
		}
		taken[name] = t.String()
		m.names[t] = name
	}
	sort.Slice(types, func(i, j int) bool { return m.names[types[i]] < m.names[types[j]] })
	m.sorted = types
	return m, nil
}

// tsName is a struct type's TS name: its Go name without a trailing JSON, its
// first letter upper-cased (sessionJSON is Session, sendBody is SendBody).
func tsName(t reflect.Type) (string, error) {
	n := strings.TrimSuffix(t.Name(), "JSON")
	if n == "" || strings.ContainsAny(n, "[]") {
		return "", fmt.Errorf("%s has no name the generator can use", t)
	}
	r, size := utf8.DecodeRuneInString(n)
	return string(unicode.ToUpper(r)) + n[size:], nil
}

// walkQuery registers a route's query type: a named struct of string fields,
// each named by its query tag, as readQuery reads it.
func (m *model) walkQuery(t reflect.Type, where string) error {
	if t.Kind() != reflect.Struct || t.Name() == "" {
		return fmt.Errorf("%s: %s is not a named struct", where, t)
	}
	if _, done := m.fields[t]; done {
		return nil
	}
	var fields []field
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := f.Tag.Get("query")
		if name == "" || f.Type.Kind() != reflect.String || !f.IsExported() {
			return fmt.Errorf("%s: %s.%s must be an exported string field with a query tag", where, t.Name(), f.Name)
		}
		fields = append(fields, field{name: name, typ: f.Type})
	}
	m.fields[t] = fields
	return nil
}

// walk registers every named struct type t reaches, with its JSON fields. It
// only collects: tsType reports a type it cannot write.
func (m *model) walk(t reflect.Type, where string) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == timeType || t == rawType {
		return nil
	}
	switch t.Kind() {
	case reflect.Struct:
		if t.Name() == "" {
			return nil
		}
		if _, done := m.fields[t]; done {
			return nil
		}
		fields, err := jsonFields(t)
		if err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		m.fields[t] = fields
		for _, f := range fields {
			if err := m.walk(f.typ, where+" "+t.Name()+"."+f.name); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array, reflect.Map:
		return m.walk(t.Elem(), where)
	}
	return nil
}

// jsonFields are t's fields as encoding/json writes them: names from the
// tags, unexported and json:"-" fields left out, an embedded struct's fields
// flattened, a field with omitempty or omitzero optional.
func jsonFields(t reflect.Type) ([]field, error) {
	var out []field
	seen := map[string]bool{}
	var collect func(t reflect.Type) error
	collect = func(t reflect.Type) error {
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			if f.Anonymous && name == "" {
				inner := f.Type
				if inner.Kind() == reflect.Pointer {
					inner = inner.Elem()
				}
				if inner.Kind() == reflect.Struct {
					if err := collect(inner); err != nil {
						return err
					}
					continue
				}
			}
			if !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			optional := false
			for _, o := range strings.Split(opts, ",") {
				switch o {
				case "omitempty", "omitzero":
					optional = true
				case "string":
					return fmt.Errorf("%s.%s: the string tag option changes the wire type, which the generator does not follow", t.Name(), f.Name)
				}
			}
			if seen[name] {
				return fmt.Errorf("%s: the JSON name %q appears twice", t.Name(), name)
			}
			seen[name] = true
			out = append(out, field{name: name, typ: f.Type, optional: optional})
		}
		return nil
	}
	if err := collect(t); err != nil {
		return nil, err
	}
	return out, nil
}

// marshals reports whether t writes its own JSON, which reflection cannot
// follow.
func marshals(t reflect.Type) bool {
	p := reflect.PointerTo(t)
	return t.Implements(jsonMarshaler) || p.Implements(jsonMarshaler) || t.Implements(textMarshaler) || p.Implements(textMarshaler)
}

// canBeNull reports whether encoding/json may write null for a value of type
// t whose TS type is ts.
func canBeNull(t reflect.Type, ts string) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		return ts != "unknown"
	}
	return false
}

// tsType is the TS type of what encoding/json writes for t. where names the
// route, event or field it is for, in an error.
func (m *model) tsType(t reflect.Type, where string) (string, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == timeType:
		return "string", nil
	case t == rawType, t.Kind() == reflect.Interface && t.NumMethod() == 0:
		return "unknown", nil
	case marshals(t):
		return "", fmt.Errorf("%s: %s marshals itself, which the generator cannot type", where, t)
	}
	switch t.Kind() {
	case reflect.String:
		if u, ok := m.c.Unions[t]; ok {
			return u.Name, nil
		}
		return "string", nil
	case reflect.Bool:
		return "boolean", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return "number", nil
	case reflect.Slice, reflect.Array:
		// An array is written as a JSON array: T[], never null; its length
		// is not in the type, since a JSON import widens it to T[].
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return "string", nil
		}
		elem, null, err := m.elementType(t.Elem(), where)
		if err != nil {
			return "", err
		}
		if null {
			return "(" + elem + " | null)[]", nil
		}
		return elem + "[]", nil
	case reflect.Map:
		switch t.Key().Kind() {
		case reflect.String,
			reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		default:
			return "", fmt.Errorf("%s: %s has a map key encoding/json writes in no plain form", where, t)
		}
		elem, null, err := m.elementType(t.Elem(), where)
		if err != nil {
			return "", err
		}
		if null {
			elem += " | null"
		}
		return "Record<string, " + elem + ">", nil
	case reflect.Struct:
		if name, ok := m.names[t]; ok {
			return name, nil
		}
		if t.Name() == "" && t.NumField() == 0 {
			return "Record<string, never>", nil
		}
		if t.Name() == "" {
			return "", fmt.Errorf("%s: an unnamed struct with fields (%s): give it a name", where, t)
		}
		return "", fmt.Errorf("%s: %s was not reached", where, t)
	}
	return "", fmt.Errorf("%s: %s (a %s) has no JSON type", where, t, t.Kind())
}

// elementType is the TS type of a slice element or map value, and whether
// encoding/json may write null for it.
func (m *model) elementType(t reflect.Type, where string) (string, bool, error) {
	ts, err := m.tsType(t, where)
	if err != nil {
		return "", false, err
	}
	return ts, canBeNull(t, ts), nil
}

// fieldType is the TS type of a field: its type, with | null for one that
// encoding/json writes as null unless the field is optional.
func (m *model) fieldType(f field, where string) (string, error) {
	ts, err := m.tsType(f.typ, where)
	if err != nil {
		return "", err
	}
	if !f.optional && canBeNull(f.typ, ts) {
		ts += " | null"
	}
	return ts, nil
}

// key is a property name in TS source: bare when it is an identifier.
func key(name string) string {
	if identifier.MatchString(name) {
		return name
	}
	return strconv.Quote(name)
}

// types is the TS source of every union and every interface, each followed by
// a blank line: unions by name, then interfaces by name.
func (m *model) types() (string, error) {
	var b strings.Builder
	for _, u := range m.unions {
		quoted := make([]string, len(u.Values))
		for i, v := range u.Values {
			quoted[i] = strconv.Quote(v)
		}
		fmt.Fprintf(&b, "export type %s = %s;\n\n", u.Name, strings.Join(quoted, " | "))
	}
	for _, t := range m.sorted {
		fmt.Fprintf(&b, "export interface %s {\n", m.names[t])
		for _, f := range m.fields[t] {
			ts, err := m.fieldType(f, t.Name()+"."+f.name)
			if err != nil {
				return "", err
			}
			opt := ""
			if f.optional {
				opt = "?"
			}
			fmt.Fprintf(&b, "  %s%s: %s;\n", key(f.name), opt, ts)
		}
		b.WriteString("}\n\n")
	}
	return b.String(), nil
}

const tsHeader = `// Code generated by go generate ./internal/web; DO NOT EDIT.
// Source: the route table (routes.go), the stream's events (stream.go) and the union types (unions.go).

`

const tsPrelude = `export type Method = "GET" | "POST" | "PUT" | "DELETE";

export type Outcome<T> =
  | { kind: "ok"; status: number; value: T; notices: Notice[] }
  | { kind: "pending" }
  | { kind: "unpaired" }
  | { kind: "refused"; status: number; message: string }
  | { kind: "stopping"; message: string }
  | { kind: "timeout"; message: string }
  | { kind: "failed"; status: number; message: string };

export interface Transport {
  send<T>(
    method: Method,
    path: string,
    query: Record<string, string> | undefined,
    body: unknown,
  ): Promise<Outcome<T>>;
}

`

const tsQueryOf = `function queryOf(query: object | undefined): Record<string, string> | undefined {
  if (query === undefined) {
    return undefined;
  }
  const out: Record<string, string> = {};
  for (const [name, value] of Object.entries(query)) {
    if (typeof value === "string") {
      out[name] = value;
    }
  }
  return out;
}

`

var (
	validMethod = map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true}
	pathValue   = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)
)

// pathValues are the names in path's {name} segments, in order.
func pathValues(path string) ([]string, error) {
	found := pathValue.FindAllStringSubmatch(path, -1)
	if strings.Count(path, "{") != len(found) || strings.Count(path, "}") != len(found) {
		return nil, fmt.Errorf("%s: a path value must be a plain {name}", path)
	}
	names := make([]string, len(found))
	for i, f := range found {
		names[i] = f[1]
	}
	return names, nil
}

// pathType is the TS type of a path value: the session and the job are
// numbers, everything else a string.
func pathType(name string) string {
	if name == "id" || name == "job" {
		return "number"
	}
	return "string"
}

// pathExpr is a path as a TS expression: a string, or a template literal
// when it holds path values (a string one is encoded).
func pathExpr(path string) string {
	if !strings.Contains(path, "{") {
		return strconv.Quote(path)
	}
	out := pathValue.ReplaceAllStringFunc(path, func(s string) string {
		name := s[1 : len(s)-1]
		if pathType(name) == "number" {
			return "${" + name + "}"
		}
		return "${encodeURIComponent(" + name + ")}"
	})
	return "`" + out + "`"
}

// clientCode is the TS of the client: the Client interface's members, the
// object createClient returns, and whether any route takes a query (the
// queryOf helper is written only then).
type clientCode struct {
	iface    string
	impl     string
	hasQuery bool
}

// client writes one method per route, named by its client name, taking the
// path values in path order and then the query or the body, and answering
// Promise<Outcome<Resp>>; a page route gives <client>URL instead.
func (m *model) client() (clientCode, error) {
	var code clientCode
	var ib, mb strings.Builder
	seen := map[string]bool{}
	for _, r := range m.c.Routes {
		where := r.Method + " " + r.Path
		if !validMethod[r.Method] {
			return clientCode{}, fmt.Errorf("%s: the method must be GET, POST, PUT or DELETE", where)
		}
		if !identifier.MatchString(r.Client) {
			return clientCode{}, fmt.Errorf("%s: the client name %q is not a TS identifier", where, r.Client)
		}
		name := r.Client
		if r.Page {
			name += "URL"
		}
		if seen[name] {
			return clientCode{}, fmt.Errorf("%s: the client name %q is used twice", where, name)
		}
		seen[name] = true

		values, err := pathValues(r.Path)
		if err != nil {
			return clientCode{}, err
		}
		var args, names []string
		for _, v := range values {
			if v == "query" || v == "body" {
				return clientCode{}, fmt.Errorf("%s: a path value cannot be called %q", where, v)
			}
			args = append(args, v+": "+pathType(v))
			names = append(names, v)
		}

		if r.Page {
			if r.Query != nil || r.Body != nil || r.Resp != nil {
				return clientCode{}, fmt.Errorf("%s: a page route has no query, body or response type", where)
			}
			fmt.Fprintf(&ib, "  %s(%s): string;\n", name, strings.Join(args, ", "))
			fmt.Fprintf(&mb, "    %s: (%s) => %s,\n", name, strings.Join(names, ", "), pathExpr(r.Path))
			continue
		}

		query, body := "undefined", "undefined"
		if r.Query != nil {
			if r.Method != "GET" {
				return clientCode{}, fmt.Errorf("%s: only a GET takes a query", where)
			}
			ts, err := m.tsType(r.Query, where+" query")
			if err != nil {
				return clientCode{}, err
			}
			args = append(args, "query?: Partial<"+ts+">")
			names = append(names, "query")
			query = "queryOf(query)"
			code.hasQuery = true
		}
		if r.Body != nil {
			if r.Method == "GET" {
				return clientCode{}, fmt.Errorf("%s: a GET takes no body", where)
			}
			ts, err := m.tsType(r.Body, where+" body")
			if err != nil {
				return clientCode{}, err
			}
			args = append(args, "body: "+ts)
			names = append(names, "body")
			body = "body"
		}

		result := "void"
		switch r.OK {
		case 204:
			if r.Resp != nil {
				return clientCode{}, fmt.Errorf("%s: a 204 has no body, so no response type", where)
			}
		case 200, 201:
			if r.Resp == nil {
				return clientCode{}, fmt.Errorf("%s: a JSON route needs a response type", where)
			}
			ts, err := m.tsType(r.Resp, where+" response")
			if err != nil {
				return clientCode{}, err
			}
			result = ts
		default:
			return clientCode{}, fmt.Errorf("%s: the success status must be 200, 201 or 204, not %d", where, r.OK)
		}

		fmt.Fprintf(&ib, "  %s(%s): Promise<Outcome<%s>>;\n", name, strings.Join(args, ", "), result)
		fmt.Fprintf(&mb, "    %s: (%s) => t.send<%s>(%s, %s, %s, %s),\n",
			name, strings.Join(names, ", "), result, strconv.Quote(r.Method), pathExpr(r.Path), query, body)
	}
	code.iface, code.impl = ib.String(), mb.String()
	return code, nil
}

// genTS is gen.ts: the types, Method, Outcome and Transport, the stream's
// event map, the Client and createClient. It has no imports.
func (m *model) genTS() ([]byte, error) {
	hasNotice := false
	for _, n := range m.names {
		hasNotice = hasNotice || n == "Notice"
	}
	if !hasNotice {
		return nil, fmt.Errorf("gen.ts needs a Notice type, which Outcome carries, and no route or event reaches one")
	}
	types, err := m.types()
	if err != nil {
		return nil, err
	}
	var events strings.Builder
	events.WriteString("export interface StreamEvents {\n")
	for _, e := range m.c.Events {
		ts, err := m.tsType(e.Data, "event "+e.Name)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&events, "  %s: %s;\n", key(e.Name), ts)
	}
	events.WriteString("}\n\n")
	code, err := m.client()
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString(tsHeader)
	b.WriteString(types)
	b.WriteString(tsPrelude)
	b.WriteString(events.String())
	b.WriteString("export interface Client {\n" + code.iface + "}\n\n")
	if code.hasQuery {
		b.WriteString(tsQueryOf)
	}
	b.WriteString("export function createClient(t: Transport): Client {\n  return {\n" + code.impl + "  };\n}\n")
	return []byte(b.String()), nil
}

const mdHeader = "# Web API\n\n" +
	"Generated by `go generate ./internal/web` from the route table (`internal/web/routes.go`), the stream's events table (`internal/web/stream.go`) and the union types (`internal/web/unions.go`); do not edit it by hand. " +
	"Every route needs the paired device's cookie except the one marked open. " +
	"`internal/web/ui/src/api/gen.ts` is the app's typed form of this reference, and `internal/web/api.fields` pins every field, route, event and union value that has shipped: " +
	"one may be added, and removing or changing one means deleting its line there by hand in the same commit.\n\n"

// typeRef is one of a route's three types, with the word the reference and
// api.fields use for it.
type typeRef struct {
	key   string
	title string
	t     reflect.Type
}

// refs are a route's query, body and response types, in that order; one a
// route lacks has a nil t.
func refs(r web.ContractRoute) []typeRef {
	return []typeRef{
		{"query", "Query", r.Query},
		{"body", "Body", r.Body},
		{"resp", "Response", r.Resp},
	}
}

// webAPI is docs/specs/web-api.md: every route with its doc, then every
// stream event, every type's fields and every union's values.
func (m *model) webAPI() ([]byte, error) {
	var b strings.Builder
	b.WriteString(mdHeader)
	b.WriteString("## Routes\n\n")
	for _, r := range m.c.Routes {
		where := r.Method + " " + r.Path
		fmt.Fprintf(&b, "### `%s`\n\n", where)
		if r.Doc != "" {
			b.WriteString(r.Doc + "\n\n")
		}
		auth := "the device cookie"
		if r.Open {
			auth = "none (open)"
		}
		fmt.Fprintf(&b, "- Auth: %s\n", auth)
		if r.Page {
			fmt.Fprintf(&b, "- Page: HTML for the app's iframe; the generated client builds its URL as `%sURL`.\n", r.Client)
		} else {
			fmt.Fprintf(&b, "- Success: %d\n- Client: `%s`\n", r.OK, r.Client)
		}
		for _, ref := range refs(r) {
			if ref.t == nil {
				continue
			}
			ts, err := m.tsType(ref.t, where+" "+ref.key)
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(&b, "- %s: `%s`\n", ref.title, ts)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Stream events\n\n")
	b.WriteString("`GET /api/v1/stream` sends these server-sent events; the first is always `snapshot`.\n\n")
	for _, e := range m.c.Events {
		ts, err := m.tsType(e.Data, "event "+e.Name)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "- `%s`: `%s`\n", e.Name, ts)
	}
	b.WriteString("\n")

	b.WriteString("## Types\n\n")
	for _, t := range m.sorted {
		fmt.Fprintf(&b, "### `%s`\n\n", m.names[t])
		for _, f := range m.fields[t] {
			ts, err := m.fieldType(f, t.Name()+"."+f.name)
			if err != nil {
				return nil, err
			}
			opt := ""
			if f.optional {
				opt = "?"
			}
			fmt.Fprintf(&b, "- `%s%s`: `%s`\n", f.name, opt, ts)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Unions\n\n")
	for _, u := range m.unions {
		quoted := make([]string, len(u.Values))
		for i, v := range u.Values {
			quoted[i] = "`" + strconv.Quote(v) + "`"
		}
		fmt.Fprintf(&b, "### `%s`\n\n%s\n\n", u.Name, strings.Join(quoted, ", "))
	}
	return []byte(b.String()), nil
}
