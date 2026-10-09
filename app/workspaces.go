package app

import (
	"errors"
	"fmt"
	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/aidan-bailey/loom/ui"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
)

// workspaceSlot is the TUI's view of one loaded workspace: the model's
// view of the workspace (info, named by id) plus the view state over it,
// the rail, the split pane and the workbench. The model (core) owns the
// workspace, its instances, its storage, config and state; the slot holds
// only the copies the model publishes. home embeds the focused slot, so
// m.list, m.splitPane, m.workbench and the accessors below resolve to the
// focused slot's. Slots are always handled by pointer.
type workspaceSlot struct {
	// id names the workspace this slot shows; 0 only in bare test homes.
	id core.WorkspaceID
	// info is the workspace as the model last published it
	// (core.WorkspacesChanged), or as this slot last wrote it
	// (syncWorkspaces, mutateUIPrefs). Read it through the accessors below.
	info core.WorkspaceView
	// views is this workspace's instances as the model last published them
	// (core.ViewsChanged), in display order. Read them through
	// slotRows/rowsOf, which add the TUI's overlays.
	views []core.InstanceView
	// list is the session rail; it reads the slot's rows (slotRows).
	list *ui.List
	// splitPane displays the agent and terminal panes with diff overlay.
	splitPane *ui.SplitPane
	// workbench renders the right content panel when viewMode is
	// viewWorkbench; the left half is splitPane with its terminal hidden.
	// It pairs with this slot's splitPane (its terminal tab shows the
	// slot's shared TerminalPane). Non-nil for every slot.
	workbench *ui.Workbench
}

// name is the workspace's registered name, "" for the global context.
func (s *workspaceSlot) name() string { return s.info.Name }

// label names the workspace in notices: its name, or "global".
func (s *workspaceSlot) label() string { return s.info.Label }

// settings is a copy of the workspace's config.json, as last published.
func (s *workspaceSlot) settings() config.Settings { return s.info.Settings }

// uiPrefs is a deep copy of the workspace's UI prefs, safe to change: the
// change reaches state.json only through mutateUIPrefs.
func (s *workspaceSlot) uiPrefs() config.UIPrefs { return s.info.UIPrefs.Clone() }

// recovery is the summary of the workspace's last orphan reconcile.
func (s *workspaceSlot) recovery() core.RecoverySummary { return s.info.Recovery }

// activateWorkspace opens the workspace def names as a new tab (openTab):
// the served workspace of that name, found after a reread of the registry
// when the model does not serve it yet (openNamed).
func (m *home) activateWorkspace(def config.Workspace) (tea.Cmd, error) {
	id, err := m.servedID(def.Name)
	if err != nil {
		return nil, err
	}
	return m.openTab(id)
}

// openTab opens the served workspace id as a new tab (core's Open: a load
// that failed retried; on its first open, its workspace terminal) and
// builds its view. A workspace this TUI failed to open at startup is one
// no longer (failedOpen). The first tab opened from classic/global mode
// takes focus at once (see the invariant on home.workspaceSlot); later
// ones open in the background, and callers that want to show one focus it
// with loadSlot.
//
// Focusing the first tab drops the classic slot; the returned Cmd releases
// its pane clients (releaseSlotCmd, prunePanes) and is nil otherwise.
// Callers must return it (or, before the program runs, run it).
func (m *home) openTab(id core.WorkspaceID) (tea.Cmd, error) {
	if len(m.slots) == 0 {
		// Leaving classic mode: tear the classic slot down (workbench,
		// pending split ratios) while its UI prefs are still the ones m.*
		// writes; loadSlot's own teardown then finds nothing left to do.
		m.leaveFocusedSlot()
	}
	v, err := m.core.Open(id)
	if err != nil {
		return nil, err
	}
	m.failedOpen = slices.DeleteFunc(m.failedOpen, func(n string) bool { return n == v.Name })
	// The open's notices (a workspace terminal launched without remote
	// control) land before anything the caller shows next, as they did when
	// the load set them itself.
	notices := m.drainCore()
	m.slots = append(m.slots, m.newSlotView(v))
	var release tea.Cmd
	if len(m.slots) == 1 {
		// Leaving classic mode: the classic slot is not in m.slots, so the
		// invariant needs the new tab focused before we return. loadSlot
		// runs the classic slot's workbench cleanup and ratio flush first;
		// the slot is then dropped, so its attach clients go too.
		classic := m.workspaceSlot
		m.loadSlot(0)
		release = tea.Batch(releaseSlotCmd(classic), m.prunePanes())
	}
	return tea.Batch(notices, release), nil
}

// servedNamed is the view of the served workspace called name ("" for the
// global one), from the published workspaces; false when the model serves
// none by that name.
func (m *home) servedNamed(name string) (core.WorkspaceView, bool) {
	for _, v := range m.core.Workspaces() {
		if v.Name == name {
			return v, true
		}
	}
	return core.WorkspaceView{}, false
}

// servedID is the ID of the served workspace called name, for a client
// that opens it by name (the picker). One the model does not serve yet
// (registered by another process since the registry was last read) is
// looked for again after a reread (ReloadRegistry), which serves it. One
// whose directory the model serves under another name is refused
// (servedFor): the TUI keys its tabs, the picker and the open list it
// persists on names, so a tab under the other name would be closed by the
// next picker commit and dropped from the open list, which keeps
// registered names only. A twin (the other name is registered too) is to
// be opened by that name; a rename while the daemon runs waits for its
// restart (`loom serve stop`, then loom), which serves the workspace under
// its new name (until a rename can reload a workspace, daemon stage 3C).
func (m *home) servedID(name string) (core.WorkspaceID, error) {
	v, twin, ok := m.servedFor(name)
	if !ok {
		if err := m.core.ReloadRegistry(); err != nil {
			return 0, fmt.Errorf("workspace %s: %w", labelOf(name), err)
		}
		v, twin, ok = m.servedFor(name)
	}
	switch {
	case !ok:
		return 0, fmt.Errorf("workspace %s is not registered", labelOf(name))
	case v.Name == name:
		return v.ID, nil
	case twin && v.Name == "":
		return 0, fmt.Errorf("%s shares its config dir with the global workspace, which loom serves: pick Global", name)
	case twin:
		return 0, fmt.Errorf("%s is the same directory as %s, which loom serves: open %s", name, v.Name, v.Name)
	default:
		return 0, fmt.Errorf("%s is the workspace loom serves as %s (renamed while the loom daemon runs, or registered twice for one directory): stop the daemon (`loom serve stop`), then start loom, to open it as %s",
			name, labelOf(v.Name), name)
	}
}

// servedFor is the served workspace the registered name stands for: the
// one called name, else the one whose directory is name's registry
// entry's, which the model serves under another name (it serves one
// workspace per directory, under the name it first served it by). twin
// reports, for the latter, that the other name is still registered (one
// directory registered twice, as through a symlink) or is the global
// workspace's (a workspace registered at $HOME, whose config dir is the
// global one); otherwise name is the workspace's new name (renamed while
// loom runs). false when the model serves neither.
func (m *home) servedFor(name string) (v core.WorkspaceView, twin, ok bool) {
	if v, ok := m.servedNamed(name); ok || name == "" {
		return v, false, ok
	}
	reg := m.core.Registry()
	registered := func(n string) bool {
		return slices.ContainsFunc(reg.Workspaces, func(w config.Workspace) bool { return w.Name == n })
	}
	i := slices.IndexFunc(reg.Workspaces, func(w config.Workspace) bool { return w.Name == name })
	if i < 0 {
		return core.WorkspaceView{}, false, false
	}
	dir := config.WorkspaceConfigDir(&reg.Workspaces[i])
	for _, v := range m.core.Workspaces() {
		if v.ConfigDir != "" && sameDir(v.ConfigDir, dir) {
			return v, v.Name == "" || registered(v.Name), true
		}
	}
	return core.WorkspaceView{}, false, false
}

// twinOf is the served workspace whose directory the registered name's is,
// when the model serves it under another, still-registered name, or as the
// global workspace (servedFor). At startup such a name is that workspace:
// the TUI shows the twin in its place (twinNote).
func (m *home) twinOf(name string) (core.WorkspaceView, bool) {
	v, twin, ok := m.servedFor(name)
	return v, ok && twin
}

// twinNote is the startup's note that name is shown as served, its twin
// (twinOf), "" being the global workspace.
func twinNote(name, served string) string {
	if served == "" {
		return fmt.Sprintf("%s shares its config dir with the global workspace; showing global", name)
	}
	return fmt.Sprintf("%s is the same directory as %s; showing %s", name, served, served)
}

// addNote adds note to the startup's info notes once, logging it the first
// time.
func addNote(notes []string, note string) []string {
	if slices.Contains(notes, note) {
		return notes
	}
	log.For("app").Info("workspace.startup_note", "note", note)
	return append(notes, note)
}

// showStartupInfo shows the startup's info line: its notes (a twin shown in
// a name's place), then the recovery summary s, on one line.
func (m *home) showStartupInfo(notes []string, s core.RecoverySummary) {
	if !s.Empty() {
		notes = append(slices.Clone(notes), s.String())
	}
	if len(notes) > 0 {
		m.errBox.SetInfo(strings.Join(notes, " · "))
	}
}

// sameDir reports whether a and b name one directory, as the model decides
// it serves one workspace per directory: equal once their symlinks are
// resolved (session.CanonicalPath), or once cleaned.
func sameDir(a, b string) bool {
	if ca := session.CanonicalPath(a); ca != "" && ca == session.CanonicalPath(b) {
		return true
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// openNamed opens the served workspace called name (servedID, then core's
// Open) and returns its view.
func (m *home) openNamed(name string) (core.WorkspaceView, error) {
	id, err := m.servedID(name)
	if err != nil {
		return core.WorkspaceView{}, err
	}
	return m.core.Open(id)
}

// deactivateWorkspace closes a workspace tab by name: the TUI stops
// showing it, while the model keeps serving it. The last open tab is never
// closed: leaving no tab means leaving workspace mode, and only
// enterGlobalMode builds the slot that must take focus then.
//
// The invariant on home.workspaceSlot holds on return. Closing the
// focused tab refocuses, via loadSlot, the tab that slides into its index
// (or the new last tab).
//
// The returned Cmd releases the closed slot's pane clients
// (releaseSlotCmd, prunePanes; nil when none is attached) and must be
// returned to the runtime.
func (m *home) deactivateWorkspace(name string) (tea.Cmd, error) {
	idx := slices.IndexFunc(m.slots, func(s *workspaceSlot) bool { return s.name() == name })
	if idx == -1 {
		return nil, nil
	}
	if len(m.slots) == 1 {
		return nil, fmt.Errorf("cannot close %s, the last open workspace: return to global mode instead", name)
	}
	slot := m.slots[idx]
	wasFocused := idx == m.focusedSlot
	m.slots = slices.Delete(m.slots, idx, idx+1)
	switch {
	case wasFocused:
		// m.workspaceSlot still points at the closed slot, so loadSlot's
		// departing-slot cleanup (workbench, ratio flush) lands on it.
		m.loadSlot(min(idx, len(m.slots)-1))
	case idx < m.focusedSlot:
		m.focusedSlot--
	}
	return tea.Batch(releaseSlotCmd(slot), m.prunePanes()), nil
}

// releaseSlotCmd returns a Cmd that releases the attach clients a dropped
// slot's terminal pane holds on each loom_term_* shell it has shown (the
// shells keep running). The agent panes' clients belong to the registry,
// which every drop site prunes (prunePanes). Returns nil when nothing is
// attached.
func releaseSlotCmd(slot *workspaceSlot) tea.Cmd {
	if slot == nil || slot.splitPane == nil {
		return nil
	}
	return releaseClientsCmd(attachedClients(slot.splitPane.Terminal().DetachAll()))
}

// attachedClients keeps the sessions whose attach client is open.
func attachedClients(sessions []*tmux.TmuxSession) []attachedClient {
	var out []attachedClient
	for _, ts := range sessions {
		if ts.PtmxAlive() {
			out = append(out, attachedClient{name: ts.SessionName(), ts: ts})
		}
	}
	return out
}

// attachedClient is a tmux session whose attach client a release closes;
// name labels it in logs.
type attachedClient struct {
	name string
	ts   *tmux.TmuxSession
}

// releaseClientsCmd returns a Cmd that closes each client's attach PTY —
// PTY, output pump and emulator — leaving the tmux sessions running, or
// nil when there are none.
//
// A client is only ever closed here, and only after Retain or Replace has
// removed it from the registry (or its terminal pane has detached it), so
// nothing re-attaches or closes it while it closes: a TmuxSession's attach
// lifecycle (Restore, PausePreview) must never run on two goroutines at
// once.
//
// The close runs in the Cmd, off the Update goroutine: PausePreview waits
// — up to the pump-exit timeout, per session — for the output pump to
// exit, and the pump delivers pane events through tea.Program.Send, which
// blocks until Update returns. The clients close concurrently, so N
// clients of live sessions cost about one pump-exit timeout rather than
// N.
func releaseClientsCmd(clients []attachedClient) tea.Cmd {
	if len(clients) == 0 {
		return nil
	}
	return func() tea.Msg {
		var wg sync.WaitGroup
		for _, c := range clients {
			wg.Add(1)
			go func(c attachedClient) {
				defer wg.Done()
				if err := c.ts.PausePreview(); err != nil {
					log.For("app").Warn("slot_release.preview_close_failed", "session", c.name, "err", err)
				}
			}(c)
		}
		wg.Wait()
		return nil
	}
}

// openSlots returns every loaded workspace slot: the open tabs, or in
// classic/global mode the classic slot alone. The focused slot is always
// among them.
func (m *home) openSlots() []*workspaceSlot {
	if len(m.slots) == 0 {
		return []*workspaceSlot{m.workspaceSlot}
	}
	return m.slots
}

// checkSlotInvariant reports a violation of the embedded-focused-slot
// invariant (see home.workspaceSlot): the focused slot is never nil, no
// slot appears in m.slots twice, no two tabs show the same workspace, every
// slot shows a workspace the model serves, and with tabs open the focused
// slot is m.slots[m.focusedSlot]. In classic/global mode (no tabs) the focused
// slot is the classic slot, outside m.slots by construction.
// Tests call it after every slot transition.
func (m *home) checkSlotInvariant() error {
	if m.workspaceSlot == nil {
		return errors.New("slot invariant: focused slot is nil")
	}
	seen := make(map[*workspaceSlot]int, len(m.slots))
	shows := make(map[core.WorkspaceID]int, len(m.slots))
	for i, s := range m.slots {
		if s == nil {
			return fmt.Errorf("slot invariant: m.slots[%d] is nil", i)
		}
		if j, dup := seen[s]; dup {
			return fmt.Errorf("slot invariant: m.slots[%d] and m.slots[%d] are the same slot", j, i)
		}
		seen[s] = i
		if j, dup := shows[s.id]; dup && s.id != 0 {
			return fmt.Errorf("slot invariant: m.slots[%d] and m.slots[%d] show the same workspace", j, i)
		}
		shows[s.id] = i
	}
	// Every slot shows a workspace the model serves: the tabs, or with
	// none open the classic slot. A bare test home has no model.
	if m.core != nil {
		for i, s := range m.openSlots() {
			if !m.core.IsLoaded(s.id) {
				if len(m.slots) == 0 {
					return errors.New("slot invariant: the classic slot shows no workspace the model serves")
				}
				return fmt.Errorf("slot invariant: m.slots[%d] shows no workspace the model serves", i)
			}
		}
	}
	if len(m.slots) == 0 {
		return nil
	}
	if m.focusedSlot < 0 || m.focusedSlot >= len(m.slots) {
		return fmt.Errorf("slot invariant: focusedSlot %d out of range [0,%d)", m.focusedSlot, len(m.slots))
	}
	if m.workspaceSlot != m.slots[m.focusedSlot] {
		if i, ok := seen[m.workspaceSlot]; ok {
			return fmt.Errorf("slot invariant: focused slot is m.slots[%d], but focusedSlot is %d", i, m.focusedSlot)
		}
		return fmt.Errorf("slot invariant: focused slot is not in m.slots (focusedSlot %d)", m.focusedSlot)
	}
	return nil
}

// leaveFocusedSlot runs the departing-slot teardown every focus change
// needs, while the focused slot's splitPane/appState are still the ones
// m.* resolves to. It copies nothing: the slot owns its state. loadSlot
// runs it too, so a separate call is only needed where focus leaves by
// another route (enterGlobalMode) or the teardown must come before other
// work (applyWorkspaceToggle, handleQuit).
func (m *home) leaveFocusedSlot() {
	// Workbench mode does not survive a slot switch: tear it down while
	// the departing slot's splitPane/appState are still active, so the
	// terminal-hidden restore and ratio flush land on the right slot.
	m.cleanupWorkbench()
	// Flush pending split-ratio saves into THIS slot's state.json before
	// the slot swap: the pending map would otherwise survive the switch
	// and the armed throttle tick would drain slot A's title→ratio into
	// slot B's prefs — a durable wrong value when workspaces share a
	// title. One synchronous write per slot switch is mutateUIPrefs'
	// blessed rare-toggle case; the in-flight tick then finds the map
	// empty and simply disarms.
	m.flushPendingRatioSaves()
}

// loadSlot focuses slot idx: it becomes the embedded m.workspaceSlot, and
// the tab bar, layout and UI prefs follow it. It is the focus-change
// choke point, and runs leaveFocusedSlot's teardown on the departing
// slot first so that no path can skip it. Loading the slot that is
// already focused only refreshes the tab bar and peer sections.
func (m *home) loadSlot(idx int) {
	if idx < 0 || idx >= len(m.slots) {
		return
	}
	if slot := m.slots[idx]; slot == m.workspaceSlot {
		// Nothing departs, only the tab set around the slot may have
		// changed: openTab focused the first tab and the caller
		// now focuses it again, a toggle whose deactivation already
		// refocused ends by re-focusing, or tabs came and went beside it.
		// Skip the teardown and the layout pass below (it loops tmux
		// SetDetachedSize); the tab bar keeps its height with tabs open.
		m.focusedSlot = idx
		m.tabBar.SetWorkspaces(m.slotNames(), idx)
		m.refreshPeerSections()
		return
	}
	// Departing-slot teardown (idempotent where leaveFocusedSlot already
	// ran): the workbench cleanup and the pending split-ratio flush must
	// land on the slot being left, and m.* resolves to it until the swap
	// below. Focus-changing callers rely on this rather than running the
	// teardown themselves.
	m.leaveFocusedSlot()
	slot := m.slots[idx]
	m.focusedSlot = idx
	m.workspaceSlot = slot
	m.list.SetWorkspaceName(slot.name())
	m.tabBar.SetWorkspaces(m.slotNames(), m.focusedSlot)
	// Resize immediately using the now-correct tab bar height. Without this,
	// the first View() after a workspace switch uses components pre-sized when
	// the tab bar had 0 names (height=0 instead of 3), producing 3 extra lines
	// that Bubble Tea clips from the top, cutting off the workspace tab bar.
	// applyUIPrefs below runs the full layout whenever appState exists, so
	// this interim resize survives only for bare test homes — a redundant
	// SetSize here would loop tmux SetDetachedSize subprocess calls twice
	// per workspace switch.
	if m.id == 0 && m.lastWidth > 0 && m.lastHeight > 0 {
		listWidth := int(float32(m.lastWidth) * ui.ListWidthPercent)
		paneWidth := m.lastWidth - listWidth
		contentHeight := m.lastHeight - m.topChromeHeight() - 2
		m.list.SetSize(listWidth, contentHeight)
		m.splitPane.SetSize(paneWidth, contentHeight)
	}
	m.refreshPeerSections()
	// The freshly-loaded slot's components carry no layout prefs yet;
	// apply the persisted rail/terminal/ratio state (re-runs the
	// window-size layout when sized).
	m.applyUIPrefs()
}

// applyWorkspaceToggle diffs the current slots against the desired list,
// activating and deactivating workspaces as needed.
// Activates new workspaces first so that if activation fails, the old
// workspace is still available.
//
// An empty desired list from global mode switches nothing: see
// stayInGlobalMode. Nothing needs saving first: the model keeps serving
// every workspace a transition stops showing.
func (m *home) applyWorkspaceToggle(desired []config.Workspace) tea.Cmd {
	if len(desired) == 0 && m.inGlobalMode() {
		return m.stayInGlobalMode()
	}
	if len(m.slots) > 0 {
		m.leaveFocusedSlot()
	}

	// Empty desired = explicit return to global mode (e.g. user picked
	// the Global row in the mid-session picker). Handled by a dedicated
	// helper because the inverse transition builds the global slot and
	// clears the registry's open list.
	if len(desired) == 0 {
		return m.enterGlobalMode()
	}

	desiredNames := make(map[string]bool, len(desired))
	for _, ws := range desired {
		desiredNames[ws.Name] = true
	}
	// A failed-to-restore workspace left unchecked was explicitly closed.
	m.failedOpen = slices.DeleteFunc(m.failedOpen, func(n string) bool { return !desiredNames[n] })

	var activationErrors []string
	var deactivationErrors []string
	var releases []tea.Cmd

	// 1. Activate new workspaces first (safe — adds to slots without removing).
	currentNames := make(map[string]bool, len(m.slots))
	for _, slot := range m.slots {
		currentNames[slot.name()] = true
	}
	for _, ws := range desired {
		if !currentNames[ws.Name] {
			release, err := m.activateWorkspace(ws)
			if err != nil {
				activationErrors = append(activationErrors,
					fmt.Sprintf("%s: %v", ws.Name, err))
			}
			releases = append(releases, release)
		}
	}

	// 2. Deactivate slots not in desired (reverse order to keep indices stable).
	// Skipped when no desired workspace is open (every activation failed):
	// closing the rest would leave no tab at all, which is enterGlobalMode's
	// transition, not this one — keep the open tabs and report the failures.
	if slices.ContainsFunc(m.slots, func(s *workspaceSlot) bool { return desiredNames[s.name()] }) {
		for i := len(m.slots) - 1; i >= 0; i-- {
			if !desiredNames[m.slots[i].name()] {
				release, err := m.deactivateWorkspace(m.slots[i].name())
				if err != nil {
					deactivationErrors = append(deactivationErrors,
						fmt.Sprintf("%s: %v", m.slots[i].name(), err))
				}
				releases = append(releases, release)
			}
		}
	}

	// 3. Re-focus the focused slot so the tab bar and peer sections
	// reflect the new set. Activation and deactivation have already
	// focused the right slot (running any layout pass), so this is
	// loadSlot's cheap already-focused path.
	m.loadSlot(m.focusedSlot)

	m.tabBar.SetWorkspaces(m.slotNames(), m.focusedSlot)
	m.persistOpenList()
	if len(m.slots) > 0 {
		m.showRecoverySummary(m.slots[m.focusedSlot].recovery())
	}

	// Point the panes and menu at the (possibly new) selection, so none
	// of them keeps a dropped instance, then release the dropped slots.
	cmds := append([]tea.Cmd{tea.RequestWindowSize, m.instanceChanged()}, releases...)

	// 4. Surface activation/deactivation errors to the user.
	var msgs []string
	if len(activationErrors) > 0 {
		msgs = append(msgs, fmt.Sprintf("failed to activate: %s",
			strings.Join(activationErrors, "; ")))
	}
	if len(deactivationErrors) > 0 {
		msgs = append(msgs, fmt.Sprintf("failed to deactivate: %s",
			strings.Join(deactivationErrors, "; ")))
	}
	if len(msgs) > 0 {
		cmds = append(cmds, m.handleError(fmt.Errorf("%s", strings.Join(msgs, "; "))))
	}
	return tea.Batch(cmds...)
}

// inGlobalMode reports whether the focused slot is the global slot: no tab
// is open, and the classic slot's context names no workspace — the global
// context classic startup and enterGlobalMode give it (a nil context, in
// bare test homes, counts too). A classic slot for a named workspace
// (`loom --workspace`, or launched inside one) is not: leaving it for
// global mode is a real transition.
func (m *home) inGlobalMode() bool {
	return len(m.slots) == 0 && m.name() == ""
}

// stayInGlobalMode applies a picker commit with nothing selected (the
// Global row, or every workspace unchecked) made from global mode. The
// focused slot already is the global slot, so nothing is rebuilt: a
// rebuild would attach a second client to every live session (then
// release the first). It closes the workspaces that failed to restore
// (failedOpen), the only thing such a commit can change, and persists the
// now-empty open list, after the teardown every picker commit runs on the
// focused slot (leaveFocusedSlot).
func (m *home) stayInGlobalMode() tea.Cmd {
	m.leaveFocusedSlot()
	m.failedOpen = nil
	m.persistOpenList()
	return tea.RequestWindowSize
}

// enterGlobalMode transitions from workspace-tab mode, or from a classic
// workspace slot, to global (no-workspace) mode; from global mode itself
// applyWorkspaceToggle runs stayInGlobalMode instead. It opens the global
// workspace (core's Open: config.GlobalWorkspaceContext's, the one global
// startup shows, so both resolve LOOM_GLOBAL_DIR, else ~/.loom; never
// LOOM_HOME), which the model has served since it booted, and builds the
// classic slot over it. The workspaces it stops showing stay served.
// Returning to global mode closes the workspaces that failed to restore
// (failedOpen) and clears the registry's open list, so the next launch
// lands in global mode rather than restoring tabs the user just closed.
//
// Tmux note: session names are loom_<title>, keyed by title alone, so a
// global instance whose title matches one in a closing tab shares its tmux
// session, and the global slot attaches a second client to it while the
// tab's is still attached. The overlap is brief: the release Cmds returned
// below detach every dropped instance's client.
//
// Fails closed, with nothing switched, when the global workspace's load
// failed and fails again: no tab closed, the list unswapped and the
// registry unchanged (workbench mode may already have been exited —
// applyWorkspaceToggle runs leaveFocusedSlot first).
func (m *home) enterGlobalMode() tea.Cmd {
	// Picker escape hatch (W → Global row) from a classic workspace slot
	// reaches here with no leaveFocusedSlot of its own — clean up workbench
	// residue (wbRatio flush, split-terminal restore) and flush pending
	// ratios while the departing slot is still the one m.* writes, or
	// handleQuit later flushes them into the global state.json.
	m.leaveFocusedSlot()

	global, err := m.openNamed("")
	if err != nil {
		return m.handleError(fmt.Errorf("failed to load global sessions (staying in workspace mode): %w", err))
	}
	notices := m.drainCore()

	// The global slot keeps the departing slot's splitPane and workbench:
	// the panes are sized and wired already, and the closed tab no longer
	// uses them.
	view := &workspaceSlot{id: global.ID, info: global, splitPane: m.splitPane, workbench: m.workbench}
	view.list = ui.NewList(&m.spinner, slotRows{m, view})
	view.list.SetPanes(m.panes)
	m.seedViews(view)

	// Everything shown so far is dropped: every tab, or — global mode
	// entered from a classic workspace slot — that slot. The focused one's
	// panes live on in the global slot.
	dropped, carried := m.openSlots(), m.workspaceSlot
	m.slots = nil
	m.focusedSlot = 0
	m.workspaceSlot = view
	m.ensureSlotPanes(view)
	m.failedOpen = nil
	m.persistOpenList()

	m.tabBar.SetWorkspaces(nil, 0)

	// Apply the global state's persisted layout prefs so global mode
	// renders with its own rail/terminal/ratio settings — not the
	// previous workspace's — and the display agrees with where
	// mutateUIPrefs will write. When sized, this re-runs the full
	// window-size layout, which also resizes the components for the
	// now-zero-height tab bar.
	m.applyUIPrefs()

	// The carried terminal pane still caches clients for the closed tab's
	// shells; keep only those the global list can show again.
	keep := make(map[string]bool)
	for _, inst := range m.list.GetInstances() {
		keep[inst.Title] = true
	}
	staleTerminals := releaseClientsCmd(attachedClients(m.splitPane.Terminal().DetachExcept(keep)))

	m.showRecoverySummary(global.Recovery)

	// Point the carried-over panes and the menu at the global selection,
	// so none of them keeps a dropped instance, then release the drops.
	cmds := []tea.Cmd{notices, tea.RequestWindowSize, m.instanceChanged(), staleTerminals, m.prunePanes()}
	for _, slot := range dropped {
		if slot == carried {
			continue // its panes live on; prunePanes closes its agents' clients
		}
		cmds = append(cmds, releaseSlotCmd(slot))
	}
	return tea.Batch(cmds...)
}

// sessionToTabStatus maps a session.Status to the corresponding ui.TabStatus.
func sessionToTabStatus(s session.Status) ui.TabStatus {
	switch s {
	case session.Prompting:
		return ui.TabStatusPrompting
	case session.Running:
		return ui.TabStatusRunning
	case session.Ready:
		return ui.TabStatusReady
	case session.Loading:
		return ui.TabStatusLoading
	case session.Paused:
		return ui.TabStatusPaused
	default:
		return ui.TabStatusNone
	}
}

// updateTabBarStatuses checks each slot for instances and updates the tab bar's
// status indicators. The highest-priority status across all instances in a slot wins.
// Precedence (high→low): Prompting > Running > Ready > Loading > Paused > None.
func (m *home) updateTabBarStatuses() {
	if len(m.slots) == 0 {
		return
	}
	statuses := make([]ui.TabStatus, len(m.slots))
	for i, slot := range m.slots {
		for _, inst := range slot.list.GetInstances() {
			if !inst.Started {
				continue
			}
			ts := sessionToTabStatus(inst.Status)
			if ts > statuses[i] {
				statuses[i] = ts
			}
		}
	}
	m.tabBar.SetStatuses(statuses)
	m.refreshPeerSections()
}

// refreshPeerSections rebuilds the rail's peer-workspace summaries from
// the non-focused slots' lists (the focused slot is skipped: it is the
// rail's own workspace, not a peer). Main-goroutine only (reads slot
// lists, which are only mutated there).
func (m *home) refreshPeerSections() {
	if len(m.slots) <= 1 {
		m.list.SetPeerSections(nil)
		return
	}
	peers := make([]ui.PeerSection, 0, len(m.slots)-1)
	for i, slot := range m.slots {
		if i == m.focusedSlot {
			continue
		}
		peers = append(peers, m.peerSectionFor(slot))
	}
	m.list.SetPeerSections(peers)
}

// persistFocusedWorkspace writes the currently focused slot's name to
// LastUsed so the next launch focuses the same tab.
func (m *home) persistFocusedWorkspace() {
	if m.focusedSlot < 0 || m.focusedSlot >= len(m.slots) {
		return
	}
	if err := m.core.SetLastUsed(m.slots[m.focusedSlot].name()); err != nil {
		log.For("app").Error("persist_focused_workspace_failed", "err", err)
	}
}

// slotNames returns the names of all active workspace slots — the set
// the tab bar shows, and the head of the open list (openList).
func (m *home) slotNames() []string {
	names := make([]string, len(m.slots))
	for i, slot := range m.slots {
		names[i] = slot.name()
	}
	return names
}

// openList is the open list this TUI persists (persistOpenList) and the
// workspace picker shows checked: its tabs' names, in tab order, then the
// workspaces it failed to open and keeps open (failedOpen).
func (m *home) openList() []string {
	names := m.slotNames()
	for _, name := range m.failedOpen {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// persistOpenList writes this TUI's open list to the registry
// (core.Core.PersistOpenList), so the next launch restores it.
func (m *home) persistOpenList() { m.core.PersistOpenList(m.openList()) }
