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
