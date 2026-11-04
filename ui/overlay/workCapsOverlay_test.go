package overlay

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

func capsKey(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+u":
		return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func newCaps(set WorkCapsValues) *WorkCapsOverlay {
	host := WorkCapsHost{Name: "fresco", Cores: 16, RAMBytes: 30 << 30}
	defaults := WorkCapsValues{CPUs: 14, MemoryGB: 22, IOJobs: 2, ClaimCPUs: 4, ClaimMemoryGB: 6, StarveMinutes: 10, KeepFinished: 50}
	return NewWorkCapsOverlay(host, defaults, set, "")
}

func pressCaps(o *WorkCapsOverlay, keys ...string) {
	for _, k := range keys {
		o.HandleKeyPress(capsKey(k))
	}
}

func capsLine(t *testing.T, out, sub string) string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	t.Fatalf("no line holds %q in:\n%s", sub, out)
	return ""
}

func TestWorkCapsOverlay_StartsFromTheConfiguredValues(t *testing.T) {
	set := WorkCapsValues{CPUs: 8, ClaimMemoryGB: 4}
	o := newCaps(set)
	assert.Equal(t, set, o.Values())

	out := ansi.Strip(o.Render())
	assert.Contains(t, out, "Work queue caps · fresco")
	assert.Contains(t, out, "this host: 16 cores, 30.0 GB RAM")
	cpus := capsLine(t, out, "CPUs")
	assert.Contains(t, cpus, "8")
	assert.Contains(t, cpus, "set · host default 14")
	mem := capsLine(t, out, "Memory (GB)")
	assert.Contains(t, mem, "22")
	assert.Contains(t, mem, "host default")
	assert.NotContains(t, mem, "set", "a field left alone is the host's default")
	claim := capsLine(t, out, "Default claim GB")
	assert.Contains(t, claim, "set · host default 6")
}

func TestWorkCapsOverlay_AHostThatCouldNotReadItsRAM(t *testing.T) {
	host := WorkCapsHost{Name: "odd", Cores: 8}
	o := NewWorkCapsOverlay(host, WorkCapsValues{CPUs: 6, IOJobs: 2}, WorkCapsValues{}, "")
	out := ansi.Strip(o.Render())
	assert.Contains(t, out, "this host: 8 cores, RAM could not be read")
	mem := capsLine(t, out, "Memory (GB)")
	assert.Contains(t, mem, "?")
	assert.Contains(t, mem, "no host default")
}

func TestWorkCapsOverlay_DigitsEditTheFieldUnderTheCursor(t *testing.T) {
	o := newCaps(WorkCapsValues{})
	pressCaps(o, "1", "2")
	assert.Equal(t, WorkCapsValues{CPUs: 12}, o.Values())
	pressCaps(o, "down", "3")
	assert.Equal(t, WorkCapsValues{CPUs: 12, MemoryGB: 3}, o.Values())
	pressCaps(o, "tab", "tab", "tab", "9")
	assert.Equal(t, WorkCapsValues{CPUs: 12, MemoryGB: 3, ClaimMemoryGB: 9}, o.Values())
	pressCaps(o, "shift+tab", "up", "up", "7") // claim CPUs, I/O jobs, memory: 7 follows the 3
	assert.Equal(t, WorkCapsValues{CPUs: 12, MemoryGB: 37, ClaimMemoryGB: 9}, o.Values())
}

func TestWorkCapsOverlay_TheCursorStopsAtBothEnds(t *testing.T) {
	o := newCaps(WorkCapsValues{})
	pressCaps(o, "up", "up")
	assert.Equal(t, workCapsCPUs, o.cursor)
	for i := 0; i < 20; i++ {
		pressCaps(o, "down")
	}
	assert.Equal(t, workCapsKeepFinished, o.cursor)
}

func TestWorkCapsOverlay_BackspaceAndCtrlUReturnAFieldToItsDefault(t *testing.T) {
	o := newCaps(WorkCapsValues{CPUs: 123})
	pressCaps(o, "backspace")
	assert.Equal(t, 12, o.Values().CPUs)
	pressCaps(o, "backspace", "backspace", "backspace")
	assert.Equal(t, 0, o.Values().CPUs, "an empty field is the host default")
	pressCaps(o, "5", "ctrl+u")
	assert.Equal(t, 0, o.Values().CPUs)
	assert.Contains(t, capsLine(t, ansi.Strip(o.Render()), "CPUs"), "host default")
}

func TestWorkCapsOverlay_OnlyDigitsAreTyped(t *testing.T) {
	o := newCaps(WorkCapsValues{})
	pressCaps(o, "a", "-", " ", ".", "x", "5")
	assert.Equal(t, WorkCapsValues{CPUs: 5}, o.Values())
}

func TestWorkCapsOverlay_LeadingZerosAndLength(t *testing.T) {
	o := newCaps(WorkCapsValues{})
	pressCaps(o, "0", "7")
	assert.Equal(t, 7, o.Values().CPUs, "a zero is replaced by the next digit")
	pressCaps(o, "ctrl+u", "0", "0")
	assert.Equal(t, "0", o.buf[workCapsCPUs], "zeros do not pile up")
	pressCaps(o, "ctrl+u", "1", "2", "3", "4", "5", "6", "7", "8", "9", "1", "1")
	assert.Equal(t, 123456789, o.Values().CPUs, "nine digits at most")
}

func TestWorkCapsOverlay_EnterSavesUnlessASaveIsInFlight(t *testing.T) {
	o := newCaps(WorkCapsValues{})
	closed, submit := o.HandleKeyPress(capsKey("enter"))
	assert.False(t, closed)
	assert.True(t, submit)

	o.SetSaving(true)
	assert.True(t, o.Saving())
	closed, submit = o.HandleKeyPress(capsKey("enter"))
	assert.False(t, closed)
	assert.False(t, submit, "a save in flight is not sent twice")
	pressCaps(o, "5")
	assert.Equal(t, WorkCapsValues{}, o.Values(), "the form is held while it saves")
	assert.Contains(t, ansi.Strip(o.Render()), "saving…")

	closed, submit = o.HandleKeyPress(capsKey("esc"))
	assert.True(t, closed, "esc leaves even while saving")
	assert.False(t, submit)
}

func TestWorkCapsOverlay_ShowsTheModelsRefusal(t *testing.T) {
	o := newCaps(WorkCapsValues{CPUs: 2000})
	o.SetSaving(true)
	o.SetError("scheduler.cpus is 2000: it must be 1–1024")
	assert.False(t, o.Saving(), "an answer ends the save")
	assert.Contains(t, ansi.Strip(o.Render()), "scheduler.cpus is 2000: it must be 1–1024")
	pressCaps(o, "backspace")
	assert.NotContains(t, ansi.Strip(o.Render()), "it must be", "editing clears the refusal")
	assert.Equal(t, 200, o.Values().CPUs)
}

func TestWorkCapsOverlay_NamesWhyAdmissionIsPaused(t *testing.T) {
	host := WorkCapsHost{Name: "fresco", Cores: 16, RAMBytes: 30 << 30}
	o := NewWorkCapsOverlay(host, WorkCapsValues{}, WorkCapsValues{CPUs: 2000}, "scheduler.cpus is 2000: it must be 1–1024")
	assert.Contains(t, ansi.Strip(o.Render()), "admission paused: scheduler.cpus is 2000: it must be 1–1024")
	assert.NotContains(t, ansi.Strip(newCaps(WorkCapsValues{}).Render()), "admission paused")
}

func TestWorkCapsOverlay_IsAnOverlay(t *testing.T) {
	var o Overlay = newCaps(WorkCapsValues{})
	o.SetSize(100, 40)
	closed, cmd := o.HandleKey(capsKey("esc"))
	assert.True(t, closed)
	assert.Nil(t, cmd)
	assert.Contains(t, ansi.Strip(o.View()), "Work queue caps")
}
