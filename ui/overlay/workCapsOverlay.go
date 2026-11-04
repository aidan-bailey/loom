package overlay

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// workCapsField indexes the caps form's rows, in display order.
type workCapsField int

const (
	workCapsCPUs workCapsField = iota
	workCapsMemoryGB
	workCapsIOJobs
	workCapsClaimCPUs
	workCapsClaimMemoryGB
	workCapsStarveMinutes
	workCapsKeepFinished
	workCapsFieldCount
)

// workCapsMaxDigits is how many digits a field takes: nine always fit an int.
const workCapsMaxDigits = 9

func (f workCapsField) label() string {
	switch f {
	case workCapsCPUs:
		return "CPUs"
	case workCapsMemoryGB:
		return "Memory (GB)"
	case workCapsIOJobs:
		return "I/O jobs"
	case workCapsClaimCPUs:
		return "Default claim CPUs"
	case workCapsClaimMemoryGB:
		return "Default claim GB"
	case workCapsStarveMinutes:
		return "Starve after (min)"
	case workCapsKeepFinished:
		return "Keep finished jobs"
	}
	return ""
}

// WorkCapsHost is the machine the form edits caps for: its name, cores and
// RAM in bytes (0 when the daemon could not read it).
type WorkCapsHost struct {
	Name     string
	Cores    int
	RAMBytes uint64
}

// WorkCapsValues holds one number per field of the form. As a form's
// content, 0 is a field left on the host's default; as defaults, 0 is a
// field the host has none for (the memory fields, when its RAM could not
// be read).
type WorkCapsValues struct {
	CPUs          int
	MemoryGB      int
	IOJobs        int
	ClaimCPUs     int
	ClaimMemoryGB int
	StarveMinutes int
	KeepFinished  int
}

func (v WorkCapsValues) get(f workCapsField) int {
	switch f {
	case workCapsCPUs:
		return v.CPUs
	case workCapsMemoryGB:
		return v.MemoryGB
	case workCapsIOJobs:
		return v.IOJobs
	case workCapsClaimCPUs:
		return v.ClaimCPUs
	case workCapsClaimMemoryGB:
		return v.ClaimMemoryGB
	case workCapsStarveMinutes:
		return v.StarveMinutes
	case workCapsKeepFinished:
		return v.KeepFinished
	}
	return 0
}

func (v *WorkCapsValues) set(f workCapsField, n int) {
	switch f {
	case workCapsCPUs:
		v.CPUs = n
	case workCapsMemoryGB:
		v.MemoryGB = n
	case workCapsIOJobs:
		v.IOJobs = n
	case workCapsClaimCPUs:
		v.ClaimCPUs = n
	case workCapsClaimMemoryGB:
		v.ClaimMemoryGB = n
	case workCapsStarveMinutes:
		v.StarveMinutes = n
	case workCapsKeepFinished:
		v.KeepFinished = n
	}
}

// WorkCapsOverlay is the work view's caps form: a field for each number of
// the scheduler block, each showing the value in effect and whether it is
// the host's default. It owns no persistence: HandleKeyPress reports an
// Enter, and the caller sends the values (Values) and reports the model's
// answer (SetError, or closing the form).
type WorkCapsOverlay struct {
	host     WorkCapsHost
	defaults WorkCapsValues
	paused   string
	buf      [workCapsFieldCount]string
	cursor   workCapsField
	width    int
	saving   bool
	err      string
}

var _ Overlay = (*WorkCapsOverlay)(nil)

// NewWorkCapsOverlay opens the form over the values set in the scheduler
// block (0 for a field it leaves on the default), with the host's defaults
// beside them and the reason admission is paused ("" when it is not).
func NewWorkCapsOverlay(host WorkCapsHost, defaults, set WorkCapsValues, paused string) *WorkCapsOverlay {
	o := &WorkCapsOverlay{host: host, defaults: defaults, paused: paused, width: 66}
	for f := workCapsField(0); f < workCapsFieldCount; f++ {
		if n := set.get(f); n != 0 {
			o.buf[f] = strconv.Itoa(n)
		}
	}
	return o
}

// Values is what the form holds: the number typed in each field, 0 for an
// empty one.
func (o *WorkCapsOverlay) Values() WorkCapsValues {
	var v WorkCapsValues
	for f := workCapsField(0); f < workCapsFieldCount; f++ {
		n, _ := strconv.Atoi(o.buf[f])
		v.set(f, n)
	}
	return v
}

// SetSaving marks a save as sent and unanswered: the form holds its fields
// and ignores Enter until the answer.
func (o *WorkCapsOverlay) SetSaving(saving bool) { o.saving = saving }

// Saving reports whether a save is sent and unanswered.
func (o *WorkCapsOverlay) Saving() bool { return o.saving }

// SetError shows the model's refusal in the form and ends the save. The
// next edit clears it.
func (o *WorkCapsOverlay) SetError(msg string) {
	o.err = msg
	o.saving = false
}

// HandleKeyPress processes one key press. closed reports that the form
// should be dismissed (Esc, which also works while a save is in flight);
// submit reports an Enter, which the caller answers with a save.
func (o *WorkCapsOverlay) HandleKeyPress(msg tea.KeyPressMsg) (closed, submit bool) {
	key := msg.String()
	if key == "esc" {
		return true, false
	}
	if o.saving {
		return false, false
	}
	switch key {
	case "enter":
		o.err = ""
		return false, true
	case "up", "shift+tab":
		o.cursor = workCapsField(max(0, int(o.cursor)-1))
	case "down", "tab":
		o.cursor = workCapsField(min(int(workCapsFieldCount)-1, int(o.cursor)+1))
	case "backspace":
		if b := o.buf[o.cursor]; b != "" {
			o.buf[o.cursor] = b[:len(b)-1]
		}
		o.err = ""
	case "ctrl+u":
		o.buf[o.cursor] = ""
		o.err = ""
	default:
		if len(key) == 1 && key[0] >= '0' && key[0] <= '9' {
			o.typeDigit(key[0])
		}
	}
	return false, false
}

// typeDigit appends d to the field under the cursor. A lone zero is
// replaced rather than followed, and a field takes workCapsMaxDigits.
func (o *WorkCapsOverlay) typeDigit(d byte) {
	o.err = ""
	b := o.buf[o.cursor]
	switch {
	case b == "0":
		b = string(d)
	case len(b) >= workCapsMaxDigits:
		return
	default:
		b += string(d)
	}
	o.buf[o.cursor] = b
}

// HandleKey satisfies the Overlay interface. The app calls HandleKeyPress,
// for the submit signal.
func (o *WorkCapsOverlay) HandleKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	closed, _ := o.HandleKeyPress(msg)
	return closed, nil
}

// SetSize satisfies the Overlay interface; the form uses the width.
func (o *WorkCapsOverlay) SetSize(width, _ int) { o.width = width }

// View satisfies the Overlay interface.
func (o *WorkCapsOverlay) View() string { return o.Render() }

var workCapsTitleStyle, workCapsSelectedStyle, workCapsNormalStyle, workCapsHintStyle, workCapsErrStyle lipgloss.Style

func init() { ui.RegisterThemeHook(rebuildWorkCapsStyles) }

func rebuildWorkCapsStyles() {
	workCapsTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(ui.Accent)
	workCapsSelectedStyle = lipgloss.NewStyle().Background(ui.SelectionBg).Foreground(ui.SelectionFg)
	workCapsNormalStyle = lipgloss.NewStyle().Foreground(ui.Text)
	workCapsHintStyle = lipgloss.NewStyle().Foreground(ui.Faint)
	workCapsErrStyle = lipgloss.NewStyle().Foreground(ui.ErrorColor)
}

// Render draws the form.
func (o *WorkCapsOverlay) Render() string {
	title := "Work queue caps"
	if o.host.Name != "" {
		title += " · " + ui.CleanText(o.host.Name)
	}
	var b strings.Builder
	b.WriteString(workCapsTitleStyle.Render(title) + "\n")
	b.WriteString(workCapsHintStyle.Render(o.hostLine()) + "\n")
	if o.paused != "" {
		b.WriteString(workCapsErrStyle.Render("admission paused: "+ui.CleanText(o.paused)) + "\n")
	}
	b.WriteString("\n")
	for f := workCapsField(0); f < workCapsFieldCount; f++ {
		b.WriteString(o.row(f) + "\n")
	}
	b.WriteString("\n")
	switch {
	case o.saving:
		b.WriteString(workCapsHintStyle.Render("saving…"))
	case o.err != "":
		b.WriteString(workCapsErrStyle.Render(ui.CleanText(o.err)))
	default:
		b.WriteString(workCapsHintStyle.Render("↑/↓ move · 0-9 edit · ^u default · enter save · esc cancel"))
	}
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ui.Accent).
		Padding(1, 2).
		Width(max(o.width, 50))
	return border.Render(b.String())
}

// hostLine names the cores and RAM the defaults come from.
func (o *WorkCapsOverlay) hostLine() string {
	ram := "RAM could not be read"
	if o.host.RAMBytes > 0 {
		ram = fmt.Sprintf("%.1f GB RAM", float64(o.host.RAMBytes)/(1<<30))
	}
	return fmt.Sprintf("this host: %d cores, %s", o.host.Cores, ram)
}

// row draws one field: its value in effect, and whether that is the host's
// default or set.
func (o *WorkCapsOverlay) row(f workCapsField) string {
	cursor := "  "
	if f == o.cursor {
		cursor = "> "
	}
	def := o.defaults.get(f)
	value, note := o.buf[f], ""
	switch {
	case value == "" && def > 0:
		value, note = strconv.Itoa(def), "host default"
	case value == "":
		value, note = "?", "no host default"
	case def > 0:
		note = fmt.Sprintf("set · host default %d", def)
	default:
		note = "set"
	}
	if f == o.cursor && o.buf[f] != "" && !o.saving {
		value += "_"
	}
	line := fmt.Sprintf("%s%-20s %-11s %s", cursor, f.label(), value, note)
	if f == o.cursor {
		return workCapsSelectedStyle.Render(line)
	}
	return workCapsNormalStyle.Render(line)
}
