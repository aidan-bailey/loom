package app

import (
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/sched"
	"github.com/aidan-bailey/loom/ui"
	"github.com/aidan-bailey/loom/ui/overlay"

	tea "charm.land/bubbletea/v2"
)

// pendingWork is a request of the work view waiting for its Reply. form is
// the caps form that sent a SetSchedulerCaps, whose Reply it answers; nil
// for the other requests.
type pendingWork struct {
	form *overlay.WorkCapsOverlay
}

// workReplied routes a work view request's Reply. The model's answer to a
// caps save goes to the form that sent it while that form is still the one
// open: a refusal into the form, an acceptance closing it. An answer for a
// form that has since closed, or been replaced, and every other Reply's
// error goes to the error line.
func (m *home) workReplied(p *pendingWork, r core.Reply) tea.Cmd {
	if o := p.form; o != nil && o == m.workCapsOverlay() {
		if r.Err != nil {
			o.SetError(r.Err.Error())
			return nil
		}
		m.dismissOverlay()
		m.state = stateDefault
		return nil
	}
	if r.Err != nil {
		return m.handleError(r.Err)
	}
	return nil
}

// openWorkCaps opens the caps form over the view, on the scheduler block as
// the model last published it. Before the daemon has reported its queue
// there is no host to edit caps for.
func (m *home) openWorkCaps() {
	q := m.queue
	if !ui.WorkReported(q) {
		m.errBox.SetInfo("the daemon has not reported its work queue yet")
		return
	}
	host := overlay.WorkCapsHost{Name: q.Host.Name, Cores: q.Host.Cores, RAMBytes: q.Host.RAMBytes}
	o := overlay.NewWorkCapsOverlay(host, workDefaults(q.Host, q.Settings), capsValues(q.Settings), q.Paused)
	o.SetSize(int(float32(m.lastWidth)*ui.OverlayWidthPercent), int(float32(m.lastHeight)*ui.OverlayHeightPercent))
	m.setOverlay(o, overlayWorkCaps)
	m.state = stateWorkCaps
}

// handleStateWorkCapsKey drives the caps form. Esc closes it; Enter sends
// its values as SetSchedulerCaps and holds the form until the Reply
// (workReplied), which carries the model's refusal back into it.
func handleStateWorkCapsKey(m *home, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	o := m.workCapsOverlay()
	if o == nil {
		return m, nil
	}
	closed, submit := o.HandleKeyPress(msg)
	switch {
	case closed:
		m.dismissOverlay()
		m.state = stateDefault
	case submit:
		o.SetSaving(true)
		m.core.SetSchedulerCaps(schedulerSettings(o.Values()), m.newReq(pendingReq{work: &pendingWork{form: o}}))
	}
	return m, nil
}

// workDefaults is what each field of the form takes when it is left empty:
// the value in effect, which the model's resolve gives with only what that
// field's default depends on. The published block is not resolved whole: when
// it is invalid, which is when the user opens the form to fix it, one fault
// would otherwise take every other field's default with it. So cpus, io_jobs,
// starve and keep resolve against a block holding only a placeholder memory
// (Resolve refuses a block with no memory on a host whose RAM can't be read);
// memory_gb against an empty one; and each half of the default claim against
// the one configured cap it follows (cpus, or memory_gb), the other half at its
// default. A field whose
// resolve fails has no default and stays 0: a host whose RAM could not be read
// with no memory_gb set has no memory default, nor a claim's memory, while
// the fields that do not depend on memory keep theirs.
func workDefaults(h sched.Host, s config.SchedulerSettings) overlay.WorkCapsValues {
	// The placeholder only has to pass the claim's bound; no field read
	// through it is a memory one.
	const placeholder = 1
	// of resolves block and reads one field of the result; 0 when it fails.
	of := func(block config.SchedulerSettings, field func(sched.Policy) int) int {
		p, err := sched.Resolve(block, h)
		if err != nil {
			return 0
		}
		return field(p)
	}
	independent := config.SchedulerSettings{MemoryGB: placeholder}
	claimMem := s.MemoryGB
	if claimMem == 0 {
		claimMem = placeholder
	}
	return overlay.WorkCapsValues{
		CPUs:          of(independent, func(p sched.Policy) int { return p.Caps.CPUs }),
		MemoryGB:      of(config.SchedulerSettings{}, func(p sched.Policy) int { return p.Caps.MemoryGB }),
		IOJobs:        of(independent, func(p sched.Policy) int { return p.Caps.IOJobs }),
		ClaimCPUs:     of(config.SchedulerSettings{CPUs: s.CPUs, MemoryGB: claimMem}, func(p sched.Policy) int { return p.DefaultClaim.CPUs }),
		ClaimMemoryGB: of(config.SchedulerSettings{MemoryGB: s.MemoryGB}, func(p sched.Policy) int { return p.DefaultClaim.MemoryGB }),
		StarveMinutes: of(independent, func(p sched.Policy) int { return int(p.StarveAfter / time.Minute) }),
		KeepFinished:  of(independent, func(p sched.Policy) int { return p.KeepFinished }),
	}
}

// capsValues is the scheduler block as the form's numbers: 0 for a field the
// block leaves on the default.
func capsValues(s config.SchedulerSettings) overlay.WorkCapsValues {
	v := overlay.WorkCapsValues{CPUs: s.CPUs, MemoryGB: s.MemoryGB, IOJobs: s.IOJobs, StarveMinutes: s.StarveAfterMinutes, KeepFinished: s.KeepFinished}
	if s.DefaultClaim != nil {
		v.ClaimCPUs, v.ClaimMemoryGB = s.DefaultClaim.CPUs, s.DefaultClaim.MemoryGB
	}
	return v
}

// schedulerSettings is the scheduler block the form's numbers make. A form
// that edited no claim field leaves the block without a default claim.
func schedulerSettings(v overlay.WorkCapsValues) config.SchedulerSettings {
	s := config.SchedulerSettings{CPUs: v.CPUs, MemoryGB: v.MemoryGB, IOJobs: v.IOJobs, StarveAfterMinutes: v.StarveMinutes, KeepFinished: v.KeepFinished}
	if v.ClaimCPUs != 0 || v.ClaimMemoryGB != 0 {
		s.DefaultClaim = &config.ClaimSettings{CPUs: v.ClaimCPUs, MemoryGB: v.ClaimMemoryGB}
	}
	return s
}
