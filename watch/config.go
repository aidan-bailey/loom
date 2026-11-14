package watch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/aidan-bailey/loom/config"
)

// ConfigFile is the watcher's config, tracked at the repository root and read
// from origin/<base> (spec §3.1).
const ConfigFile = "loom-watch.json"

// maxConfigBytes is the most loom-watch.json may hold (spec §7.1).
const maxConfigBytes = 64 << 10

// Config is loom-watch.json after ParseConfig: defaults applied, bounds
// checked. Durations are Go duration strings in the file.
type Config struct {
	Version       int
	Label         string
	EscalateLabel string
	Title         string
	Cap           int
	Stuck         time.Duration
	NudgeLimit    int
	NudgeGap      time.Duration
	IssueChanged  bool
	LogTail       int
	Session       SessionConfig
	Hook          *HookConfig
}

// SessionConfig is the `session` object: how a watcher session starts.
// Prompt and ResumePrompt are paths in the repository at origin/<base>.
type SessionConfig struct {
	Model          string
	Effort         string
	PermissionMode string
	Account        string
	Prompt         string
	ResumePrompt   string
}

// HookConfig is the optional `hook` object: the command (program first), run
// in the watcher checkout, and its per-call timeout.
type HookConfig struct {
	Command []string
	Timeout time.Duration
}

// rawConfig is the file's shape; a pointer tells an absent key from a zero.
type rawConfig struct {
	Version       *int        `json:"version"`
	Label         *string     `json:"label"`
	EscalateLabel *string     `json:"escalate_label"`
	Title         *string     `json:"title"`
	Cap           *int        `json:"cap"`
	Stuck         *string     `json:"stuck"`
	NudgeLimit    *int        `json:"nudge_limit"`
	NudgeGap      *string     `json:"nudge_gap"`
	IssueChanged  *bool       `json:"issue_changed"`
	LogTail       *int        `json:"log_tail"`
	Session       *rawSession `json:"session"`
	Hook          *rawHook    `json:"hook"`
}

type rawSession struct {
	Model          string `json:"model"`
	Effort         string `json:"effort"`
	PermissionMode string `json:"permission_mode"`
	Account        string `json:"account"`
	Prompt         string `json:"prompt"`
	ResumePrompt   string `json:"resume_prompt"`
}

type rawHook struct {
	Command []string `json:"command"`
	Timeout *string  `json:"timeout"`
}

// ParseConfig reads loom-watch.json: one JSON object of at most 64 KiB with
// no unknown key. It applies the defaults and the bounds of spec §3.1 and
// §7.1. Any fault is an error naming the key, and the Config is then zero:
// the watcher does nothing for a workspace whose file is invalid.
func ParseConfig(data []byte) (Config, error) {
	if len(data) > maxConfigBytes {
		return Config{}, fmt.Errorf("%s is %d bytes, the limit is %d", ConfigFile, len(data), maxConfigBytes)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return Config{}, fmt.Errorf("%s is empty", ConfigFile)
	}
	var raw rawConfig
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("%s: %w", ConfigFile, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("%s: unexpected data after the JSON object", ConfigFile)
	}
	cfg, err := buildConfig(raw)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func buildConfig(raw rawConfig) (Config, error) {
	var cfg Config
	var err error
	if raw.Version == nil {
		return Config{}, errors.New("version: required")
	}
	if *raw.Version != 1 {
		return Config{}, fmt.Errorf("version: %d is not supported, want 1", *raw.Version)
	}
	cfg.Version = 1
	if cfg.Label, err = labelValue("label", raw.Label); err != nil {
		return Config{}, err
	}
	if cfg.EscalateLabel, err = labelValue("escalate_label", raw.EscalateLabel); err != nil {
		return Config{}, err
	}
	if cfg.Label == cfg.EscalateLabel {
		return Config{}, fmt.Errorf("escalate_label: %q is also label, so every issue the watcher lists would be hands-off", cfg.Label)
	}
	if raw.Title == nil || *raw.Title == "" {
		return Config{}, errors.New("title: required")
	}
	if _, err = ParseTitleTemplate(*raw.Title); err != nil {
		return Config{}, fmt.Errorf("title: %w", err)
	}
	cfg.Title = *raw.Title
	if cfg.Cap, err = boundedInt("cap", raw.Cap, 2, 1, 10); err != nil {
		return Config{}, err
	}
	if cfg.Stuck, err = boundedDuration("stuck", raw.Stuck, 2*time.Hour, 10*time.Minute, 48*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.NudgeLimit, err = boundedInt("nudge_limit", raw.NudgeLimit, 3, 1, 10); err != nil {
		return Config{}, err
	}
	if cfg.NudgeGap, err = boundedDuration("nudge_gap", raw.NudgeGap, time.Hour, time.Minute, 24*time.Hour); err != nil {
		return Config{}, err
	}
	cfg.IssueChanged = true
	if raw.IssueChanged != nil {
		cfg.IssueChanged = *raw.IssueChanged
	}
	if cfg.LogTail, err = boundedInt("log_tail", raw.LogTail, 200, 1, 1000); err != nil {
		return Config{}, err
	}
	if cfg.Session, err = buildSession(raw.Session); err != nil {
		return Config{}, err
	}
	if raw.Hook != nil {
		if cfg.Hook, err = buildHook(raw.Hook); err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

func buildSession(raw *rawSession) (SessionConfig, error) {
	if raw == nil {
		return SessionConfig{}, errors.New("session.prompt: required")
	}
	s := SessionConfig(*raw)
	if err := oneOf("session.model", s.Model, config.ClaudeModelAliases()); err != nil {
		return SessionConfig{}, err
	}
	if err := oneOf("session.effort", s.Effort, config.ClaudeEfforts); err != nil {
		return SessionConfig{}, err
	}
	if err := oneOf("session.permission_mode", s.PermissionMode, config.ClaudePermissionModes); err != nil {
		return SessionConfig{}, err
	}
	if s.Prompt == "" {
		return SessionConfig{}, errors.New("session.prompt: required")
	}
	if err := repoPath("session.prompt", s.Prompt); err != nil {
		return SessionConfig{}, err
	}
	if s.ResumePrompt == "" {
		s.ResumePrompt = s.Prompt
	} else if err := repoPath("session.resume_prompt", s.ResumePrompt); err != nil {
		return SessionConfig{}, err
	}
	return s, nil
}

func buildHook(raw *rawHook) (*HookConfig, error) {
	if n := len(raw.Command); n < 1 || n > 32 {
		return nil, fmt.Errorf("hook.command: want 1 to 32 strings, got %d", n)
	}
	if raw.Command[0] == "" {
		return nil, errors.New("hook.command: the first string, the program, is empty")
	}
	for _, s := range raw.Command {
		if strings.ContainsRune(s, 0) {
			return nil, errors.New("hook.command: a string holds a NUL byte")
		}
	}
	timeout, err := boundedDuration("hook.timeout", raw.Timeout, time.Minute, time.Second, 300*time.Second)
	if err != nil {
		return nil, err
	}
	return &HookConfig{Command: slices.Clone(raw.Command), Timeout: timeout}, nil
}

// labelValue checks a required GitHub label name: present, and no surrounding
// space or control character.
func labelValue(key string, p *string) (string, error) {
	if p == nil || *p == "" {
		return "", fmt.Errorf("%s: required", key)
	}
	if strings.TrimSpace(*p) != *p || strings.ContainsFunc(*p, unicode.IsControl) {
		return "", fmt.Errorf("%s: %q has surrounding space or a control character", key, *p)
	}
	return *p, nil
}

func boundedInt(key string, p *int, def, lo, hi int) (int, error) {
	if p == nil {
		return def, nil
	}
	if *p < lo || *p > hi {
		return 0, fmt.Errorf("%s: %d is outside %d to %d", key, *p, lo, hi)
	}
	return *p, nil
}

func boundedDuration(key string, p *string, def, lo, hi time.Duration) (time.Duration, error) {
	if p == nil {
		return def, nil
	}
	d, err := time.ParseDuration(*p)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a duration such as \"90s\" or \"2h\"", key, *p)
	}
	if d < lo || d > hi {
		return 0, fmt.Errorf("%s: %s is outside %s to %s", key, d, lo, hi)
	}
	return d, nil
}

// oneOf accepts an empty value (the key's default) or a member of allowed.
func oneOf(key, value string, allowed []string) error {
	if value == "" || slices.Contains(allowed, value) {
		return nil
	}
	return fmt.Errorf("%s: %q is not one of %s", key, value, strings.Join(allowed, ", "))
}

// repoPath accepts a clean relative path inside the repository, the form
// `git show origin/<base>:<path>` reads.
func repoPath(key, p string) error {
	if path.Clean(p) != p || path.IsAbs(p) || p == "." || p == ".." || strings.HasPrefix(p, "../") ||
		strings.ContainsFunc(p, unicode.IsControl) {
		return fmt.Errorf("%s: %q is not a clean relative path inside the repository", key, p)
	}
	return nil
}
