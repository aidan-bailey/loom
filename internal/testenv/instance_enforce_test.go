package testenv

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const module = "github.com/aidan-bailey/loom"

// modelObjects are, by import path, the names a TUI file must not use: the
// model's own objects (whose memory the model owns), their constructors,
// and the process-wide toggles only the model sets.
//   - session: an instance (the type, its constructors and options), the
//     storage instances persist to, and the launch toggles the model sets
//     on a settings save (core.Model.SaveSettings).
//   - core: a loaded workspace, the model and its loop, and the jobs and
//     output it keeps to itself (the TUI holds a core.Core, built with
//     core.New and started in newHome, and handles no job).
//   - config: the workspace registry and state.json (the TUI reads
//     core.RegistryView and core.WorkspaceView copies), and the config
//     save (the model writes config.json).
//   - account: the account registry (the TUI reads core.AccountNames).
var modelObjects = map[string]map[string]bool{
	module + "/session": {
		"Instance": true, "NewInstance": true, "FromInstanceData": true, "InstanceOptions": true,
		"Storage": true, "NewStorage": true,
		"SetLoomContextEnabled": true, "SetSubagentTrackingEnabled": true,
	},
	module + "/core": {
		"Workspace": true, "WorkspaceParts": true, "NewWorkspace": true, "Model": true,
		"Loop": true, "Job": true, "Out": true,
	},
	module + "/config": {
		"WorkspaceRegistry": true, "LoadWorkspaceRegistry": true,
		"AppState": true, "State": true, "LoadStateFrom": true,
		"SaveConfigTo": true,
	},
	module + "/account": {
		"Registry": true, "LoadRegistry": true,
	},
}

// handover exempts, by file and function, the names a function may use to
// hand the startup objects main.go builds to core.New: Run and newHome
// take the workspace registry as a parameter and pass it on, reading none
// of it after the model is built.
var handover = map[string]map[string]map[string]bool{
	"app/app_init.go": {
		"Run":     {module + "/config.WorkspaceRegistry": true},
		"newHome": {module + "/config.WorkspaceRegistry": true},
	},
}

// bridgeNames are the model's former bridge from an InstanceID to an
// instance and back, and the script host's former wrapper of it (app's
// instOf), deleted in stage 1C package D: flagged anywhere, in case one
// comes back.
var bridgeNames = map[string]bool{
	"InstanceOf":     true,
	"IDFor":          true,
	"AdoptForScript": true,
	"instOf":         true,
}

// TestTUIHoldsNoModelObject fails when the TUI (app, ui and its
// subpackages) or the script engine (script) names one of the model's own
// objects in production code (modelObjects): a session instance, its
// storage, a loaded workspace, the model, the workspace or account
// registry, state.json, the config save or the launch toggles. It grew
// from TestTUIHoldsNoInstance (daemon stage 1C), which covered instances
// only. The TUI and Lua see the model only as values (core.InstanceView,
// core.WorkspaceView, …) and change it only by request. Test files are
// exempt, and so is the startup handover (handover).
func TestTUIHoldsNoModelObject(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(root, "go.mod"))

	var offenders []string
	for _, dir := range []string{"app", "ui", "script"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			require.NoError(t, err)
			offenders = append(offenders, modelObjectUses(t, path, filepath.ToSlash(rel))...)
			return nil
		})
		require.NoError(t, err)
	}
	assert.Empty(t, offenders, "the TUI must hold none of the model's objects: it reads values and acts by request")
}

// modelObjectUses lists path's uses of a forbidden name (modelObjects)
// through its import of the package defining it, under whatever name the
// file imports it (a dot import included), and of a bridge method
// (bridgeNames) on anything. rel is path relative to the module root, for
// the handover exemption.
func modelObjectUses(t *testing.T, path, rel string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	require.NoError(t, err)
	// local import name → import path, for the packages modelObjects names.
	imported := map[string]string{}
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		require.NoError(t, err)
		if modelObjects[p] == nil {
			continue
		}
		name := p[strings.LastIndex(p, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		imported[name] = p
	}
	var uses []string
	check := func(n ast.Node, exempt map[string]bool) {
		ast.Inspect(n, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				if bridgeNames[x.Sel.Name] {
					uses = append(uses, fset.Position(x.Pos()).String()+": "+x.Sel.Name)
					return true
				}
				pkg, ok := x.X.(*ast.Ident)
				if !ok {
					return true
				}
				ip, ok := imported[pkg.Name]
				if ok && ip != "" && modelObjects[ip][x.Sel.Name] && !exempt[ip+"."+x.Sel.Name] {
					uses = append(uses, fset.Position(x.Pos()).String()+": "+pkg.Name+"."+x.Sel.Name)
				}
			case *ast.Ident:
				// A dot import puts the names in the file's own scope.
				if ip, ok := imported["."]; ok && modelObjects[ip][x.Name] && !exempt[ip+"."+x.Name] {
					uses = append(uses, fset.Position(x.Pos()).String()+": "+x.Name)
				}
			}
			return true
		})
	}
	for _, d := range file.Decls {
		exempt := map[string]bool{}
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil {
			exempt = handover[rel][fn.Name.Name]
		}
		check(d, exempt)
	}
	return uses
}
