// Package agent consolidates program-specific knowledge for the
// supported coding agents (claude, aider, gemini, etc.).
//
// Before this package, per-agent logic was scattered: recovery flag
// assembly in session/agent_restart.go, trust-prompt strings in
// session/tmux/tmux.go, and "is this a known program?" checks inline
// at call sites. Adapter consolidates those so that adding a new
// agent is a one-file change.
package agent

import (
	"path/filepath"
	"strings"
)

// TrustPromptAction describes how to dismiss a trust/permission prompt.
// Each agent has its own expected response sequence.
type TrustPromptAction int

const (
	// TrustPromptNone means no trust-prompt handling is needed.
	TrustPromptNone TrustPromptAction = iota
	// TrustPromptTapEnter responds with a single Enter keypress.
	TrustPromptTapEnter
	// TrustPromptTapDAndEnter responds with "d" followed by Enter — used by
	// agents that offer a "don't ask again" option on their trust screen.
	TrustPromptTapDAndEnter
)

// Adapter describes a single supported agent's behavior: how to detect
// its prompts, how to dismiss them, and how to restart it.
//
// The adapter is consulted from Instance and tmux code paths that
// previously inlined these strings. To add a new agent, implement this
// interface and register it in DefaultRegistry.
type Adapter interface {
	// Name returns a stable short identifier (e.g. "claude").
	Name() string
	// Matches reports whether the given program command string is this
	// adapter's agent. Typically checks the basename of the first
	// whitespace-separated token.
	Matches(program string) bool
	// TrustPromptPatterns returns substrings that identify a trust
	// prompt for this agent. If any pattern is present in the pane
	// content, TrustPromptResponse() tells us how to respond.
	TrustPromptPatterns() []string
	// TrustPromptResponse tells us how to dismiss the trust prompt when
	// one is detected.
	TrustPromptResponse() TrustPromptAction
	// PendingPromptPattern returns the substring that indicates the
	// agent is blocked waiting for the user to answer a prompt. An
	// empty string disables auto-yes detection for this agent.
	PendingPromptPattern() string
	// ApplyRecoveryFlag returns the program string with recovery flags
	// appended (e.g. "claude --continue"). Idempotent: if the flag is
	// already present, the input is returned unchanged. Returns the
	// input unchanged if the adapter does not support recovery.
	ApplyRecoveryFlag(program string) string
}

// Registry is a prioritized list of adapters. Lookup returns the first
// Matches hit, or the fallback adapter if nothing matches. The
// fallback's Matches() is never consulted.
type Registry struct {
	adapters []Adapter
	fallback Adapter
}

// NewRegistry builds a registry. fallback receives unknown programs
// and must implement Adapter with safe no-op behavior.
func NewRegistry(fallback Adapter, adapters ...Adapter) *Registry {
	return &Registry{adapters: adapters, fallback: fallback}
}

// Lookup returns the first adapter whose Matches() returns true, or
// the fallback.
func (r *Registry) Lookup(program string) Adapter {
	for _, a := range r.adapters {
		if a.Matches(program) {
			return a
		}
	}
	return r.fallback
}

// firstField returns the first whitespace-separated token of program,
// or the empty string if program is all whitespace.
func firstField(program string) string {
	parts := strings.Fields(program)
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// basenameMatch reports whether the program's first token has the
// given basename (after path stripping). Absolute paths like
// /nix/store/.../bin/claude still match "claude".
func basenameMatch(program, name string) bool {
	first := firstField(program)
	if first == "" {
		return false
	}
	return filepath.Base(first) == name
}
