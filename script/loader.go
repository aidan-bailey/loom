package script

import (
	"claude-squad/log"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// loadScripts walks dir looking for *.lua files and runs each one
// under the engine's LState. Called under e.mu — the engine sets
// e.loading=true before invoking us so cs.register_action can tell
// it is being called from a module top level rather than from
// inside a dispatched action.
//
// Error policy: a broken file logs a warning and does not prevent
// subsequent files from loading. A missing directory is a silent
// no-op so a user who never creates ~/.claude-squad/scripts sees
// nothing unusual.
func loadScripts(e *Engine, dir string) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.WarningLog.Printf("script loader: cannot read %s: %v", dir, err)
		}
		return
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".lua") {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files) // deterministic registration order

	e.loading = true
	defer func() { e.loading = false }()

	for _, name := range files {
		path := filepath.Join(dir, name)
		e.curFile = name
		if err := e.L.DoFile(path); err != nil {
			log.WarningLog.Printf("script loader: %s: %v", name, err)
			// DoFile may have left the stack in a partially-bad
			// state; clear it so the next file starts clean.
			e.L.SetTop(0)
			continue
		}
		log.InfoLog.Printf("script loader: loaded %s", name)
	}
	e.curFile = ""
}

// currentFile returns the script file currently being loaded, or a
// placeholder. The value is only non-empty while loadScripts is in
// its inner loop, but api.RegisterAction captures it at registration
// time so later error messages can cite the right source.
func (e *Engine) currentFile() string {
	if e.curFile == "" {
		return "<runtime>"
	}
	return e.curFile
}

// LoadFromString is a test-only entry point that executes the given
// chunk as a script. Used by tests that want to exercise the API
// without writing temp files to disk.
func (e *Engine) LoadFromString(name, chunk string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.loading = true
	e.curFile = name
	defer func() {
		e.loading = false
		e.curFile = ""
	}()

	if err := e.L.DoString(chunk); err != nil {
		e.L.SetTop(0)
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}
