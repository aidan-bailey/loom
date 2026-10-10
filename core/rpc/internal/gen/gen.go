// Package gen generates core/rpc's wire code from core.Core (core/iface.go):
// every method's parameter and result types, the method table, the
// server's dispatch, and the client's methods. One source of truth keeps
// the two sides of the wire from drifting; TestGenerated_IsFresh fails when
// methods_gen.go no longer matches iface.go.
package gen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Kinds of method, from a method's line comment in iface.go.
const (
	kindRequest = "request"
	kindLocal   = "local"
	kindCast    = "cast"
	kindClient  = "client"
)

type param struct{ name, typ string }

type method struct {
	name    string
	kind    string
	params  []param
	values  []string // non-error results
	withErr bool     // the last result is an error
}

// builtin are the predeclared types iface.go uses unqualified.
var builtin = map[string]bool{
	"bool": true, "string": true, "int": true, "int64": true, "uint32": true,
	"uint64": true, "float64": true, "byte": true, "rune": true, "any": true, "error": true,
}

// Generate returns methods_gen.go for the Core interface in src
// (core/iface.go's source). pkg is the source of core's other files
// (CoreSources), whose type declarations it reads to refuse a request ID
// nested in a parameter (nestedReqID).
func Generate(src []byte, pkg ...[]byte) ([]byte, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "iface.go", src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	types := typeDecls(file)
	for i, other := range pkg {
		f, err := parser.ParseFile(fset, fmt.Sprintf("core file %d", i), other, 0)
		if err != nil {
			return nil, err
		}
		for name, t := range typeDecls(f) {
			types[name] = t
		}
	}
	imports := map[string]string{}
	for _, imp := range file.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		imports[name] = path
	}
	iface := findCore(file)
	if iface == nil {
		return nil, fmt.Errorf("no Core interface in iface.go")
	}
	used := map[string]bool{}
	var methods []method
	for _, f := range iface.Methods.List {
		ft, ok := f.Type.(*ast.FuncType)
		if !ok || len(f.Names) != 1 {
			return nil, fmt.Errorf("Core must hold methods only")
		}
		m := method{name: f.Names[0].Name, kind: kindRequest}
		if f.Doc != nil {
			for _, line := range strings.Split(f.Doc.Text(), "\n") {
				if strings.Contains(line, "rpc:") {
					return nil, fmt.Errorf("%s: an rpc: directive is a line comment after the method, not part of its doc comment", m.name)
				}
			}
		}
		if f.Comment != nil {
			switch c := strings.TrimSpace(f.Comment.Text()); c {
			case "rpc:local":
				m.kind = kindLocal
			case "rpc:cast":
				m.kind = kindCast
			case "rpc:client":
				m.kind = kindClient
			default:
				return nil, fmt.Errorf("%s: unknown line comment %q", m.name, c)
			}
		}
		for _, p := range ft.Params.List {
			typ, err := render(p.Type, used)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", m.name, err)
			}
			if len(p.Names) == 0 {
				return nil, fmt.Errorf("%s: unnamed parameter: its name is its wire field", m.name)
			}
			if nestedReqID(p.Type, types, map[string]bool{}) {
				return nil, fmt.Errorf("%s: parameter %s carries a request ID inside %s: the server tags only a parameter of type ReqID, so this one would reach the model untagged", m.name, p.Names[0].Name, typ)
			}
			for _, n := range p.Names {
				m.params = append(m.params, param{name: n.Name, typ: typ})
			}
		}
		if ft.Results != nil {
			for _, r := range ft.Results.List {
				typ, err := render(r.Type, used)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", m.name, err)
				}
				for range max(1, len(r.Names)) {
					if typ == "error" {
						m.withErr = true
					} else {
						if m.withErr {
							return nil, fmt.Errorf("%s: the error must be the last result", m.name)
						}
						m.values = append(m.values, typ)
					}
				}
			}
		}
		if len(m.values) > 2 || (len(m.values) == 2 && m.values[1] != "bool") || (len(m.values) == 2 && m.withErr) {
			return nil, fmt.Errorf("%s: results must be (), (T), (T, bool), with an optional error last", m.name)
		}
		if m.kind == kindCast && (len(m.values) > 0 || m.withErr) {
			return nil, fmt.Errorf("%s: a cast returns nothing", m.name)
		}
		methods = append(methods, m)
	}
	return emit(methods, imports, used)
}

// CoreSources reads the core package in dir: iface.go, and the source of
// every other non-test file, for Generate.
func CoreSources(dir string) (iface []byte, pkg [][]byte, err error) {
	iface, err = os.ReadFile(filepath.Join(dir, "iface.go"))
	if err != nil {
		return nil, nil, err
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, nil, err
	}
	for _, path := range paths {
		base := filepath.Base(path)
		if base == "iface.go" || strings.HasSuffix(base, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		pkg = append(pkg, src)
	}
	return iface, pkg, nil
}

// typeDecls are the types file declares, by name.
func typeDecls(file *ast.File) map[string]ast.Expr {
	types := map[string]ast.Expr{}
	for _, d := range file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, s := range gd.Specs {
			if ts, ok := s.(*ast.TypeSpec); ok {
				types[ts.Name.Name] = ts.Type
			}
		}
	}
	return types
}

// nestedReqID reports whether e, a parameter's type, carries a request ID
// (core's ReqID) anywhere but as the type itself: as a slice's, map's or
// pointer's element, or as a field of a core type it names, at any depth.
// The server's dispatch tags only a parameter of type ReqID with the
// connection's number (rpc.tagReq), so a nested one would reach the model
// as the client numbered it, colliding with other clients' requests. Other
// packages' types cannot name core's. types are core's type declarations.
func nestedReqID(e ast.Expr, types map[string]ast.Expr, seen map[string]bool) bool {
	if id, ok := e.(*ast.Ident); ok && id.Name == "ReqID" {
		return false
	}
	return mentionsReqID(e, types, seen)
}

// mentionsReqID reports whether e names core's ReqID at any depth.
func mentionsReqID(e ast.Expr, types map[string]ast.Expr, seen map[string]bool) bool {
	switch e := e.(type) {
	case *ast.Ident:
		if e.Name == "ReqID" {
			return true
		}
		t, ok := types[e.Name]
		if !ok || seen[e.Name] {
			return false
		}
		seen[e.Name] = true
		return mentionsReqID(t, types, seen)
	case *ast.ArrayType:
		return mentionsReqID(e.Elt, types, seen)
	case *ast.MapType:
		return mentionsReqID(e.Key, types, seen) || mentionsReqID(e.Value, types, seen)
	case *ast.StarExpr:
		return mentionsReqID(e.X, types, seen)
	case *ast.StructType:
		for _, f := range e.Fields.List {
			if mentionsReqID(f.Type, types, seen) {
				return true
			}
		}
	}
	return false
}

// findCore returns the Core interface's type.
func findCore(file *ast.File) *ast.InterfaceType {
	for _, d := range file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, s := range gd.Specs {
			ts, ok := s.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "Core" {
				continue
			}
			if it, ok := ts.Type.(*ast.InterfaceType); ok {
				return it
			}
		}
	}
	return nil
}

// render writes a type expression as package rpc names it: core's own
// types qualified with core., other packages' kept, and records the
// packages it used.
func render(e ast.Expr, used map[string]bool) (string, error) {
	switch e := e.(type) {
	case *ast.Ident:
		if builtin[e.Name] {
			return e.Name, nil
		}
		used["core"] = true
		return "core." + e.Name, nil
	case *ast.SelectorExpr:
		pkg, ok := e.X.(*ast.Ident)
		if !ok {
			return "", fmt.Errorf("unsupported selector type")
		}
		used[pkg.Name] = true
		return pkg.Name + "." + e.Sel.Name, nil
	case *ast.ArrayType:
		if e.Len != nil {
			return "", fmt.Errorf("arrays are not supported, use a slice")
		}
		elt, err := render(e.Elt, used)
		return "[]" + elt, err
	case *ast.MapType:
		k, err := render(e.Key, used)
		if err != nil {
			return "", err
		}
		v, err := render(e.Value, used)
		return "map[" + k + "]" + v, err
	case *ast.StarExpr:
		x, err := render(e.X, used)
		return "*" + x, err
	}
	return "", fmt.Errorf("unsupported type %T", e)
}

// field is a parameter's Go field name: its name exported ("id" is ID).
func field(name string) string {
	if name == "id" {
		return "ID"
	}
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func emit(methods []method, imports map[string]string, used map[string]bool) ([]byte, error) {
	var b bytes.Buffer
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("// Code generated by core/rpc/internal/gen from core/iface.go; DO NOT EDIT.\n\npackage rpc\n\nimport (\n")
	p("\t\"encoding/json\"\n\n")
	used["core"] = true
	var paths []string
	for name := range used {
		path := imports[name]
		if name == "core" {
			path = "github.com/aidan-bailey/loom/core"
		}
		if path == "" {
			return nil, fmt.Errorf("package %s is not imported by iface.go", name)
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		p("\t%q\n", path)
	}
	p(")\n\n")

	for _, m := range methods {
		if m.kind == kindClient {
			continue
		}
		p("// %sParams are %s's parameters on the wire.\ntype %sParams struct {\n", m.name, m.name, m.name)
		for _, a := range m.params {
			p("\t%s %s `json:%q`\n", field(a.name), a.typ, a.name)
		}
		p("}\n\n")
		p("// %sResult is %s's result on the wire; an error travels in the reply.\ntype %sResult struct {\n", m.name, m.name, m.name)
		if len(m.values) > 0 {
			p("\tValue %s `json:\"value\"`\n", m.values[0])
		}
		if len(m.values) > 1 {
			p("\tOK bool `json:\"ok\"`\n")
		}
		p("}\n\n")
	}

	p("// methods are the Core methods the wire carries, in iface.go's order, with\n// how a client serves each (its line comment there).\nvar methods = []methodInfo{\n")
	for _, m := range methods {
		if m.kind == kindClient {
			continue
		}
		p("\t{Name: %q, Kind: kind%s, Params: %sParams{}, Result: %sResult{}},\n", m.name, strings.ToUpper(m.kind[:1])+m.kind[1:], m.name, m.name)
	}
	p("}\n\n")

	p("// dispatch calls method on b with params decoded, returning its result\n// for the reply and its error; found is false for a method the wire does\n// not carry. tag rewrites every request ID (core.ReqID) the params carry\n// into the server's, which names the connection (see Server).\nfunc dispatch(b core.Core, method string, params json.RawMessage, tag func(*core.ReqID) error) (result any, err error, found bool) {\n\tswitch method {\n")
	for _, m := range methods {
		if m.kind == kindClient {
			continue
		}
		p("\tcase %q:\n\t\tvar p %sParams\n\t\tif err := decodeParams(params, &p); err != nil {\n\t\t\treturn nil, err, true\n\t\t}\n", m.name, m.name)
		var args []string
		for _, a := range m.params {
			args = append(args, "p."+field(a.name))
			if a.typ == "core.ReqID" {
				p("\t\tif err := tag(&p.%s); err != nil {\n\t\t\treturn nil, err, true\n\t\t}\n", field(a.name))
			}
		}
		call := fmt.Sprintf("b.%s(%s)", m.name, strings.Join(args, ", "))
		switch {
		case len(m.values) == 0 && !m.withErr:
			p("\t\t%s\n\t\treturn %sResult{}, nil, true\n", call, m.name)
		case len(m.values) == 0:
			p("\t\terr := %s\n\t\treturn %sResult{}, err, true\n", call, m.name)
		case len(m.values) == 1 && !m.withErr:
			p("\t\treturn %sResult{Value: %s}, nil, true\n", m.name, call)
		case len(m.values) == 1:
			p("\t\tv, err := %s\n\t\treturn %sResult{Value: v}, err, true\n", call, m.name)
		default:
			p("\t\tv, ok := %s\n\t\treturn %sResult{Value: v, OK: ok}, nil, true\n", call, m.name)
		}
	}
	p("\t}\n\treturn nil, nil, false\n}\n\n")

	for _, m := range methods {
		if m.kind == kindClient {
			continue
		}
		var sig, args, wire []string
		for _, a := range m.params {
			sig = append(sig, a.name+" "+a.typ)
			args = append(args, a.name)
			wire = append(wire, field(a.name)+": "+a.name)
		}
		results := append([]string(nil), m.values...)
		if m.withErr {
			results = append(results, "error")
		}
		ret := ""
		switch len(results) {
		case 0:
		case 1:
			ret = " " + results[0]
		default:
			ret = " (" + strings.Join(results, ", ") + ")"
		}
		p("// %s is core.Core's %s (%s).\nfunc (c *Client) %s(%s)%s {\n", m.name, m.name, m.kind, m.name, strings.Join(sig, ", "), ret)
		params := fmt.Sprintf("%sParams{%s}", m.name, strings.Join(wire, ", "))
		switch m.kind {
		case kindCast:
			p("\tc.cast(%q, %s)\n", m.name, params)
		case kindLocal:
			var vars []string
			for i, v := range m.values {
				p("\tvar v%d %s\n", i, v)
				vars = append(vars, fmt.Sprintf("v%d", i))
			}
			if m.withErr {
				p("\tvar err error\n")
				vars = append(vars, "err")
			}
			p("\tc.local(func(r *replica) { %s = r.%s(%s) })\n", strings.Join(vars, ", "), m.name, strings.Join(args, ", "))
			p("\treturn %s\n", strings.Join(vars, ", "))
		default:
			// A request a Reply answers (it carries a ReqID) that is refused
			// as unavailable gets no Reply: the client records its request
			// IDs for its user to fail (noteRefused, TakeRefused).
			var reqs []string
			for _, a := range m.params {
				if a.typ == "core.ReqID" {
					reqs = append(reqs, a.name)
				}
			}
			noted := func(call string) string {
				if len(reqs) == 0 {
					return call
				}
				return fmt.Sprintf("c.noteRefused(%s, %s)", call, strings.Join(reqs, ", "))
			}
			p("\tvar r %sResult\n", m.name)
			noErr := noted(fmt.Sprintf("c.requestNoErr(%q, %s, &r)", m.name, params))
			withErr := fmt.Sprintf("err := c.request(%q, %s, &r)\n", m.name, params)
			if len(reqs) > 0 {
				withErr += "\t" + noted("err") + "\n"
			}
			switch {
			case len(m.values) == 0 && !m.withErr:
				p("\t%s\n", noErr)
			case len(m.values) == 0 && len(reqs) == 0:
				p("\treturn c.request(%q, %s, &r)\n", m.name, params)
			case len(m.values) == 0:
				p("\t%s\treturn err\n", withErr)
			case len(m.values) == 1 && !m.withErr:
				p("\t%s\n\treturn r.Value\n", noErr)
			case len(m.values) == 1:
				p("\t%s\treturn r.Value, err\n", withErr)
			default:
				p("\t%s\n\treturn r.Value, r.OK\n", noErr)
			}
		}
		p("}\n\n")
	}
	return format.Source(b.Bytes())
}
