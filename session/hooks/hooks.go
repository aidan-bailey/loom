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
	briefFile    = "subagent-context.json"
)

// SettingsPath is the file passed to claude --settings.
func SettingsPath(dir string) string { return filepath.Join(dir, settingsFile) }

// EventsDir is where hooks write one file per event.
func EventsDir(dir string) string { return filepath.Join(dir, eventsSubdir) }

// BriefPath is the file briefCommand prints: the SubagentStart hook output
// that hands a subagent loom's context.
func BriefPath(dir string) string { return filepath.Join(dir, briefFile) }

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

// briefMatcher matches every agent type but a fork, which already holds
// its parent's whole prompt. Claude Code reads a matcher holding any
// character but letters, digits, _, -, spaces, "," and "|" as an
// unanchored JavaScript regex; Go's regexp has no lookahead, so only the
// opt-in TestRealClaude_SubagentBrief can pin what it matches.
const briefMatcher = "^(?!fork$)"

// briefCommand returns the shell command that hands a subagent the brief
// at briefPath, which holds the whole hook output. Like HookCommand it
// reads its whole input first and always exits 0, so Claude never fails
// writing the payload or reports a hook error; with the file gone it
// prints nothing, and the subagent starts unbriefed. briefPath must
// satisfy SafePath.
func briefCommand(briefPath string) string {
	return `cat >/dev/null; cat '` + briefPath + `' 2>/dev/null; exit 0`
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type hookMatcher struct {
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}

type settingsDoc struct {
	Hooks               map[string][]hookMatcher `json:"hooks"`
	AutoMemoryDirectory string                   `json:"autoMemoryDirectory,omitempty"`
}

// SettingsJSON returns the settings file registering HookCommand for every
// event in HookEvents. Claude adds these hooks to the user's own. A
// non-empty autoMemoryDir also sets Claude's autoMemoryDirectory, which
// outranks the user's own settings for this launch. A non-empty briefPath
// adds a second SubagentStart entry, for every agent type but a fork
// (briefMatcher), running briefCommand on it.
func SettingsJSON(eventsDir, autoMemoryDir, briefPath string) ([]byte, error) {
	cmd := HookCommand(eventsDir)
	doc := settingsDoc{Hooks: make(map[string][]hookMatcher, len(HookEvents)), AutoMemoryDirectory: autoMemoryDir}
	for _, name := range HookEvents {
		doc.Hooks[name] = []hookMatcher{{Hooks: []hookCommand{{Type: "command", Command: cmd}}}}
	}
	if briefPath != "" {
		doc.Hooks[EventSubagentStart] = append(doc.Hooks[EventSubagentStart], hookMatcher{
			Matcher: briefMatcher,
			Hooks:   []hookCommand{{Type: "command", Command: briefCommand(briefPath)}},
		})
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

// Prepare empties dir and writes a fresh settings.json (with
// autoMemoryDir, when set), events folder and launch-id for a new launch,
// returning the launch ID. A non-empty brief is what each subagent of the
// launch but a fork is told at its start: Prepare writes it as the hook
// output briefCommand prints (BriefPath) and registers that command.
// Events from an earlier launch describe agents that no longer exist, so
// nothing is kept. launch-id is written last, so a concurrent scan never
// sees a new ID without its settings.
func Prepare(dir, autoMemoryDir, brief string) (string, error) {
	if !SafePath(dir) {
		return "", fmt.Errorf("hooks: hooks folder %q contains a single quote", dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("hooks: clear hooks folder: %w", err)
	}
	if err := os.MkdirAll(EventsDir(dir), 0o700); err != nil {
		return "", fmt.Errorf("hooks: create hooks folder: %w", err)
	}
	briefPath := ""
	if brief != "" {
		briefPath = BriefPath(dir)
		if err := writeBrief(briefPath, brief); err != nil {
			return "", err
		}
	}
	settings, err := SettingsJSON(EventsDir(dir), autoMemoryDir, briefPath)
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

// writeBrief writes path as the SubagentStart hook output whose
// additionalContext is brief.
func writeBrief(path, brief string) error {
	type output struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	}
	b, err := json.Marshal(struct {
		HookSpecificOutput output `json:"hookSpecificOutput"`
	}{output{EventSubagentStart, brief}})
	if err != nil {
		return fmt.Errorf("hooks: encode subagent brief: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("hooks: write subagent brief: %w", err)
	}
	return nil
}

func newLaunchID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("hooks: launch id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
