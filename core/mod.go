package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
)

// modGrace is how long after a launch's SessionStart (or the daemon's start
// serving the mod, whichever is later: modsSince) its loom mod has to say
// hello before the launch counts as having none (modAbsent). It covers the
// worst reconnect after a daemon restart: a mod mid-way through its longest
// backoff (5 s) when the socket starts listening, then a hello that beats
// the hook scan adopting its launch and gets a 404, costing another 5 s
// backoff, then the scan and the hello's round trips.
const modGrace = 15 * time.Second

// modStartCap bounds how long a hooked launch that has reported no
// SessionStart counts as still starting, from its link's creation (or the
// daemon's boot, whichever is later): Claude may sit at a trust prompt left
// to the person, or its settings may stop hooks (disableAllHooks, managed
// allowManagedHooksOnly), so the SessionStart never comes. Past it the
// launch has no mod (modAbsent), and what waits for one falls back. Generous,
// since a first start can take a while (plugins, MCP servers) and the
// fallback pastes into whatever the pane shows.
const modStartCap = 60 * time.Second

// ModPollTimeout is how long a live loom mod may go without saying hello or
// polling its inbox before its launch counts as having none (modAbsent): its
// socket was removed, its module unloaded, or Claude's hooks worker died. A
// mod that works never goes this long: it polls again at once after each
// answer, a held poll ends within the server's hold
// (internal/modserver.DefaultHold), and after a failure the mod retries
// within 5 s. internal/modserver's tests keep the hold plus that 5 s under
// it.
const ModPollTimeout = 45 * time.Second

// ErrModUnknownLaunch answers a mod whose launch no served instance holds now:
// its session relaunched or left loom, or the hook scan has not yet read a
// restored session's launch. The mod retries.
var ErrModUnknownLaunch = errors.New("no session holds this launch")

// ErrModSuperseded answers a module instance a newer one has replaced: a poll
// naming another poller than the recorded hello's, or a hello from an
// instance loaded before the recorded one. The superseded instance stops
// polling (a reloaded module's old pump, which would otherwise wake the new
// one's held poll with each of its own, and be woken by them).
var ErrModSuperseded = errors.New("a newer loom mod polls for this launch")

// Origin says who sent a message, which decides how the mod submits it and
// what happens when the session's launch has no live mod.
type Origin string

const (
	// OriginCLI is `loom session send`: framed, and refused without a mod.
	OriginCLI Origin = "cli"
	// OriginScript is Lua's inst:send_prompt: the person's own words, and
	// pasted without a mod, as SendPrompt does.
	OriginScript Origin = "script"
	// OriginFirstPrompt is an N flow's first prompt, which only the daemon
	// sends (deliverStart): the person's own words, pasted without a mod.
	OriginFirstPrompt Origin = "first-prompt"
)

// Message is a message for a session's Claude, delivered through the loom mod
// (Send).
type Message struct {
	Text string
	// From names the sender on a line before a CLI message's text.
	From string
	// Wait asks for Answered when the turn that carries the message ends.
	Wait   bool
	Origin Origin
}

// compose is the text the mod submits for msg: a CLI message names its sender
// on a line of its own.
func (msg Message) compose() string {
	if msg.Origin == OriginCLI && msg.From != "" {
		return "From " + msg.From + ":\n\n" + msg.Text
	}
	return msg.Text
}

// Send delivers msg to the session id's Claude through the loom mod
// (enqueue): queued behind any running turn or open dialog, never typed into
// the pane; messages that wait through the same turn are answered together,
// as one turn. The Reply comes once the mod has queued it, or at once with a
// refusal: a session not started or Paused (opSend's precondition), one busy
// (Loading: a start or resume is launching it, and its link would be the old
// launch's, or a pause is stopping it), an origin no sender may use, or
// (OriginCLI) an empty message, another program than Claude or a launch
// without a live mod. A script's message is pasted instead on such a launch,
// as SendPrompt does, and so is a script's empty one on any launch. With
// Wait, Answered follows when the turn that carries the message ends.
func (m *Model) Send(id InstanceID, msg Message, req ReqID) {
	inst, _, ok := m.admit(opSend, id, req)
	if !ok {
		return
	}
	switch {
	case inst.GetStatus() == session.Loading:
		m.refuse(req, id, fmt.Errorf("%s %s: the session is busy (starting, resuming or pausing): send again once it runs", opSend, inst.Title))
		return
	case strings.TrimSpace(msg.Text) == "" && msg.Origin == OriginCLI:
		m.refuse(req, id, fmt.Errorf("send to %s: the message is empty", inst.Title))
		return
	case msg.Origin != OriginCLI && msg.Origin != OriginScript:
		m.refuse(req, id, fmt.Errorf("send to %s: %q is no sender's origin", inst.Title, msg.Origin))
		return
	case msg.Origin == OriginCLI && !session.IsClaudeProgram(inst.Program()):
		m.refuse(req, id, fmt.Errorf("send to %s: %s", inst.Title, noModReason(inst)))
		return
	}
	out := &modMessage{text: msg.compose(), asUser: msg.Origin == OriginScript, origin: msg.Origin, req: req, wait: msg.Wait}
	if strings.TrimSpace(msg.Text) == "" {
		// A script's empty send_prompt presses Enter, as it did before the
		// mod: Lua's API keeps it, and the mod has nothing to submit.
		m.withoutMod(inst, out, "")
		return
	}
	m.enqueue(inst, out)
}

// ModHello is what a mod says when it starts or reconnects.
type ModHello struct {
	Mod     int    `json:"mod"`
	Claude  string `json:"claude"`
	Session string `json:"session"`
	// Poller names the module instance saying hello: a random ID each
	// loaded module mints once, so a reloaded one has a new one. Its polls
	// carry it (ModInbox), and only the recorded hello's poller may poll. ""
	// from an older mod, which names none.
	Poller string `json:"poller,omitempty"`
	// Loaded is when that instance loaded, in Unix milliseconds: of two
	// instances still pumping (a reload's old one after a daemon restart,
	// both saying hello again on their own backoff), the one loaded last
	// wins whichever hello lands last. 0 from an older mod.
	Loaded int64 `json:"loaded,omitempty"`
}

// ModMessage is a message as the mod's inbox hands it out.
type ModMessage struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	AsUser bool   `json:"asUser,omitempty"`
}

// ModEvent is one thing a mod reports about a message: queued (handed to
// Claude), started (its turn began), complete (its turn ended: Text is the
// answer, Reason why) or failed (Error). Claude merges the messages queued
// during one turn into the next, so several messages can report one turn
// (the same TurnID and answer): each is settled by its own ID.
type ModEvent struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	TurnID string `json:"turnId,omitempty"`
	Text   string `json:"text,omitempty"`
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ModHost is the model as the loom mod's server reaches it
// (internal/modserver). Each call names the launch the mod runs in
// (LOOM_LAUNCH); one no served instance holds now is ErrModUnknownLaunch.
// *Loop implements it.
type ModHost interface {
	ModHello(launch string, hello ModHello) error
	// ModInbox returns the messages waiting for launch, and a channel that
	// closes when that may have changed (a message arrived, a newer poll
	// superseded this one, the launch ended). A nil channel means the
	// daemon is stopping. poller names the polling module instance
	// (ModHello.Poller); one the latest hello did not name is
	// ErrModSuperseded.
	ModInbox(launch, poller string) ([]ModMessage, <-chan struct{}, error)
	ModEvents(launch string, events []ModEvent) error
	// ModsServing says the server listens from now, so every launch's
	// grace runs from here at the earliest.
	ModsServing()
}

// programName is program's command name: "aider" for "/usr/bin/aider --model
// x".
func programName(program string) string {
	f := strings.Fields(program)
	if len(f) == 0 {
		return program
	}
	return filepath.Base(f[0])
}

// modLiveness is whether a launch has a loom mod.
type modLiveness int

const (
	modPending modLiveness = iota // launched; no hello yet
	modLive                       // the mod said hello
	modAbsent                     // no mod: not offered, or no hello within the grace
)

// modMessage is a message for a session's mod.
type modMessage struct {
	id     string
	text   string
	asUser bool
	origin Origin
	req    ReqID // the sender's request; 0 for none
	wait   bool  // the sender waits for the turn's end (Answered)
	// offered is set once an inbox poll has handed the message to the mod,
	// which may have submitted it whether or not its report arrived.
	offered bool
	// cause and owner finish a first prompt's start: the Create's request,
	// which Started carries, and the start's owner.
	cause ReqID
	owner *Workspace
}

// modLink is what the model knows of one instance's current launch and its
// mod.
type modLink struct {
	launch  string
	live    modLiveness
	outbox  []*modMessage          // waiting for the mod, oldest first
	turning map[string]*modMessage // queued with Wait: waiting for their turn's end, by ID
	wake    chan struct{}
	// heard is when the mod last said hello or polled its inbox: a live
	// link silent for ModPollTimeout turns absent (modTick).
	heard time.Time
	// poller is the module instance the recorded hello named
	// (ModHello.Poller), "" for none: only it polls (ModInbox). loaded is
	// that instance's load time (ModHello.Loaded), 0 for none: a hello from
	// an instance loaded earlier is refused (ModHello).
	poller string
	loaded int64
	// since is when the link was made: the model met the launch then, as a
	// start or relaunch delivered or a message or mod named it. A launch
	// that reports no SessionStart is still starting only up to
	// modStartCap from it (pastModGrace).
	since time.Time
}

func newModLink(launch string, live modLiveness) *modLink {
	return &modLink{launch: launch, live: live, turning: map[string]*modMessage{}, wake: make(chan struct{}), since: time.Now()}
}

// poke ends a held poll (its channel closes) and arms the next.
func (l *modLink) poke() {
	close(l.wake)
	l.wake = make(chan struct{})
}

// take removes the message id from the outbox and returns it; nil when none
// waits there.
func (l *modLink) take(id string) *modMessage {
	for i, msg := range l.outbox {
		if msg.id == id {
			l.outbox = append(l.outbox[:i], l.outbox[i+1:]...)
			return msg
		}
	}
	return nil
}

// newModMessageID is a message's ID: random, so IDs stay unique across daemon
// restarts, which the mod's duplicate check relies on.
func newModMessageID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// modLaunch is inst's current launch, and whether it can have a mod: a daemon
// serves the mod (session.ModSocket), the program is Claude, and the launch is
// hooked, or not yet known ("": a restored instance before its first scan).
func modLaunch(inst *session.Instance) (launch string, possible bool) {
	launch, hooked := inst.HookLaunch()
	if session.ModSocket() == "" || !session.IsClaudeProgram(inst.Program()) {
		return launch, false
	}
	return launch, hooked || launch == ""
}

// linkFor is inst's link for its current launch. A link for an earlier launch
// ends (endLink) and a new one starts, pending, or absent when the launch can't
// have a mod; a link made before a restored instance's scan takes the launch
// the scan read.
func (m *Model) linkFor(inst *session.Instance) *modLink {
	launch, possible := modLaunch(inst)
	l := m.mods[inst]
	switch {
	case l != nil && l.launch == launch:
		return l
	case l != nil && l.launch == "" && possible:
		l.launch = launch
		return l
	case l != nil:
		m.endLink(inst, l, "the session relaunched")
	}
	live := modPending
	if !possible || m.modsStopped {
		live = modAbsent
	}
	l = newModLink(launch, live)
	if m.mods == nil {
		m.mods = map[*session.Instance]*modLink{}
	}
	m.mods[inst] = l
	return l
}

// instByLaunch is the served instance whose current launch is launch.
func (m *Model) instByLaunch(launch string) *session.Instance {
	if launch == "" {
		return nil
	}
	for _, ws := range m.workspaces {
		for _, inst := range ws.insts {
			if id, hooked := inst.HookLaunch(); hooked && id == launch {
				return inst
			}
		}
	}
	return nil
}

// enqueue hands msg to inst's mod: into the outbox while the launch's mod is
// pending or live, or at once to the fallback when it has none.
func (m *Model) enqueue(inst *session.Instance, msg *modMessage) {
	msg.id = newModMessageID()
	l := m.linkFor(inst)
	if l.live == modAbsent {
		m.withoutMod(inst, msg, "")
		return
	}
	l.outbox = append(l.outbox, msg)
	l.poke()
}

// withoutMod delivers msg the way it goes when inst's launch has no live mod:
// a CLI message is refused, a script's is pasted (SendPrompt's job, answering
// its request), and a first prompt is pasted as a start always sent it
// (sendInitialPrompt, whose promptSent emits Started). why, when set, says
// why the mod did not deliver it, for the CLI's refusal; "" is a launch that
// never had a mod.
func (m *Model) withoutMod(inst *session.Instance, msg *modMessage, why string) {
	id := m.idOf(inst)
	switch msg.origin {
	case OriginCLI:
		if why == "" {
			why = noModReason(inst)
		}
		m.refuse(msg.req, id, fmt.Errorf("send to %s: %s", inst.Title, why))
	case OriginScript:
		m.causedBy(0, func() { m.spawn(m.track(msg.req, id, m.sendPromptInst(inst, msg.text))) })
	case OriginFirstPrompt:
		m.causedBy(msg.cause, func() { m.spawn(m.sendInitialPrompt(inst, msg.owner, msg.text)) })
	}
}

// noModReason says why inst's launch has no live mod, and what to do, for a
// CLI message refused there: it runs another program than Claude, the daemon
// serves no mod (Windows, or a socket path too long), the session was
// launched before this daemon started and its mod has not said hello (an
// older loom's launch, common after an upgrade, or a mod that failed: a
// pause and resume relaunches it with the mod), its launch has no hooks, or
// its Claude never said hello. Launches counts this process's launches only,
// so it can't tell an older loom's launch from a mod that failed.
func noModReason(inst *session.Instance) string {
	launch, hooked := inst.HookLaunch()
	switch {
	case !session.IsClaudeProgram(inst.Program()):
		return fmt.Sprintf("it runs %s, not Claude: loom session send reaches only Claude", programName(inst.Program()))
	case session.ModSocket() == "":
		return "this loom daemon serves no loom mod (on Windows, or where its socket path would be too long): attach and type instead"
	case inst.Launches() == 0:
		return "it was launched before this daemon started and its loom mod has not said hello: pause and resume it to relaunch it with the mod, or attach and type instead"
	case launch != "" && !hooked:
		return "it runs without loom's hooks (its program passes its own --settings, or its hooks folder could not be prepared), so it has no loom mod: attach and type instead"
	default:
		return "its Claude runs without the loom mod (an older Claude, or the mod did not load): attach and type instead"
	}
}

// endLink settles everything l holds once its launch is over: a sender whose
// message was never queued hears why (undelivered), and a sender waiting for
// a turn's end gets a Notice. A held poll ends. why says what happened ("the
// session stopped").
func (m *Model) endLink(inst *session.Instance, l *modLink, why string) {
	m.causedBy(0, func() {
		for _, msg := range l.outbox {
			m.undelivered(inst, msg, why+" before the message reached it")
		}
		m.endTurns(inst, l, why)
	})
	l.outbox, l.turning = nil, map[string]*modMessage{}
	l.poke()
}

// undelivered settles a message that will not be queued and must not be
// pasted either: its sender hears why, and a first prompt finishes its start
// as a failed paste does (logged, then Started).
func (m *Model) undelivered(inst *session.Instance, msg *modMessage, why string) {
	if msg.origin == OriginFirstPrompt {
		log.For("core").Warn("mod.first_prompt_undelivered", "instance", inst.Title, "why", why)
		m.causedBy(msg.cause, func() { m.deliverPromptSent(promptSent{inst: inst, owner: msg.owner}) })
		return
	}
	m.refuse(msg.req, m.idOf(inst), fmt.Errorf("send to %s: %s", inst.Title, why))
}

// endTurns tells every sender waiting for a turn's end on l that it will not
// hear it, because of why, and forgets them.
func (m *Model) endTurns(inst *session.Instance, l *modLink, why string) {
	for _, msg := range l.turning {
		m.emit(Notice{Req: msg.req, Err: fmt.Errorf("send to %s: %s before its turn ended", inst.Title, why)})
	}
	l.turning = map[string]*modMessage{}
}

// modGone turns l's launch absent and settles what it holds: a message the
// mod never took goes as a modless launch's would (withoutMod, for why); one
// it took may have reached Claude, so it is never pasted (undelivered); and
// every turn's waiter hears it will not end. A held poll ends.
func (m *Model) modGone(inst *session.Instance, l *modLink, why string) {
	l.live = modAbsent
	out := l.outbox
	l.outbox = nil
	took := why
	if took == "" {
		took = "the loom mod went away"
	}
	m.causedBy(0, func() {
		for _, msg := range out {
			if msg.offered {
				m.undelivered(inst, msg, took+" after it took the message, which may or may not have reached Claude")
				continue
			}
			m.withoutMod(inst, msg, why)
		}
		m.endTurns(inst, l, took)
	})
	l.poke()
}

// modTick settles each link by its instance: one that left the model or
// stopped running ends; a relaunch ends the old launch's (linkFor); a pending
// launch past its grace, or a live one silent for ModPollTimeout, turns
// absent (modGone). A later hello makes it live again. The hook timer runs
// it (HookTick).
func (m *Model) modTick(now time.Time) {
	for inst, l := range m.mods {
		st := inst.GetStatus()
		switch {
		case m.holding(inst) == nil:
			m.endLink(inst, l, "the session left loom")
			delete(m.mods, inst)
		case !inst.Started() || st == session.Paused || st == session.Recoverable:
			m.endLink(inst, l, "the session stopped")
			delete(m.mods, inst)
		default:
			l = m.linkFor(inst)
			switch {
			case l.live == modPending && m.pastModGrace(inst, l, now):
				log.For("core").Info("mod.absent", "instance", inst.Title, "launch", l.launch)
				m.modGone(inst, l, "")
			case l.live == modLive && now.Sub(l.heard) >= ModPollTimeout:
				log.For("core").Warn("mod.silent", "instance", inst.Title, "launch", l.launch, "heard", l.heard)
				m.modGone(inst, l, "the loom mod stopped answering")
			}
		}
	}
}

// ModsServing notes that the daemon's mod socket listens from now: the grace
// of every launch runs from here at the earliest (modsSince), since no mod
// could reach the daemon before.
func (m *Model) ModsServing() { m.modsSince = time.Now() }

// ModsStop settles every link as the daemon stops, after its mod server has
// closed, so no mod can queue what the links hold (modGone, "the loom daemon
// stopped"): what no mod took is pasted, a CLI message refused, what a mod
// took never pasted, and a turn's waiter told. The pastes are jobs, which the
// stop's quiesce waits for, as it waited for a first prompt's paste before
// the mod. Every launch counts as having none from now on (modsStopped).
func (m *Model) ModsStop() {
	m.modsStopped = true
	for inst, l := range m.mods {
		m.modGone(inst, l, "the loom daemon stopped")
	}
	log.For("core").Info("mod.stopped", "links", len(m.mods))
}

// pastModGrace reports whether inst's launch, whose link is l, has had its
// grace: modGrace since its SessionStart or the daemon's boot, whichever is
// later. A launch that has not reported its SessionStart is still starting (a
// trust prompt, say), but only up to modStartCap from the later of l's
// creation and the boot: its hooks may never report one. A restored launch no
// scan has adopted ("") times its grace from the boot alone: a hooked one is
// adopted at its first scan, well inside the grace, so one still unadopted
// then has no hooks (its program passes its own --settings, or its folder was
// never prepared or was swept) and will never report a SessionStart.
func (m *Model) pastModGrace(inst *session.Instance, l *modLink, now time.Time) bool {
	at := m.modsSince
	if launch, _ := inst.HookLaunch(); launch == "" {
		return now.Sub(at) >= modGrace
	}
	started, ok := inst.LaunchSessionStartedAt()
	if !ok {
		if l.since.After(at) {
			at = l.since
		}
		return now.Sub(at) >= modStartCap
	}
	if started.After(at) {
		at = started
	}
	return now.Sub(at) >= modGrace
}

// ModHello records that launch's mod is live, and the module instance that
// alone may poll: the one loaded last (ModHello.Loaded), whichever hello lands
// last, so a stale instance's hello after a daemon restart is
// ErrModSuperseded and records nothing. A hello naming no load time is taken
// as the latest.
func (m *Model) ModHello(launch string, h ModHello) error {
	inst := m.instByLaunch(launch)
	if inst == nil {
		return ErrModUnknownLaunch
	}
	l := m.linkFor(inst)
	if m.modsStopped {
		return nil // no mod is served any more: nothing revives
	}
	if h.Loaded != 0 && h.Loaded < l.loaded {
		return ErrModSuperseded // a stale instance, still pumping: it stops
	}
	if l.live != modLive {
		log.For("core").Info("mod.live", "instance", inst.Title, "launch", launch, "mod", h.Mod, "claude", h.Claude)
	}
	if l.poller != h.Poller && l.poller != "" {
		log.For("core").Info("mod.new_poller", "instance", inst.Title, "launch", launch, "poller", h.Poller)
	}
	l.live = modLive
	l.heard = time.Now()
	l.poller, l.loaded = h.Poller, h.Loaded
	return nil
}

// ModInbox returns the messages waiting for launch's mod, and the channel a
// held poll waits on. A poll supersedes any poll still held for the launch.
// A poll proves the mod is there, so it makes the link live as a hello does:
// one judged silent while it still polled (the loop stalled past
// ModPollTimeout) would otherwise stay absent, since the mod says hello again
// only after a failed call. Once the daemon stops (ModsStop) nothing revives.
// The newest module instance wins: a poll naming another poller than the
// latest hello's is ErrModSuperseded, and pokes, stamps and revives nothing,
// so two pumps on one launch never wake each other. A poll or hello that
// names none (an older mod) is accepted.
func (m *Model) ModInbox(launch, poller string) ([]ModMessage, <-chan struct{}, error) {
	inst := m.instByLaunch(launch)
	if inst == nil {
		return nil, nil, ErrModUnknownLaunch
	}
	l := m.linkFor(inst)
	if poller != "" && l.poller != "" && poller != l.poller {
		return nil, nil, ErrModSuperseded
	}
	if l.live != modLive && !m.modsStopped {
		log.For("core").Info("mod.live", "instance", inst.Title, "launch", launch, "by", "poll")
		l.live = modLive
	}
	l.poke()
	l.heard = time.Now()
	out := make([]ModMessage, 0, len(l.outbox))
	for _, msg := range l.outbox {
		msg.offered = true
		out = append(out, ModMessage{ID: msg.id, Text: msg.text, AsUser: msg.asUser})
	}
	return out, l.wake, nil
}

// ModEvents applies what launch's mod reports.
func (m *Model) ModEvents(launch string, events []ModEvent) error {
	inst := m.instByLaunch(launch)
	if inst == nil {
		return ErrModUnknownLaunch
	}
	l := m.linkFor(inst)
	for _, ev := range events {
		switch ev.Kind {
		case "queued":
			m.modQueued(inst, l, ev.ID)
		case "started":
			log.DebugKV("core.mod.started", "instance", inst.Title, "id", ev.ID, "turn", ev.TurnID)
		case "complete":
			m.modComplete(inst, l, ev)
		case "failed":
			m.modFailed(inst, l, ev)
		default:
			log.For("core").Warn("mod.unknown_event", "instance", inst.Title, "kind", ev.Kind)
		}
	}
	return nil
}

// modQueued answers the sender of the message id: a first prompt's start
// finishes (Started, carrying the Create's request), any other sender gets
// its Reply, and one that waits for the turn's end is kept until it comes.
func (m *Model) modQueued(inst *session.Instance, l *modLink, id string) {
	msg := l.take(id)
	if msg == nil {
		return // reported again: the mod's report was retried
	}
	if msg.origin == OriginFirstPrompt {
		m.causedBy(msg.cause, func() { m.deliverPromptSent(promptSent{inst: inst, owner: msg.owner}) })
		return
	}
	m.reply(msg.req, Reply{ID: m.idOf(inst)})
	if msg.wait && msg.req != 0 {
		l.turning[msg.id] = msg
	}
}

// modComplete hands a waiting sender its turn's answer.
func (m *Model) modComplete(inst *session.Instance, l *modLink, ev ModEvent) {
	msg := l.turning[ev.ID]
	if msg == nil {
		return
	}
	delete(l.turning, ev.ID)
	m.emit(Answered{Req: msg.req, ID: m.idOf(inst), Text: ev.Text, Reason: ev.Reason})
}

// modFailed settles a message the mod could not submit: one still in the
// outbox falls back as a modless launch's would; one Claude dropped after it
// was queued is reported (to its waiter, or to every client), never pasted,
// since it may have reached Claude after all. A failed report means the
// prompt never entered Claude: a drop is answered before it enters, and a
// rejected or thrown submit never submitted it. So pasting one still in the
// outbox can't deliver it twice, and the rule that a message the mod took is
// never pasted (offered, modGone) does not apply here.
func (m *Model) modFailed(inst *session.Instance, l *modLink, ev ModEvent) {
	if msg := l.take(ev.ID); msg != nil {
		m.withoutMod(inst, msg, "the loom mod could not submit it: "+ev.Error)
		return
	}
	err := fmt.Errorf("send to %s: Claude dropped a message loom delivered: %s", inst.Title, ev.Error)
	if msg := l.turning[ev.ID]; msg != nil {
		delete(l.turning, ev.ID)
		m.emit(Notice{Req: msg.req, Err: err})
		return
	}
	m.emit(Notice{Err: err})
}
