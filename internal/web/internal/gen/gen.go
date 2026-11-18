// Package gen turns internal/web's route table, stream events and union types
// (web.Contract) into the app's typed contract: ui/src/api/gen.ts, the
// reference docs/specs/web-api.md, and api.fields, which pins every field,
// route, event and union value that has shipped. It reads Go types by
// reflection and writes what encoding/json writes; `go generate ./internal/web`
// runs it, and TestContract_GeneratedFilesAreFresh fails when a file is stale.
package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
