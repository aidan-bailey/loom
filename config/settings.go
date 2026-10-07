package config

import "slices"

// Settings is config.json's content: every persisted field, with the
// getters that read them. A plain value: the model hands copies of it
// across the boundary (core.WorkspaceView.Settings), and the TUI's
// settings overlay edits its own Config built from one (FromSettings).
// Config embeds it beside the mutex that guards the live, shared copy;
// JSON flattens the embedded struct, so config.json's format is the one
// Config always had.
type Settings struct {
	// DefaultProgram is the default program to run in new instances
	DefaultProgram string `json:"default_program"`
	// BranchPrefix is the prefix used for git branches created by the application.
	BranchPrefix string `json:"branch_prefix"`
	// BaseBranch names the branch new session worktrees are cut from.
	// Empty means auto-detect (origin/HEAD, then main, then master, then
	// whatever the root repo currently has checked out) — see
	// git.ResolveBaseCommit. DefaultConfig deliberately leaves it empty:
	// unlike BranchPrefix there is no sensible universal literal, and
	// auto-detect is correct for main/master/develop repos alike.
	// Read through GetBaseBranch.
	BaseBranch string `json:"base_branch,omitempty"`
	// Profiles is a list of named program profiles.
	Profiles []Profile `json:"profiles,omitempty"`
	// ClaudeRemoteControl controls whether new Claude sessions launch
	// with `--remote-control` (named after the session title). It is a
	// pointer so a config file predating this field (nil) is treated as
	// enabled rather than taking the bool zero value; only an explicit
	// false disables it. Read it through RemoteControlEnabled.
	ClaudeRemoteControl *bool `json:"claude_remote_control,omitempty"`
	// ClaudeLoomContext controls whether new Claude sessions launch with
	// --append-system-prompt-file pointing at loom's embedded context
	// file (see session.WriteLoomContextFiles). nil is treated as enabled
	// (read via LoomContextEnabled), matching ClaudeRemoteControl. A no-op
	// for agents other than Claude.
	ClaudeLoomContext *bool `json:"claude_loom_context,omitempty"`
	// ClaudeSubagentTracking controls whether the subagents and teammates
	// loom's hooks track are shown, as a count on rail cards and as rows
	// on overview cards (see session/subagent). Every Claude launch gets
	// the hooks regardless, since they also carry status, the session ID
	// and the last message. nil is treated as enabled (read via
	// SubagentTrackingEnabled), matching ClaudeLoomContext. Takes effect
	// at once.
	ClaudeSubagentTracking *bool `json:"claude_subagent_tracking,omitempty"`
	// ClaudePermissionMode is the --permission-mode value new Claude
	// sessions launch with. Unlike ClaudeRemoteControl, DefaultConfig
	// sets this explicitly to "default" rather than leaving it nil — nil
	// only occurs for a config.json predating this field, and is
	// treated identically to "default" (no flag injected; Claude's own
	// default applies). Read it through PermissionMode.
	ClaudePermissionMode *string `json:"claude_permission_mode,omitempty"`
	// Theme names the active UI color theme (see ui.ThemeNames).
	// Empty (pre-existing config files) means the default theme.
	// Read through GetTheme.
	Theme string `json:"theme,omitempty"`
	// ClaudeTmpArchiveDir relocates the zips loom archives Claude's
	// per-session temp dirs into (session.ClaudeTmpArchiveDir): every
	// workspace's archives go under it, each workspace in its own
	// subfolder. Read from the global config.json only. Empty (the
	// default) keeps each workspace's archives in its own loom config
	// folder. Absolute, or starting with ~. Read through
	// ClaudeTmpArchiveRoot.
	ClaudeTmpArchiveDir string `json:"claude_tmp_archive_dir,omitempty"`
	// HeadroomProxy controls whether new Claude sessions launch with
	// ANTHROPIC_BASE_URL pointed at Headroom's proxy (see
	// session.HeadroomProxyEnv). A no-op for agents other than Claude.
	// Loom does not start or manage the headroom proxy process itself —
	// the user is expected to have it running separately. Defaults to
	// off (DefaultConfig sets it explicitly to false) since it's
	// opt-in. Mutually exclusive with ClaudeRemoteControl: enabling one
	// disables the other, enforced in the Claude Preferences toggle
	// handler, the Session Launch Options modal, and defensively again
	// in launch.Compose so a hand-edited config.json with both
	// fields true still can't launch both at once. Read it through
	// HeadroomProxyEnabled.
	HeadroomProxy *bool `json:"headroom_proxy,omitempty"`
	// ClaudeModel is the --model value new Claude sessions launch with.
	// Values are short CLI aliases (not versioned IDs) so the list
	// stays valid as new models ship without a code change. "default"
	// is a no-op — Claude's own default applies. Read it through Model.
	ClaudeModel *string `json:"claude_model,omitempty"`
	// ClaudeEffort is the --effort value new Claude sessions launch
	// with. "default" is a no-op — Claude's own default applies. Read
	// it through Effort.
	ClaudeEffort *string `json:"claude_effort,omitempty"`
	// CacheTTL1h controls whether new Claude sessions launch with
	// ENABLE_PROMPT_CACHING_1H=1 (see session.CacheTTL1hEnv), extending
	// Claude's prompt cache from the default 5-minute TTL to 1 hour. A
	// no-op for agents other than Claude. Defaults to off (DefaultConfig
	// sets it explicitly to false) since it's opt-in. Read it through
	// CacheTTL1hEnabled.
	CacheTTL1h *bool `json:"cache_ttl_1h,omitempty"`
	// Claude1MContext controls whether new Claude sessions launch with
	// the [1m] long-context suffix appended to their --model alias
	// (e.g. "sonnet[1m]"). A no-op for agents other than Claude, and
	// for aliases that don't accept the suffix (see
	// ClaudeModelSupports1M). Defaults to off (DefaultConfig sets it
	// explicitly to false) since it's opt-in. Read it through
	// Context1MEnabled.
	Claude1MContext *bool `json:"claude_1m_context,omitempty"`
}

// Clone deep-copies s: Profiles and every pointer field are its own.
func (s Settings) Clone() Settings {
	out := s
	out.Profiles = slices.Clone(s.Profiles)
	out.ClaudeRemoteControl = clonePtr(s.ClaudeRemoteControl)
	out.ClaudeLoomContext = clonePtr(s.ClaudeLoomContext)
	out.ClaudeSubagentTracking = clonePtr(s.ClaudeSubagentTracking)
	out.ClaudePermissionMode = clonePtr(s.ClaudePermissionMode)
	out.HeadroomProxy = clonePtr(s.HeadroomProxy)
	out.ClaudeModel = clonePtr(s.ClaudeModel)
	out.ClaudeEffort = clonePtr(s.ClaudeEffort)
	out.CacheTTL1h = clonePtr(s.CacheTTL1h)
	out.Claude1MContext = clonePtr(s.Claude1MContext)
	return out
}

// clonePtr returns a pointer to a copy of *p, or nil.
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// FromSettings builds a Config holding a copy of s: a new object with its
// own lock, for a caller that edits settings without touching anyone
// else's (the TUI's settings overlay).
func FromSettings(s Settings) *Config { return &Config{Settings: s.Clone()} }

// Snapshot is a deep copy of the settings, taken under the lock.
func (c *Config) Snapshot() Settings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Settings.Clone()
}

// ReplaceSettings replaces every setting with a copy of s, under the lock.
func (c *Config) ReplaceSettings(s Settings) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Settings = s.Clone()
}

// GetBranchPrefix returns BranchPrefix. Config's own GetBranchPrefix reads
// it under the lock.
func (s Settings) GetBranchPrefix() string { return s.BranchPrefix }

// GetBaseBranch returns BaseBranch (see Config.GetBaseBranch).
func (s Settings) GetBaseBranch() string { return s.BaseBranch }

// GetTheme returns the configured UI theme name (see Config.GetTheme).
func (s Settings) GetTheme() string { return s.Theme }

// ClaudeTmpArchiveRoot returns ClaudeTmpArchiveDir with a leading ~
// expanded, or "" when it is unset. A value that is not absolute after
// expansion is an error: the caller keeps the default location rather than
// archive relative to whatever directory loom runs in.
func (s Settings) ClaudeTmpArchiveRoot() (string, error) {
	if s.ClaudeTmpArchiveDir == "" {
		return "", nil
	}
	return resolveEnvDir("claude_tmp_archive_dir", s.ClaudeTmpArchiveDir)
}

// RemoteControlEnabled reports whether new Claude sessions should launch
// with the --remote-control flag. Defaults to true when unset so the
// feature is on out of the box; only an explicit false disables it.
func (s Settings) RemoteControlEnabled() bool {
	return s.ClaudeRemoteControl == nil || *s.ClaudeRemoteControl
}

// LoomContextEnabled reports whether new Claude sessions should launch
// with loom's context file injected. nil (unset) is treated as enabled,
// mirroring RemoteControlEnabled. Read only from the main goroutine.
func (s Settings) LoomContextEnabled() bool {
	return s.ClaudeLoomContext == nil || *s.ClaudeLoomContext
}

// SubagentTrackingEnabled reports whether the subagents loom's hooks track
// are shown. nil (unset) is treated as enabled, mirroring
// LoomContextEnabled. Read only from the main goroutine.
func (s Settings) SubagentTrackingEnabled() bool {
	return s.ClaudeSubagentTracking == nil || *s.ClaudeSubagentTracking
}

// PermissionMode returns the configured --permission-mode value,
// defaulting to "default" when unset (nil). Deliberately unlocked, like
// RemoteControlEnabled: it's read only from the main goroutine (view
// rendering, instance creation during key handling, and from inside a
// Mutate callback in the Claude Preferences cycle handler — a
// Mutate-held lock is not reentrant, so a locked accessor here would
// deadlock). If a future caller needs this from the Lua dispatch
// goroutine, add a locked variant rather than locking this one.
func (s Settings) PermissionMode() string {
	if s.ClaudePermissionMode == nil {
		return "default"
	}
	return *s.ClaudePermissionMode
}

// HeadroomProxyEnabled reports whether new Claude sessions should
// launch with ANTHROPIC_BASE_URL pointed at Headroom's proxy. Defaults
// to false when unset.
func (s Settings) HeadroomProxyEnabled() bool {
	return s.HeadroomProxy != nil && *s.HeadroomProxy
}

// Model returns the configured --model alias, defaulting to "default"
// when unset (nil). Unlocked for the same reason as PermissionMode.
func (s Settings) Model() string {
	if s.ClaudeModel == nil {
		return "default"
	}
	return *s.ClaudeModel
}

// Effort returns the configured --effort value, defaulting to
// "default" when unset. Unlocked for the same reason as
// PermissionMode/Model.
func (s Settings) Effort() string {
	if s.ClaudeEffort == nil {
		return "default"
	}
	return *s.ClaudeEffort
}

// CacheTTL1hEnabled reports whether new Claude sessions should launch
// with ENABLE_PROMPT_CACHING_1H=1. Defaults to false when unset.
func (s Settings) CacheTTL1hEnabled() bool {
	return s.CacheTTL1h != nil && *s.CacheTTL1h
}

// Context1MEnabled reports whether new Claude sessions should launch
// with the [1m] long-context suffix on their --model alias. Defaults to
// false when unset. Unlocked for the same reason as PermissionMode.
func (s Settings) Context1MEnabled() bool {
	return s.Claude1MContext != nil && *s.Claude1MContext
}

// GetProgram returns the program to run. If Profiles is non-empty and
// DefaultProgram matches a profile name, that profile's Program is returned.
// Otherwise DefaultProgram is returned as-is.
func (s Settings) GetProgram() string {
	for _, p := range s.Profiles {
		if p.Name == s.DefaultProgram {
			return p.Program
		}
	}
	return s.DefaultProgram
}

// GetProfiles returns a unified list of profiles. If Profiles is defined,
// those are returned with the default profile first. Otherwise, a single
// profile is synthesized from DefaultProgram.
func (s Settings) GetProfiles() []Profile {
	if len(s.Profiles) == 0 {
		return []Profile{{Name: s.DefaultProgram, Program: s.DefaultProgram}}
	}
	// Reorder so the default profile comes first.
	profiles := make([]Profile, 0, len(s.Profiles))
	for _, p := range s.Profiles {
		if p.Name == s.DefaultProgram {
			profiles = append(profiles, p)
			break
		}
	}
	for _, p := range s.Profiles {
		if p.Name != s.DefaultProgram {
			profiles = append(profiles, p)
		}
	}
	return profiles
}
