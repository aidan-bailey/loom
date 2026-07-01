package overlay

import (
	"github.com/aidan-bailey/loom/config"

	tea "charm.land/bubbletea/v2"
)

// ProfilesManager is fleshed out in Task 5.
type ProfilesManager struct {
	cfg   *config.Config
	width int
}

func NewProfilesManager(cfg *config.Config) *ProfilesManager { return &ProfilesManager{cfg: cfg} }
func (p *ProfilesManager) SetWidth(w int)                    { p.width = w }
func (p *ProfilesManager) Render() string                    { return "" }
func (p *ProfilesManager) HandleKeyPress(msg tea.KeyPressMsg) (closed, changed bool) {
	if s := msg.String(); s == "esc" || s == "q" {
		return true, false
	}
	return false, false
}
