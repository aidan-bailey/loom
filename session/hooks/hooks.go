package hooks

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Layout of an instance's hooks folder.
const (
	settingsFile = "settings.json"
	launchIDFile = "launch-id"
	eventsSubdir = "events"
)

// SettingsPath is the file passed to claude --settings.
func SettingsPath(dir string) string { return filepath.Join(dir, settingsFile) }

// EventsDir is where hooks write one file per event.
func EventsDir(dir string) string { return filepath.Join(dir, eventsSubdir) }

func launchIDPath(dir string) string { return filepath.Join(dir, launchIDFile) }

// SafePath reports whether p can be embedded in the single-quoted hook
// command and the single-quoted --settings flag.
func SafePath(p string) bool { return !strings.ContainsRune(p, '\'') }

// HookCommand returns the shell command every hook runs. It writes the
// payload Claude pipes on stdin to a new file in eventsDir, first as .tmp
// and then renamed, so a reader never sees a partial file. It always exits
// 0 and always reads its whole input, including when eventsDir is gone or
// the disk is full, so Claude never reports a hook error or fails writing
// the payload. The name is <unix-nanos>-<pid>; macOS date prints a literal
// N for %N, so there the pid alone keeps names unique and the scan orders
// by modification time. eventsDir must satisfy SafePath.
func HookCommand(eventsDir string) string {
	return `f='` + eventsDir + `/'"$(date +%s%N)-$$"; ` +
		`{ cat > "$f.tmp" && mv "$f.tmp" "$f.json"; } 2>/dev/null || cat >/dev/null`
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type hookMatcher struct {
	Hooks []hookCommand `json:"hooks"`
}

type settingsDoc struct {
	Hooks map[string][]hookMatcher `json:"hooks"`
}

// SettingsJSON returns the settings file registering HookCommand for every
// event in HookEvents. Claude adds these hooks to the user's own.
func SettingsJSON(eventsDir string) ([]byte, error) {
	cmd := HookCommand(eventsDir)
	doc := settingsDoc{Hooks: make(map[string][]hookMatcher, len(HookEvents))}
	for _, name := range HookEvents {
		doc.Hooks[name] = []hookMatcher{{Hooks: []hookCommand{{Type: "command", Command: cmd}}}}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep the command's > readable
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("hooks: encode settings: %w", err)
	}
	return buf.Bytes(), nil
}

// Prepare empties dir and writes a fresh settings.json, events folder and
// launch-id for a new launch, returning the launch ID. Events from an
// earlier launch describe agents that no longer exist, so nothing is kept.
// launch-id is written last, so a concurrent scan never sees a new ID
// without its settings.
func Prepare(dir string) (string, error) {
	if !SafePath(dir) {
		return "", fmt.Errorf("hooks: hooks folder %q contains a single quote", dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("hooks: clear hooks folder: %w", err)
	}
	if err := os.MkdirAll(EventsDir(dir), 0o700); err != nil {
		return "", fmt.Errorf("hooks: create hooks folder: %w", err)
	}
	settings, err := SettingsJSON(EventsDir(dir))
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(SettingsPath(dir), settings, 0o600); err != nil {
		return "", fmt.Errorf("hooks: write settings: %w", err)
	}
	id, err := newLaunchID()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(launchIDPath(dir), []byte(id), 0o600); err != nil {
		return "", fmt.Errorf("hooks: write launch-id: %w", err)
	}
	return id, nil
}

func newLaunchID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("hooks: launch id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
