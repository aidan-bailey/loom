package overlay

import (
	"github.com/aidan-bailey/loom/config"

	tea "charm.land/bubbletea/v2"
)

// ClaudePreferences is fleshed out in Task 6.
type ClaudePreferences struct {
	cfg         *config.Config
	authBlocked bool
	authReason  string
	width       int
}

func NewClaudePreferences(cfg *config.Config, authBlocked bool, authReason string) *ClaudePreferences {
	return &ClaudePreferences{cfg: cfg, authBlocked: authBlocked, authReason: authReason}
}
func (c *ClaudePreferences) SetWidth(w int) { c.width = w }
func (c *ClaudePreferences) Render() string { return "" }
func (c *ClaudePreferences) HandleKeyPress(msg tea.KeyPressMsg) (closed, changed bool) {
	if s := msg.String(); s == "esc" || s == "q" {
		return true, false
	}
	return false, false
}
