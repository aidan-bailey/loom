package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/core/rpc"
	"github.com/aidan-bailey/loom/internal/daemon"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
)

// The TUI's link to the daemon. Losing the daemon is not the end of the
// TUI: it keeps showing and attaching panes, refuses what needs the model,
// and joins a daemon again when one answers.
//
//   - connected: the client is the daemon's.
//   - stopping: the daemon said bye (rpc.Client.Stopping). It takes no
//     request, and the replies to the requests in flight still come.
//   - waiting: the connection closed after a bye (a graceful stop, or a
//     newer loom replacing the daemon). The TUI polls for a daemon and never
//     starts one itself; ctrl+r does.
//   - reconnecting: the connection closed with no bye, or the model failed
//     (a crash). The TUI redials on a backoff, starting a daemon when none
//     runs, until maxSpawnFails of them did not start or did not stay up
//     (stableLink) since the link was last stable; then it waits.
//
// Offline (any state but connected) a banner names the state, the offline
// key gate refuses the keys that need the model (offlineKeyAllowed), and every
// request nothing will answer is failed (failStranded). A rejoin swaps the
// client in and resyncs from its replica (resync).

// Rejoin connects to a daemon again: the startup join, quiet. spawn lets it
// start one; say reports progress (a replacement) for the banner. A newer
// daemon's error matches ErrDaemonNewer, and one it started that did not
// start ErrDaemonDidNotStart.
type Rejoin func(spawn bool, say func(string)) (*rpc.Client, error)

// linkState is the TUI's link to the daemon.
type linkState int

const (
	linkConnected    linkState = iota
	linkStopping               // a bye arrived: requests refused, replies in flight still come
	linkWaiting                // closed after a bye: poll for a daemon, never spawn (ctrl+r spawns)
	linkReconnecting           // closed with no bye, or a fatal: redial on a backoff, spawning
)

// String names the state, for logs.
func (s linkState) String() string {
	switch s {
	case linkStopping:
		return "stopping"
	case linkWaiting:
		return "waiting"
	case linkReconnecting:
		return "reconnecting"
	}
	return "connected"
}

// word is what the daemon is, in the state, for the offline gate's info
// line ("the loom daemon is stopped: n needs it").
func (s linkState) word() string {
	switch s {
	case linkStopping:
		return "stopping"
	case linkWaiting:
		return "stopped"
	}
	return "unreachable"
}

// lost reports whether the connection is gone: a rejoin's to make.
func (s linkState) lost() bool { return s == linkWaiting || s == linkReconnecting }

// link is the TUI's link to the daemon (home.link).
type link struct {
	state      linkState
	byeReq     core.ReqID // m.nextReq when the bye arrived
	attempt    int        // rejoin attempts since the link was last stable
	spawnFails int        // rejoins since then whose daemon did not start, or did not stay up
	upAt       time.Time  // when a rejoin brought the link up (zero: the startup join)
	gen        int        // bumped on every state change: a stale tick or result is dropped
	busy       bool       // a rejoin is running
	note       string     // the banner's detail: the last rejoin's progress or error
}

// maxSpawnFails is how many rejoins in a row may start a daemon that does
// not start, or joins one that does not stay up (stableLink), before the
// TUI stops starting them and waits.
const maxSpawnFails = 3

// stableLink is how long a rejoined link must stay up for its rejoin to
// count as a success: one lost sooner counts toward maxSpawnFails, and the
// backoff goes on from its last attempt, so a daemon that serves and then
// dies again (a model panic its first opens trigger, say) is not restarted
// every second, forever, by every TUI.
const stableLink = 30 * time.Second

// waitPoll is how often a waiting TUI looks for a daemon.
const waitPoll = time.Second

// rejoinBackoff is the delay before reconnect attempt n (1-based): 1s, 2s,
// 4s, … capped at 30s.
func rejoinBackoff(n int) time.Duration {
	const ceiling = 30 * time.Second
	d := time.Second
	for i := 1; i < n && d < ceiling; i++ {
		d *= 2
	}
	return min(d, ceiling)
}

// rejoinTickMsg is the timer's turn to join a daemon again; gen is the
// link's when it was scheduled.
type rejoinTickMsg struct{ gen int }

// rejoinedMsg is a rejoin's result: the client joined, or why not.
type rejoinedMsg struct {
	gen    int
	spawn  bool
	client *rpc.Client
	err    error
}

// rejoinNoteMsg is a rejoin's progress, for the banner.
type rejoinNoteMsg struct {
	gen  int
	text string
}

var bannerStyle lipgloss.Style

func init() { ui.RegisterThemeHook(rebuildBannerStyle) }

func rebuildBannerStyle() {
	bannerStyle = lipgloss.NewStyle().Foreground(ui.ErrorColor).Bold(true)
}

// clock is the time, through the now seam tests set (time.Now otherwise).
func (m *home) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// offline reports whether the TUI is not connected to a daemon: stopping,
// waiting or reconnecting. Bare test homes (no client) never are.
func (m *home) offline() bool { return m.link.state != linkConnected }

// checkLink follows the client's state after every Update's drain: a bye
// takes the TUI to stopping, and the loss of the model offline (lose).
// Offline, every request nothing will answer is failed (failStranded):
// while stopping, those made after the bye, and those refused as
// unavailable on either side of it; once lost, all of them.
func (m *home) checkLink() tea.Cmd {
	if m.conn == nil {
		return nil
	}
	switch m.link.state {
	case linkConnected:
		if err := m.conn.Err(); err != nil {
			return m.lose(err)
		}
		if m.conn.Stopping() {
			log.For("app").Info("link.stopping")
			m.link.state = linkStopping
			m.link.byeReq = m.nextReq
			m.link.gen++
			m.relayout()
			return m.failStranded(m.link.byeReq)
		}
		return nil
	case linkStopping:
		if err := m.conn.Err(); err != nil {
			return m.lose(err)
		}
		return m.failStranded(m.link.byeReq)
	}
	return m.failStranded(0)
}

// lose takes the TUI offline once its client lost the model (err): it
// applies what the client received before the loss, closes the client
// (whose wakes then end, and their forwardWakes with them), fails every
// request still waiting, and schedules the first rejoin. A loss after a
// bye (core.ErrUnavailable) is a graceful stop: the TUI waits for a daemon
// and starts none itself. Any other is a crash: it reconnects, starting
// one.
func (m *home) lose(err error) tea.Cmd {
	drained := m.drainCore()
	m.stopCore()
	state := linkReconnecting
	if errors.Is(err, core.ErrUnavailable) {
		state = linkWaiting
	}
	log.For("app").Warn("link.lost", "state", state.String(), "err", err)
	prev := m.link
	m.link = link{state: state, gen: prev.gen + 1}
	if state == linkReconnecting && !prev.upAt.IsZero() && m.clock().Sub(prev.upAt) < stableLink {
		// The daemon this TUI rejoined did not stay up: no success.
		m.link.attempt, m.link.spawnFails = prev.attempt, prev.spawnFails+1
		log.For("app").Warn("link.lost_soon_after_rejoin", "up_for", m.clock().Sub(prev.upAt).String(), "fails", m.link.spawnFails)
		if m.link.spawnFails >= maxSpawnFails {
			m.link.state = linkWaiting
			m.link.note = fmt.Sprintf("it did not stay up %d times: see serve.log", m.link.spawnFails)
		} else {
			m.link.note = "it did not stay up (see serve.log)"
		}
	}
	stranded := m.failStranded(0)
	m.relayout()
	return tea.Batch(drained, stranded, m.scheduleRejoin())
}

// failStranded fails, in ascending order, every request still waiting for
// its Reply that is numbered above after, or that the client refused as
// unavailable (rpc.Client.TakeRefused: refused by the client before this
// TUI saw the bye, or by the server after it crossed the bye), as the
// model would have refused it: unavailable (core.ErrUnavailable). Each flow
// ends as its Reply ends it (handleReply): a lifecycle request shows "kill
// x: the loom daemon is unavailable", a send releases its hold, a Lua call
// resumes raising, a create and an issue fetch show their error.
func (m *home) failStranded(after core.ReqID) tea.Cmd {
	refused := map[core.ReqID]bool{}
	if m.conn != nil {
		for _, req := range m.conn.TakeRefused() {
			refused[req] = true
		}
	}
	var reqs []core.ReqID
	for req := range m.pending {
		if req > after || refused[req] {
			reqs = append(reqs, req)
		}
	}
	if len(reqs) == 0 {
		return nil
	}
	slices.Sort(reqs)
	cmds := make([]tea.Cmd, 0, len(reqs))
	for _, req := range reqs {
		log.For("app").Info("link.request_stranded", "req", uint64(req))
		cmds = append(cmds, m.handleReply(core.Reply{Req: req, Err: core.ErrUnavailable}))
	}
	return tea.Batch(cmds...)
}

// nextRejoinDelay is how long the TUI waits before its next rejoin: the
// backoff of the next reconnect attempt, or the waiting poll.
func (m *home) nextRejoinDelay() time.Duration {
	if m.link.state == linkReconnecting {
		return rejoinBackoff(m.link.attempt + 1)
	}
	return waitPoll
}

// scheduleRejoin arms the next rejoin's tick (nextRejoinDelay), for the
// link's current generation. With no rejoin to call (tests), none.
func (m *home) scheduleRejoin() tea.Cmd {
	if m.rejoin == nil {
		return nil
	}
	gen := m.link.gen
	return tea.Tick(m.nextRejoinDelay(), func(time.Time) tea.Msg { return rejoinTickMsg{gen: gen} })
}

// rejoinTick starts the rejoin a tick asked for, unless the tick is stale
// (another state, or ctrl+r started one since), the TUI is connected or
// stopping, or a rejoin runs already. Reconnecting starts a daemon when
// none runs; waiting never does.
func (m *home) rejoinTick(msg rejoinTickMsg) tea.Cmd {
	if msg.gen != m.link.gen || !m.link.state.lost() || m.link.busy {
		return nil
	}
	return m.attemptRejoin(m.link.state == linkReconnecting)
}

// attemptRejoin returns the Cmd that joins a daemon again (rejoin),
// starting one when spawn. Everything it uses is captured here, on Update:
// the Cmd runs on a goroutine of its own. Its progress reaches the banner
// through the program (rejoinNoteMsg), and its result is a rejoinedMsg.
func (m *home) attemptRejoin(spawn bool) tea.Cmd {
	if m.rejoin == nil {
		return nil
	}
	m.link.busy = true
	m.link.attempt++
	rejoin, gen, send := m.rejoin, m.link.gen, m.send
	say := func(string) {}
	if send != nil {
		say = func(text string) { send(rejoinNoteMsg{gen: gen, text: text}) }
	}
	return func() tea.Msg {
		c, err := rejoin(spawn, say)
		return rejoinedMsg{gen: gen, spawn: spawn, client: c, err: err}
	}
}

// retryNow is ctrl+r offline: a rejoin now, starting a daemon when none
// runs, whatever the state's own schedule (the spawn failures counted so
// far forgotten). Stopping, the daemon must finish first.
func (m *home) retryNow() tea.Cmd {
	switch {
	case m.link.state == linkStopping:
		m.errBox.SetInfo("the loom daemon is still stopping: it finishes its jobs in flight first")
		return nil
	case m.link.busy:
		m.errBox.SetInfo("already joining the loom daemon")
		return nil
	case m.rejoin == nil:
		return nil
	}
	m.link.spawnFails = 0
	// The tick already scheduled is stale from here: this attempt's
	// result schedules the next.
	m.link.gen++
	m.link.note = "starting the loom daemon…"
	return m.attemptRejoin(true)
}

// rejoined applies a rejoin's result: a client joined is resynced; a newer
// daemon makes the TUI exit (Run returns its error); a daemon this TUI
// started that did not start counts toward maxSpawnFails, after which the
// TUI waits; any other failure is the banner's note, and the next rejoin
// is scheduled. A stale result (the link moved on) has its client closed.
func (m *home) rejoined(msg rejoinedMsg) tea.Cmd {
	if msg.gen != m.link.gen || !m.link.state.lost() {
		if c := msg.client; c != nil {
			return func() tea.Msg { c.Close(); return nil }
		}
		return nil
	}
	m.link.busy = false
	switch {
	case msg.err == nil:
		return m.resync(msg.client)
	case errors.Is(msg.err, ErrDaemonNewer):
		log.For("app").Warn("link.daemon_newer", "err", msg.err)
		m.exitErr = msg.err
		return tea.Quit
	case errors.Is(msg.err, daemon.ErrNoDaemon):
		// Waiting's normal case. A note that the daemon did not start
		// stays until ctrl+r.
		if m.link.spawnFails < maxSpawnFails {
			m.link.note = ""
		}
	case errors.Is(msg.err, ErrDaemonDidNotStart) && m.link.state == linkReconnecting:
		m.link.spawnFails++
		if m.link.spawnFails >= maxSpawnFails {
			log.For("app").Warn("link.spawns_failed", "fails", m.link.spawnFails)
			m.link.state = linkWaiting
			m.link.gen++
			m.link.note = fmt.Sprintf("it did not start %d times: see serve.log", m.link.spawnFails)
			return m.scheduleRejoin()
		}
		m.link.note = "it did not start (see serve.log)"
	case errors.Is(msg.err, ErrDaemonDidNotStart):
		m.link.note = "it did not start (see serve.log)"
	default:
		m.link.note = firstLine(msg.err.Error())
	}
	return m.scheduleRejoin()
}

// rejoinNote shows a running rejoin's progress as the banner's note.
func (m *home) rejoinNote(msg rejoinNoteMsg) {
	if msg.gen == m.link.gen && m.link.state.lost() {
		m.link.note = firstLine(msg.text)
	}
}

// firstLine is s up to its first newline: a banner is one row.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// resync swaps in c, a client of the daemon just joined, and brings the
// TUI up to date with it, on Update. The new client's first Sync carries
// its snapshot, which the appliers apply: the rows (kept selected by ID,
// which every daemon gives a record alike), the workspaces, the accounts
// and GitHub state. Then it closes the tabs whose workspace the daemon no
// longer serves, opens every workspace shown (the daemon's first open of
// it: its terminal and GitHub polling start), sends the UI prefs changed
// offline, has the selection published again, and, on another tmux server
// (the old one died), replaces every pane client. Drafts, overlays, the
// workbench, the review and scroll positions are left as they are.
func (m *home) resync(c *rpc.Client) tea.Cmd {
	m.core, m.conn = c, c
	m.stopCore = c.Close
	if m.send != nil {
		go forwardWakes(c.Wakes(), m.send)
	}
	m.link.gen++
	log.For("app").Info("link.rejoined", "build", c.Peer().Build, "tmux", c.Peer().Tmux)

	// A workspace registered meanwhile is served once the daemon rereads
	// the registry, as at startup.
	if err := m.core.ReloadRegistry(); err != nil {
		log.For("app").Warn("registry.reload_failed", "err", err)
	}
	cmds := []tea.Cmd{m.drainCore()}

	gone, notes := m.closeUnserved()
	cmds = append(cmds, gone)

	var failed []error
	for _, s := range m.openSlots() {
		if s.id == 0 {
			continue
		}
		if _, err := m.core.Open(s.id); err != nil {
			failed = append(failed, fmt.Errorf("reopen %s: %w", s.label(), err))
		}
	}
	cmds = append(cmds, m.drainCore())

	m.sendUnsentPrefs()
	if name := m.unsentLastUsed; name != "" {
		// Sent once: a name this daemon refuses (unregistered meanwhile)
		// is dropped, unless the daemon was unavailable to take it.
		err := m.core.SetLastUsed(name)
		if err != nil {
			log.For("app").Warn("registry.update_last_used_failed", "workspace", name, "err", err)
		}
		if err == nil || !errors.Is(err, core.ErrUnavailable) {
			m.unsentLastUsed = ""
		}
	}
	m.sentSelected = 0

	if peer := c.Peer().Tmux; peer != m.daemonTmux {
		// The old server died: every client watches a session that is
		// gone. The join pinned the new server already; pin it here too,
		// whatever joined.
		log.For("app").Info("link.tmux_server_changed", "from", m.daemonTmux, "to", peer)
		tmux.UseServer(peer)
		m.daemonTmux = peer
		cmds = append(cmds, m.releaseAllPanes())
	}
	for _, s := range m.openSlots() {
		m.ensureSlotPanes(s)
	}
	cmds = append(cmds, m.prunePanes())

	// The counters go on until the link has stayed up (stableLink): a
	// daemon that dies again soon after is no success (lose).
	m.link = link{gen: m.link.gen, attempt: m.link.attempt, spawnFails: m.link.spawnFails, upAt: m.clock()}
	m.relayout()
	m.updateTabBarStatuses()
	m.errBox.SetInfo(strings.Join(append([]string{"reconnected to the loom daemon"}, notes...), "; "))
	for _, err := range failed {
		cmds = append(cmds, m.handleError(err))
	}
	return tea.Batch(append(cmds, m.instanceChanged())...)
}

// closeUnserved closes the tabs whose workspace the daemon no longer serves
// (unregistered while the TUI was offline), or, when it shows none of
// them, goes to global mode, as it does when a classic workspace slot's
// workspace is gone. It returns the transitions' Cmds and a note for each
// workspace closed.
func (m *home) closeUnserved() (tea.Cmd, []string) {
	if len(m.slots) == 0 {
		if m.id == 0 || m.core.IsLoaded(m.id) {
			return nil, nil
		}
		note := fmt.Sprintf("%s is no longer registered; showing global", m.label())
		return m.enterGlobalMode(), []string{note}
	}
	var gone []string
	for _, s := range m.slots {
		if !m.core.IsLoaded(s.id) {
			gone = append(gone, s.name())
		}
	}
	if len(gone) == 0 {
		return nil, nil
	}
	notes := make([]string, 0, len(gone))
	for _, name := range gone {
		notes = append(notes, fmt.Sprintf("%s is no longer registered; closed its tab", name))
	}
	if len(gone) == len(m.slots) {
		return m.enterGlobalMode(), notes
	}
	var cmds []tea.Cmd
	for _, name := range gone {
		cmd, err := m.deactivateWorkspace(name)
		if err != nil {
			log.For("app").Error("link.close_unserved_failed", "workspace", name, "err", err)
		}
		cmds = append(cmds, cmd)
	}
	m.loadSlot(m.focusedSlot)
	m.tabBar.SetWorkspaces(m.slotNames(), m.focusedSlot)
	m.persistOpenList()
	return tea.Batch(cmds...), notes
}

// keepUnsentPrefs applies p, the focused slot's UI prefs changed offline,
// and keeps it to be sent once a daemon is joined (resync).
func (m *home) keepUnsentPrefs(p config.UIPrefs) {
	m.info.UIPrefs = p
	unsent := p.Clone()
	m.unsentPrefs = &unsent
}

// sendUnsentPrefs sends every open slot's UI prefs changed offline to the
// daemon just joined, after its snapshot: the TUI's own value wins. One
// that fails is kept, for the next daemon.
func (m *home) sendUnsentPrefs() {
	for _, s := range m.openSlots() {
		if s.unsentPrefs == nil {
			continue
		}
		p := s.unsentPrefs.Clone()
		if err := m.core.SetUIPrefs(s.id, p); err != nil {
			log.For("app").Warn("ui_prefs_save_failed", "workspace", s.label(), "err", err)
			continue
		}
		s.unsentPrefs = nil
		s.info.UIPrefs = p
	}
}

// unsentPrefSlots names the open slots whose UI prefs changed offline and
// were never sent, for the quit's log.
func (m *home) unsentPrefSlots() []string {
	var names []string
	for _, s := range m.openSlots() {
		if s.unsentPrefs != nil {
			names = append(names, s.label())
		}
	}
	return names
}

// releaseAllPanes drops every pane client, the agents' and every slot's
// terminal pane's, and returns the Cmd closing them (the sessions are left
// alone): a rejoined daemon on another tmux server has its sessions there.
func (m *home) releaseAllPanes() tea.Cmd {
	cmds := []tea.Cmd{releaseClientsCmd(attachedClients(m.panes.Retain(nil)))}
	for _, s := range m.openSlots() {
		if s.splitPane != nil {
			cmds = append(cmds, releaseClientsCmd(attachedClients(s.splitPane.Terminal().DetachAll())))
		}
	}
	return tea.Batch(cmds...)
}

// relayout lays the screen out again at its last size, for a change of the
// top chrome's height (the banner shown or hidden).
func (m *home) relayout() {
	if m.lastWidth > 0 {
		m.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: m.lastWidth, Height: m.lastHeight})
	}
}

// bannerHeight is the banner's rows: one while offline.
func (m *home) bannerHeight() int {
	if m.offline() {
		return 1
	}
	return 0
}

// bannerText says what the link is while offline, with the last rejoin's
// note; "" while connected.
func (m *home) bannerText() string {
	var parts []string
	switch m.link.state {
	case linkStopping:
		return "⚠ the loom daemon is stopping: finishing its jobs in flight"
	case linkWaiting:
		parts = []string{"⚠ the loom daemon stopped: waiting for one to start (ctrl+r starts it)"}
		if m.link.note != "" {
			parts = append(parts, m.link.note)
		}
	case linkReconnecting:
		n := m.link.attempt
		if !m.link.busy {
			n++
		}
		parts = []string{fmt.Sprintf("⚠ lost the loom daemon: reconnecting (attempt %d)", n)}
		if m.link.note != "" {
			parts = append(parts, m.link.note)
		}
		parts = append(parts, "ctrl+r retries now")
	default:
		return ""
	}
	return strings.Join(parts, " · ")
}

// bannerView renders the banner, one row as wide as the screen; "" while
// connected.
func (m *home) bannerView() string {
	text := m.bannerText()
	if text == "" {
		return ""
	}
	if m.lastWidth > 0 && runewidth.StringWidth(text) > m.lastWidth {
		text = runewidth.Truncate(text, m.lastWidth, "…")
	}
	return lipgloss.PlaceHorizontal(m.lastWidth, lipgloss.Left, bannerStyle.Render(text))
}
