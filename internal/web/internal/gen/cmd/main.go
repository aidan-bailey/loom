// Command cmd writes internal/web/ui/src/api/gen.ts, docs/specs/web-api.md
// and internal/web/api.fields from the route table, the stream's events and
// the union types. Run it with go generate in internal/web, after any change
// to routes.go, stream.go or unions.go, or to a type they name.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aidan-bailey/loom/internal/web"
	"github.com/aidan-bailey/loom/internal/web/internal/gen"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run() error {
	if _, err := os.Stat("contract.go"); err != nil {
		return fmt.Errorf("run it with go generate in internal/web (no contract.go here): %w", err)
	}
	tsPath, mdPath, fieldsPath := gen.Files(".")
	committed, err := gen.ReadFields(fieldsPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	c := web.Contract()
	out, err := gen.Generate(c, committed)
	if err != nil {
		return err
	}
	fresh, err := gen.Generate(c, nil)
	if err != nil {
		return err
	}
	for _, l := range gen.Dropped(committed, fresh.Fields) {
		fmt.Fprintf(os.Stderr, "gen: api.fields keeps %q, which the code no longer produces; delete the line by hand if the removal is intended\n", l)
	}
	for _, f := range []struct {
		path string
		data []byte
	}{
		{tsPath, out.GenTS},
		{mdPath, out.WebAPI},
		{fieldsPath, gen.FormatFields(out.Fields)},
	} {
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(f.path, f.data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
