package core

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allEvents holds a zero value of every concrete Event type, for
// TestCoreIsValueTyped; TestAllEventsListsEveryEvent keeps it complete.
var allEvents = []Event{
	AccountsChanged{}, Alive{}, ClientsStale{}, GitHubChanged{},
	HealthChecked{}, InstancesChanged{}, Notice{}, Reactivated{},
	Recovered{}, Reply{}, SessionLaunched{}, Started{},
	StatusesChanged{}, ViewsChanged{}, WorkspacesChanged{},
}

// modelOwned are the named types a client must never receive: the model's
// own objects, whose memory it shares with the model.
var modelOwned = map[string]bool{
	"github.com/aidan-bailey/loom/core.Workspace":           true,
	"github.com/aidan-bailey/loom/core.Model":               true,
	"github.com/aidan-bailey/loom/session.Instance":         true,
	"github.com/aidan-bailey/loom/session.Storage":          true,
	"github.com/aidan-bailey/loom/config.Config":            true,
	"github.com/aidan-bailey/loom/config.WorkspaceRegistry": true,
	"github.com/aidan-bailey/loom/config.State":             true,
	"github.com/aidan-bailey/loom/account.Registry":         true,
}

var (
	errorType = reflect.TypeOf((*error)(nil)).Elem()
	eventType = reflect.TypeOf((*Event)(nil)).Elem()
	timeType  = reflect.TypeOf(time.Time{})
)

// plainProblem returns why typ is not plain data, naming the path to the
// offending part, or "" when it is. Plain data is what a client in another
// process could be sent (stage 2 serializes it) without sharing the
// model's memory: basic kinds; structs, slices, arrays and maps of plain
// data; pointers to plain data (copied by contract: every query and event
// hands out copies); time.Time; and the interfaces error and Event, which
// the codec maps (an error to a code and message, an Event to its frame).
func plainProblem(typ reflect.Type, path string, seen map[reflect.Type]bool) string {
	if typ == errorType || typ == eventType || typ == timeType {
		return ""
	}
	if typ.Name() != "" && modelOwned[typ.PkgPath()+"."+typ.Name()] {
		return fmt.Sprintf("%s: %s is the model's own object", path, typ)
	}
	if typ.PkgPath() == "sync" || typ.PkgPath() == "sync/atomic" {
		return fmt.Sprintf("%s: %s is a synchronization type", path, typ)
	}
	switch typ.Kind() {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return ""
	case reflect.Struct:
		if seen[typ] {
			return ""
		}
		seen[typ] = true
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if p := plainProblem(f.Type, path+"."+f.Name, seen); p != "" {
				return p
			}
		}
		return ""
	case reflect.Slice, reflect.Array:
		return plainProblem(typ.Elem(), path+"[]", seen)
	case reflect.Pointer:
		return plainProblem(typ.Elem(), "*"+path, seen)
	case reflect.Map:
		if p := plainProblem(typ.Key(), path+"[key]", seen); p != "" {
			return p
		}
		return plainProblem(typ.Elem(), path+"[value]", seen)
	default:
		return fmt.Sprintf("%s: %s (%s) is not plain data", path, typ, typ.Kind())
	}
}

// TestCoreIsValueTyped fails when a core.Core method, or an Event the
// model emits, carries anything but plain data (plainProblem): values a
// client in another process could receive (stage 2 serializes them)
// without sharing the model's memory.
func TestCoreIsValueTyped(t *testing.T) {
	iface := reflect.TypeOf((*Core)(nil)).Elem()
	for i := 0; i < iface.NumMethod(); i++ {
		m := iface.Method(i)
		for j := 0; j < m.Type.NumIn(); j++ {
			if p := plainProblem(m.Type.In(j), fmt.Sprintf("%s param %d", m.Name, j), map[reflect.Type]bool{}); p != "" {
				t.Error(p)
			}
		}
		for j := 0; j < m.Type.NumOut(); j++ {
			if p := plainProblem(m.Type.Out(j), fmt.Sprintf("%s result %d", m.Name, j), map[reflect.Type]bool{}); p != "" {
				t.Error(p)
			}
		}
	}
	for _, ev := range allEvents {
		typ := reflect.TypeOf(ev)
		if p := plainProblem(typ, typ.Name(), map[reflect.Type]bool{}); p != "" {
			t.Error(p)
		}
	}
}

// TestPlainProblem_Bites: the rule rejects each thing it exists to reject.
func TestPlainProblem_Bites(t *testing.T) {
	bad := map[string]any{
		"func":           func() {},
		"chan":           make(chan int),
		"interface":      struct{ X any }{},
		"model object":   &Workspace{},
		"nested model":   struct{ W []*Workspace }{},
		"in a map value": map[string]*Model{},
		"sync":           &struct{ M sync.Mutex }{},
	}
	for name, v := range bad {
		assert.NotEmpty(t, plainProblem(reflect.TypeOf(v), name, map[reflect.Type]bool{}), name)
	}
	good := []any{InstanceView{}, WorkspaceView{}, RegistryView{}, AccountNames{}, Reply{}, struct {
		E error
		T time.Time
		P *int
	}{}}
	for _, v := range good {
		assert.Empty(t, plainProblem(reflect.TypeOf(v), fmt.Sprintf("%T", v), map[reflect.Type]bool{}))
	}
}

// TestAllEventsListsEveryEvent keeps allEvents complete: every type in the
// package's non-test files with a coreEvent method is in it.
func TestAllEventsListsEveryEvent(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	var declared []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		require.NoError(t, err)
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "coreEvent" || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			if id, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
				declared = append(declared, id.Name)
			}
		}
	}
	var listed []string
	for _, ev := range allEvents {
		listed = append(listed, reflect.TypeOf(ev).Name())
	}
	sort.Strings(declared)
	sort.Strings(listed)
	assert.Equal(t, declared, listed, "allEvents must list every Event type")
}
