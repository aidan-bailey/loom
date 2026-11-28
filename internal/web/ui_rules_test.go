package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

// frameDocumentNames are the properties through which code reaches into a frame's document or
// window. The terminal panel reads the frame only through the interface terminal/frame.ts gives
// it, whose one accessor (frameTerminal) is the only code that reads the frame's window.
var frameDocumentNames = []string{"contentDocument", "contentWindow"}

// frameDocumentLeaks lists, for every .ts and .tsx file under fsys, each name of
// frameDocumentNames it holds, as "<file>: <name>", and counts the files it looked at.
func frameDocumentLeaks(fsys fs.FS) (leaks []string, files int, err error) {
	err = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !(strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".tsx")) {
			return nil
		}
		files++
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		for _, word := range frameDocumentNames {
			if strings.Contains(string(body), word) {
				leaks = append(leaks, name+": "+word)
			}
		}
		return nil
	})
	return leaks, files, err
}

func TestFrameDocumentLeaks_FindsTheFramesDocumentAndWindow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string
		count int
	}{
		{"a clean panel", map[string]string{"index.tsx": "export const a = frameTerminal(el);"}, nil, 1},
		{"the document", map[string]string{"Terminal.tsx": "frame.contentDocument.body"}, []string{"Terminal.tsx: contentDocument"}, 1},
		{"the window", map[string]string{"Terminal.tsx": "frame.contentWindow?.loomTerminal"}, []string{"Terminal.tsx: contentWindow"}, 1},
		{"a test file", map[string]string{"Terminal.test.tsx": "el.contentWindow"}, []string{"Terminal.test.tsx: contentWindow"}, 1},
		{"a nested file", map[string]string{"sub/Deep.ts": "x.contentDocument"}, []string{"sub/Deep.ts: contentDocument"}, 1},
		{"both names in one file", map[string]string{"A.tsx": "a.contentDocument; b.contentWindow"}, []string{"A.tsx: contentDocument", "A.tsx: contentWindow"}, 1},
		{"a file that is not source is not read", map[string]string{"notes.md": "contentWindow", "style.css": "contentDocument"}, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			for name, body := range tc.files {
				fsys[name] = &fstest.MapFile{Data: []byte(body)}
			}
			leaks, files, err := frameDocumentLeaks(fsys)
			require.NoError(t, err)
			require.Equal(t, tc.want, leaks)
			require.Equal(t, tc.count, files)
		})
	}
}

func TestTerminalPanel_ReachesTheFrameOnlyThroughItsInterface(t *testing.T) {
	leaks, files, err := frameDocumentLeaks(os.DirFS(filepath.Join("ui", "src", "panels", "terminal")))
	require.NoError(t, err)
	// index.tsx, Terminal.tsx and Terminal.test.tsx at least: a walk that finds nothing would pass
	// for ever.
	require.GreaterOrEqual(t, files, 3, "the walk found too few files under ui/src/panels/terminal")
	require.Empty(t, leaks, "the terminal panel reaches into the frame's document or window; go through terminal/frame.ts's interface")
}
