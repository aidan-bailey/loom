package overlay

import (
	"fmt"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type profilesMode int

const (
	profilesBrowsing profilesMode = iota
	profilesAddingName
	profilesAddingProgram
	profilesEditingProgram
	profilesConfirmingDelete
)

// ProfilesManager is the Profiles drill-in sub-screen: add/edit-program/
// delete/set-default over cfg.Profiles. It operates on the raw
// cfg.Profiles slice directly (not config.Config.GetProfiles(), which
// synthesizes a row from DefaultProgram when Profiles is empty) — the
// Default Program row on the parent SettingsOverlay is the single
// source of truth for "what runs by default"; this screen only manages
// the named list. Renaming an existing profile isn't supported in v1 —
// only its program string — since that's the field users actually need
// to tweak (e.g. a model flag).
type ProfilesManager struct {
	cfg    *config.Config
	cursor int
	width  int

	mode        profilesMode
	input       *TextInputOverlay
	pendingName string

	lastErr error
}

// NewProfilesManager creates the Profiles sub-screen over cfg.
func NewProfilesManager(cfg *config.Config) *ProfilesManager {
	return &ProfilesManager{cfg: cfg, width: 60}
}

// SetWidth propagates the available width to any embedded text input.
func (p *ProfilesManager) SetWidth(w int) {
	p.width = w
	if p.input != nil {
		p.input.SetSize(w, 3)
	}
}

// TakeError returns and clears the last validation/precondition error
// (e.g. deleting the default profile). Callers poll this after
// HandleKeyPress.
func (p *ProfilesManager) TakeError() error {
	err := p.lastErr
	p.lastErr = nil
	return err
}

// HandleKeyPress processes one key press. closed reports whether the
// sub-screen should return control to the parent SettingsOverlay;
// changed reports whether cfg was mutated.
func (p *ProfilesManager) HandleKeyPress(msg tea.KeyPressMsg) (closed, changed bool) {
	p.lastErr = nil
	switch p.mode {
	case profilesAddingName:
		return p.handleAddingName(msg)
	case profilesAddingProgram:
		return p.handleAddingProgram(msg)
	case profilesEditingProgram:
		return p.handleEditingProgram(msg)
	case profilesConfirmingDelete:
		return p.handleConfirmingDelete(msg)
	}

	switch msg.String() {
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if len(p.cfg.Profiles) > 0 && p.cursor < len(p.cfg.Profiles)-1 {
			p.cursor++
		}
	case "esc", "q":
		return true, false
	case "n":
		p.mode = profilesAddingName
		p.input = NewTextInputOverlay("Profile name", "")
		p.input.SetSize(p.width, 3)
	case "e", "enter":
		if len(p.cfg.Profiles) == 0 {
			return false, false
		}
		p.mode = profilesEditingProgram
		p.input = NewTextInputOverlay("Program command", p.cfg.Profiles[p.cursor].Program)
		p.input.SetSize(p.width, 3)
	case " ", "space":
		if len(p.cfg.Profiles) == 0 {
			return false, false
		}
		name := p.cfg.Profiles[p.cursor].Name
		p.cfg.Mutate(func(c *config.Config) { c.DefaultProgram = name })
		return false, true
	case "d":
		if len(p.cfg.Profiles) == 0 {
			return false, false
		}
		if p.cfg.Profiles[p.cursor].Name == p.cfg.DefaultProgram {
			p.lastErr = fmt.Errorf("can't delete %q: it's the current Default Program — change that first", p.cfg.Profiles[p.cursor].Name)
			return false, false
		}
		p.mode = profilesConfirmingDelete
	}
	return false, false
}

// handleAddingName/handleAddingProgram/handleEditingProgram own Enter/Esc
// directly rather than depending on TextInputOverlay's Tab-to-submit
// convention — see SettingsOverlay.handleEditingText's doc comment for
// why: Enter on a freshly-focused textarea inserts a newline instead of
// submitting unless focus was first Tabbed to the Enter button, which is
// the wrong UX for these single-line fields.
func (p *ProfilesManager) handleAddingName(msg tea.KeyPressMsg) (closed, changed bool) {
	switch msg.Code {
	case tea.KeyEnter:
		p.pendingName = p.input.GetValue()
		p.mode = profilesAddingProgram
		p.input = NewTextInputOverlay("Program command", "")
		p.input.SetSize(p.width, 3)
		return false, false
	case tea.KeyEsc:
		p.mode = profilesBrowsing
		p.input = nil
		return false, false
	}
	p.input.HandleKeyPress(msg)
	return false, false
}

func (p *ProfilesManager) handleAddingProgram(msg tea.KeyPressMsg) (closed, changed bool) {
	switch msg.Code {
	case tea.KeyEnter:
		program := p.input.GetValue()
		name := p.pendingName
		p.mode = profilesBrowsing
		p.input = nil
		p.pendingName = ""
		p.cfg.Mutate(func(c *config.Config) {
			c.Profiles = append(c.Profiles, config.Profile{Name: name, Program: program})
		})
		p.cursor = len(p.cfg.Profiles) - 1
		return false, true
	case tea.KeyEsc:
		p.mode = profilesBrowsing
		p.input = nil
		p.pendingName = ""
		return false, false
	}
	p.input.HandleKeyPress(msg)
	return false, false
}

func (p *ProfilesManager) handleEditingProgram(msg tea.KeyPressMsg) (closed, changed bool) {
	switch msg.Code {
	case tea.KeyEnter:
		program := p.input.GetValue()
		idx := p.cursor
		p.mode = profilesBrowsing
		p.input = nil
		p.cfg.Mutate(func(c *config.Config) { c.Profiles[idx].Program = program })
		return false, true
	case tea.KeyEsc:
		p.mode = profilesBrowsing
		p.input = nil
		return false, false
	}
	p.input.HandleKeyPress(msg)
	return false, false
}

func (p *ProfilesManager) handleConfirmingDelete(msg tea.KeyPressMsg) (closed, changed bool) {
	switch msg.String() {
	case "y":
		idx := p.cursor
		p.cfg.Mutate(func(c *config.Config) {
			c.Profiles = append(c.Profiles[:idx], c.Profiles[idx+1:]...)
		})
		if p.cursor >= len(p.cfg.Profiles) && p.cursor > 0 {
			p.cursor--
		}
		p.mode = profilesBrowsing
		return false, true
	case "n", "esc":
		p.mode = profilesBrowsing
		return false, false
	}
	return false, false
}

var (
	profilesTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(ui.TitleAccent)
	profilesSelectedStyle = lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	profilesNormalStyle   = lipgloss.NewStyle().Foreground(ui.TextPrimary)
	profilesHintStyle     = lipgloss.NewStyle().Foreground(ui.TextHint)
)

// Render renders whichever mode is active.
func (p *ProfilesManager) Render() string {
	switch p.mode {
	case profilesAddingName, profilesAddingProgram, profilesEditingProgram:
		return p.input.Render()
	}

	content := profilesTitleStyle.Render("Profiles") + "\n\n"
	if len(p.cfg.Profiles) == 0 {
		content += profilesNormalStyle.Render("No profiles yet — press 'n' to add one") + "\n"
	}
	for i, prof := range p.cfg.Profiles {
		cursor := "  "
		if i == p.cursor {
			cursor = "> "
		}
		mark := "  "
		if prof.Name == p.cfg.DefaultProgram {
			mark = "* "
		}
		line := fmt.Sprintf("%s%s%-16s %s", cursor, mark, prof.Name, prof.Program)
		if i == p.cursor {
			content += profilesSelectedStyle.Render(line) + "\n"
		} else {
			content += profilesNormalStyle.Render(line) + "\n"
		}
	}
	if p.mode == profilesConfirmingDelete {
		content += "\n" + profilesHintStyle.Render(fmt.Sprintf("Delete %q? y/n", p.cfg.Profiles[p.cursor].Name))
	} else {
		content += "\n" + profilesHintStyle.Render("n add • e/enter edit • d delete • space set default • esc back")
	}

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.TitleAccent).
		Padding(1, 2).
		Width(p.width)
	return border.Render(content)
}
