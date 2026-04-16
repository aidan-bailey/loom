package app

import (
	"bytes"
	"claude-squad/config"
	"claude-squad/keys"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/ui"
	"claude-squad/ui/overlay"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const GlobalInstanceLimit = 10

var inlineAttachHintStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}).
	Bold(true)

var statusLineStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#999999", Dark: "#555555"})

// Run is the main entrypoint into the application.
// wsCtx is the resolved workspace context; nil means global.
// registry is passed through for the startup workspace picker.
// appConfig is the pre-loaded config from the resolved workspace directory.
func Run(ctx context.Context, wsCtx *config.WorkspaceContext, registry *config.WorkspaceRegistry, appConfig *config.Config, program string, autoYes bool, pendingDir string) error {
	p := tea.NewProgram(
		newHome(ctx, wsCtx, registry, appConfig, program, autoYes, pendingDir),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(), // Mouse scroll
	)
	_, err := p.Run()
	return err
}

// metadataResult holds I/O results for one instance from the parallel
// metadata tick. Written by goroutine; status updates applied on main thread.
type metadataResult struct {
	instance  *session.Instance
	tmuxAlive bool
	updated   bool
	hasPrompt bool
	diffErr   error
}

type state int

const (
	stateDefault state = iota
	// stateNew is the state when the user is creating a new instance.
	stateNew
	// statePrompt is the state when the user is entering a prompt.
	statePrompt
	// stateHelp is the state when a help screen is displayed.
	stateHelp
	// stateConfirm is the state when a confirmation modal is displayed.
	stateConfirm
	// stateWorkspace is the state when the workspace picker is displayed.
	stateWorkspace
	// stateQuickInteract is the state when the quick input bar is displayed.
	stateQuickInteract
	// stateInlineAttach is the state when keystrokes are forwarded to the tmux session
	// while the UI remains visible.
	stateInlineAttach
)

// workspaceSlot bundles per-workspace state so multiple workspaces can be
// loaded in memory simultaneously.
type workspaceSlot struct {
	wsCtx     *config.WorkspaceContext
	storage   *session.Storage
	appConfig *config.Config
	appState  config.AppState
	list      *ui.List
	splitPane *ui.SplitPane
}

type home struct {
	ctx context.Context

	// -- Storage and Configuration --

	program string
	autoYes bool

	// storage is the interface for saving/loading data to/from the app's state
	storage *session.Storage
	// appConfig stores persistent application configuration
	appConfig *config.Config
	// appState stores persistent application state like seen help screens
	appState config.AppState

	// -- State --

	// state is the current discrete state of the application
	state state
	// newInstanceFinalizer is called when the state is stateNew and then you press enter.
	// It registers the new instance in the list after the instance has been started.
	newInstanceFinalizer func()

	// promptAfterName tracks if we should enter prompt mode after naming
	promptAfterName bool

	// keySent is used to manage underlining menu items
	keySent bool

	// -- UI Components --

	// list displays the list of instances
	list *ui.List
	// menu displays the bottom menu
	menu *ui.Menu
	// splitPane displays the agent and terminal panes with diff overlay
	splitPane *ui.SplitPane
	// quickInputBar displays the inline input bar for quick interactions
	quickInputBar *ui.QuickInputBar
	// errBox displays error messages
	errBox *ui.ErrBox
	// global spinner instance. we plumb this down to where it's needed
	spinner spinner.Model
	// textInputOverlay handles text input with state
	textInputOverlay *overlay.TextInputOverlay
	// textOverlay displays text information
	textOverlay *overlay.TextOverlay
	// confirmationOverlay displays confirmation modals
	confirmationOverlay *overlay.ConfirmationOverlay
	// workspacePicker displays the workspace selection overlay
	workspacePicker *overlay.WorkspacePicker
	// pendingAction stores the action to execute after confirmation
	pendingAction tea.Cmd
	// pendingPreAction runs synchronously in the main goroutine before the
	// pendingAction Cmd is dispatched. Used to set Deleting status immediately.
	pendingPreAction func()
	// pendingDir is the directory path awaiting workspace registration confirmation
	pendingDir string

	// -- Workspace slots --

	// activeCtx is the WorkspaceContext for the currently focused workspace.
	activeCtx *config.WorkspaceContext
	// registry is the loaded workspace registry, retained for the picker flow.
	registry *config.WorkspaceRegistry
	// slots holds per-workspace state for all active workspaces
	slots []workspaceSlot
	// focusedSlot is the index into slots for the currently displayed workspace
	focusedSlot int
	// tabBar renders workspace tabs at the top of the TUI
	tabBar *ui.WorkspaceTabBar
	// lastWidth and lastHeight cache the terminal size for sizing new slots
	lastWidth  int
	lastHeight int

	// lastPreviewHash caches the content hash of the selected instance
	// to skip preview ticks when nothing has changed.
	lastPreviewHash []byte
	// lastPreviewTitle tracks which instance the hash belongs to.
	lastPreviewTitle string
}

func newHome(ctx context.Context, wsCtx *config.WorkspaceContext, registry *config.WorkspaceRegistry, appConfig *config.Config, program string, autoYes bool, pendingDir string) *home {
	cfgDir := ""
	if wsCtx != nil {
		cfgDir = wsCtx.ConfigDir
	}

	appState := config.LoadStateFrom(cfgDir)

	storage, err := session.NewStorage(appState, cfgDir)
	if err != nil {
		fmt.Printf("Failed to initialize storage: %v\n", err)
		os.Exit(1)
	}

	h := &home{
		ctx:       ctx,
		activeCtx: wsCtx,
		registry:  registry,
		spinner:   spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		menu:      ui.NewMenu(),
		splitPane: ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:    ui.NewErrBox(),
		storage:   storage,
		appConfig: appConfig,
		program:   program,
		autoYes:   autoYes,
		state:     stateDefault,
		appState:  appState,
		tabBar:    ui.NewWorkspaceTabBar(),
	}
	h.list = ui.NewList(&h.spinner, autoYes)
	if wsCtx != nil && wsCtx.Name != "" {
		h.list.SetWorkspaceName(wsCtx.Name)
	}

	instances, err := storage.LoadInstances()
	if err != nil {
		fmt.Printf("Failed to load instances: %v\n", err)
		os.Exit(1)
	}

	// Check if a workspace terminal already exists in loaded instances
	hasWorkspaceTerminal := false
	for _, instance := range instances {
		if instance.IsWorkspaceTerminal {
			hasWorkspaceTerminal = true
		}
		h.list.AddInstance(instance)()
		if autoYes {
			instance.AutoYes = true
		}
	}

	// Auto-create workspace terminal if in a workspace context and none exists
	if !hasWorkspaceTerminal && wsCtx != nil && wsCtx.RepoPath != "" {
		wtTitle := "Workspace Terminal"
		if wsCtx.Name != "" {
			wtTitle = wsCtx.Name
		}
		wtInstance, wtErr := session.NewInstance(session.InstanceOptions{
			Title:               wtTitle,
			Path:                wsCtx.RepoPath,
			Program:             program,
			IsWorkspaceTerminal: true,
			ConfigDir:           cfgDir,
		})
		if wtErr != nil {
			log.ErrorLog.Printf("failed to create workspace terminal: %v", wtErr)
		} else {
			h.list.AddInstance(wtInstance)()
			if err := wtInstance.Start(true); err != nil {
				log.ErrorLog.Printf("failed to start workspace terminal: %v", err)
			}
		}
	}

	if pendingDir != "" {
		// Unregistered directory: show confirmation to register as workspace.
		name := filepath.Base(pendingDir)
		h.pendingDir = pendingDir
		h.confirmationOverlay = overlay.NewConfirmationOverlay(
			fmt.Sprintf("Register '%s' as workspace '%s'?", pendingDir, name))
		h.confirmationOverlay.SetWidth(60)
		h.state = stateConfirm
		h.pendingAction = func() tea.Msg {
			if err := h.registry.Add(name, pendingDir); err != nil {
				return fmt.Errorf("failed to register workspace: %w", err)
			}
			return workspaceRegisteredMsg{dir: pendingDir}
		}
		h.confirmationOverlay.OnCancel = func() {
			h.pendingAction = nil
			// Fall back to workspace picker if workspaces exist.
			if h.registry != nil && len(h.registry.Workspaces) > 0 {
				h.workspacePicker = overlay.NewStartupWorkspacePicker(h.registry.Workspaces)
				h.state = stateWorkspace
			}
		}
	} else if wsCtx != nil && wsCtx.Name == "" && registry != nil && len(registry.Workspaces) > 0 {
		// No directory arg, global context, workspaces exist: show picker.
		h.workspacePicker = overlay.NewStartupWorkspacePicker(registry.Workspaces)
		h.state = stateWorkspace
	}

	return h
}

// updateHandleWindowSizeEvent sets the sizes of the components.
// The components will try to render inside their bounds.
func (m *home) updateHandleWindowSizeEvent(msg tea.WindowSizeMsg) {
	m.lastWidth = msg.Width
	m.lastHeight = msg.Height
	m.tabBar.SetWidth(msg.Width)

	// List takes 20% of width, split pane takes 80%
	listWidth := int(float32(msg.Width) * 0.2)
	paneWidth := msg.Width - listWidth

	// Content gets all height minus tab bar, status line (1), and error box (1).
	contentHeight := msg.Height - m.tabBar.Height() - 2
	m.errBox.SetSize(int(float32(msg.Width)*0.9), 1)

	if m.state == stateQuickInteract && m.quickInputBar != nil {
		m.quickInputBar.SetWidth(int(float32(msg.Width) * 0.5))
	}
	m.splitPane.SetSize(paneWidth, contentHeight)
	m.list.SetSize(listWidth, contentHeight)

	if m.textInputOverlay != nil {
		m.textInputOverlay.SetSize(int(float32(msg.Width)*0.6), int(float32(msg.Height)*0.4))
	}
	if m.textOverlay != nil {
		m.textOverlay.SetWidth(int(float32(msg.Width) * 0.6))
	}

	agentWidth, agentHeight := m.splitPane.GetAgentSize()
	if err := m.list.SetSessionPreviewSize(agentWidth, agentHeight); err != nil {
		log.ErrorLog.Print(err)
	}
}

func (m *home) Init() tea.Cmd {
	// Upon starting, we want to start the spinner. Whenever we get a spinner.TickMsg, we
	// update the spinner, which sends a new spinner.TickMsg. I think this lasts forever lol.
	return tea.Batch(
		m.spinner.Tick,
		func() tea.Msg {
			time.Sleep(100 * time.Millisecond)
			return previewTickMsg{}
		},
		tickUpdateMetadataCmd,
	)
}

func (m *home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case hideErrMsg:
		m.errBox.Clear()
	case previewTickMsg:
		// Check if inline-attached instance is still alive
		inlineAttachExited := false
		if m.state == stateInlineAttach {
			selected := m.list.GetSelectedInstance()
			if selected == nil || selected.Paused() || !selected.TmuxAlive() {
				m.state = stateDefault
				m.menu.SetState(ui.StateDefault)
				inlineAttachExited = true
			}
		}

		// Use faster tick during inline attach for responsive feedback
		tickDuration := 100 * time.Millisecond
		if m.state == stateInlineAttach {
			tickDuration = 33 * time.Millisecond
		}
		nextTick := func() tea.Msg {
			time.Sleep(tickDuration)
			return previewTickMsg{}
		}

		// Skip update if same instance and content hash unchanged,
		// but always update during inline attach for responsive feedback.
		if m.state != stateInlineAttach && !inlineAttachExited {
			selected := m.list.GetSelectedInstance()
			var currentHash []byte
			var currentTitle string
			if selected != nil {
				currentHash = selected.GetContentHash()
				currentTitle = selected.Title
			}

			if currentTitle == m.lastPreviewTitle &&
				currentHash != nil &&
				bytes.Equal(currentHash, m.lastPreviewHash) {
				// Agent content unchanged, but the terminal pane has its
				// own independent tmux session whose content may have changed.
				if selected != nil {
					_ = m.splitPane.UpdateTerminal(selected)
				}
				return m, nextTick
			}

			m.lastPreviewHash = currentHash
			m.lastPreviewTitle = currentTitle
		}

		cmd := m.instanceChanged()
		cmds := []tea.Cmd{cmd, nextTick}
		if inlineAttachExited {
			cmds = append(cmds, tea.WindowSize())
		}
		return m, tea.Batch(cmds...)
	case keyupMsg:
		m.menu.ClearKeydown()
		return m, nil
	case tickUpdateMetadataMessage:
		// Collect instances from all workspace slots (focused uses m.list).
		var allInstances []*session.Instance
		if len(m.slots) > 0 {
			for i, slot := range m.slots {
				if i == m.focusedSlot {
					allInstances = append(allInstances, m.list.GetInstances()...)
				} else {
					allInstances = append(allInstances, slot.list.GetInstances()...)
				}
			}
		} else {
			allInstances = m.list.GetInstances()
		}

		// Filter to active instances.
		selected := m.list.GetSelectedInstance()
		var active []*session.Instance
		for _, inst := range allInstances {
			if inst.Started() && !inst.Paused() && inst.GetStatus() != session.Deleting {
				active = append(active, inst)
			}
		}

		// Fan out I/O to goroutines.
		results := make([]metadataResult, len(active))
		var wg sync.WaitGroup
		for i, inst := range active {
			wg.Add(1)
			go func(idx int, instance *session.Instance) {
				defer wg.Done()
				r := &results[idx]
				r.instance = instance

				r.tmuxAlive = instance.TmuxAlive()
				if !r.tmuxAlive {
					return
				}

				r.updated, r.hasPrompt = instance.CaptureAndProcessStatus()

				if instance == selected {
					r.diffErr = instance.UpdateDiffStats()
				} else {
					r.diffErr = instance.UpdateDiffStatsShort()
				}
			}(i, inst)
		}
		wg.Wait()

		// Apply results on main thread.
		for _, r := range results {
			if !r.tmuxAlive {
				if r.instance.IsWorkspaceTerminal {
					log.WarningLog.Printf("workspace terminal %q tmux died, restarting", r.instance.Title)
					if err := r.instance.Start(true); err != nil {
						log.ErrorLog.Printf("failed to restart workspace terminal: %v", err)
					}
					continue
				}
				log.WarningLog.Printf("tmux session for %q is gone, marking as paused", r.instance.Title)
				r.instance.SetStatus(session.Paused)
				continue
			}
			if r.updated {
				r.instance.SetStatus(session.Running)
			} else {
				if r.hasPrompt {
					r.instance.TapEnter()
					r.instance.SetStatus(session.Prompting)
				} else {
					r.instance.SetStatus(session.Ready)
				}
			}
			if r.diffErr != nil {
				log.WarningLog.Printf("could not update diff stats: %v", r.diffErr)
			}
		}
		m.updateTabBarStatuses()
		return m, tickUpdateMetadataCmd
	case tea.MouseMsg:
		// Handle mouse wheel events for scrolling the diff/preview pane
		if msg.Action == tea.MouseActionPress {
			if msg.Button == tea.MouseButtonWheelDown || msg.Button == tea.MouseButtonWheelUp {
				selected := m.list.GetSelectedInstance()
				if selected == nil || selected.GetStatus() == session.Paused {
					return m, nil
				}

				switch msg.Button {
				case tea.MouseButtonWheelUp:
					m.splitPane.ScrollUp()
				case tea.MouseButtonWheelDown:
					m.splitPane.ScrollDown()
				}
			}
		}
		return m, nil
	case branchSearchDebounceMsg:
		// Debounce timer fired — check if this is still the current filter version
		if m.textInputOverlay == nil {
			return m, nil
		}
		if msg.version != m.textInputOverlay.BranchFilterVersion() {
			return m, nil // stale, a newer debounce is pending
		}
		return m, m.runBranchSearch(msg.filter, msg.version)
	case branchSearchResultMsg:
		if m.textInputOverlay != nil {
			m.textInputOverlay.SetBranchResults(msg.branches, msg.version)
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKeyPress(msg)
	case tea.WindowSizeMsg:
		m.updateHandleWindowSizeEvent(msg)
		return m, nil
	case error:
		// Handle errors from confirmation actions
		return m, m.handleError(msg)
	case instanceChangedMsg:
		// Handle instance changed after confirmation action
		return m, m.instanceChanged()
	case killInstanceMsg:
		// State mutations run here in the main goroutine, not in the Cmd goroutine.
		m.splitPane.CleanupTerminalForInstance(msg.title)
		m.list.RemoveInstanceByTitle(msg.title)
		return m, m.instanceChanged()
	case killFailedMsg:
		// Revert instance status on failed deletion.
		for _, inst := range m.list.GetInstances() {
			if inst.Title == msg.title {
				inst.SetStatus(msg.previousStatus)
				break
			}
		}
		log.ErrorLog.Printf("failed to delete session %q: %v", msg.title, msg.err)
		return m, tea.Batch(m.handleError(msg.err), m.instanceChanged())
	case pauseInstanceMsg:
		// Terminal cleanup runs here in the main goroutine, not in the Cmd goroutine.
		m.splitPane.CleanupTerminalForInstance(msg.title)
		return m, m.instanceChanged()
	case workspaceRegisteredMsg:
		ws := m.registry.FindByPath(msg.dir)
		if ws == nil {
			return m, m.handleError(fmt.Errorf("workspace not found after registration"))
		}
		if err := m.activateWorkspace(*ws); err != nil {
			return m, m.handleError(fmt.Errorf("failed to activate workspace: %w", err))
		}
		m.activeCtx = config.WorkspaceContextFor(ws)
		m.loadSlot(0)
		m.updateTabBarStatuses()
		_ = m.registry.UpdateLastUsed(ws.Name)
		return m, tea.WindowSize()
	case instanceStartedMsg:
		// Select the instance that just started (or failed)
		m.list.SelectInstance(msg.instance)

		if msg.err != nil {
			m.list.Kill()
			return m, tea.Batch(m.handleError(msg.err), m.instanceChanged())
		}

		// Save after successful start
		if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
			return m, m.handleError(err)
		}
		if m.autoYes {
			msg.instance.AutoYes = true
		}

		if msg.promptAfterName {
			m.state = statePrompt
			m.menu.SetState(ui.StatePrompt)
			m.textInputOverlay = m.newPromptOverlay()
		} else {
			// If instance has a prompt (set from Shift+N flow), send it now
			if msg.instance.Prompt != "" {
				if err := msg.instance.SendPrompt(msg.instance.Prompt); err != nil {
					log.ErrorLog.Printf("failed to send prompt: %v", err)
				}
				msg.instance.Prompt = ""
			}
			// Auto-focus agent pane and capture input
			m.splitPane.SetFocusedPane(ui.FocusAgent)
			m.splitPane.SetInlineAttach(true)
			m.state = stateInlineAttach
			m.menu.SetState(ui.StateInlineAttach)
		}

		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

// persistableInstances filters out instances with transient Deleting status.
func persistableInstances(instances []*session.Instance) []*session.Instance {
	var result []*session.Instance
	for _, inst := range instances {
		if inst.GetStatus() != session.Deleting {
			result = append(result, inst)
		}
	}
	return result
}

func (m *home) handleQuit() (tea.Model, tea.Cmd) {
	if len(m.slots) > 0 {
		m.saveCurrentSlot()
		for _, slot := range m.slots {
			if err := slot.storage.SaveInstances(persistableInstances(slot.list.GetInstances())); err != nil {
				log.ErrorLog.Printf("failed to save workspace %s: %v", slot.wsCtx.Name, err)
			}
		}
	} else {
		if err := m.storage.SaveInstances(persistableInstances(m.list.GetInstances())); err != nil {
			return m, m.handleError(err)
		}
	}
	return m, tea.Quit
}

func (m *home) handleMenuHighlighting(msg tea.KeyMsg) (cmd tea.Cmd, returnEarly bool) {
	// Handle menu highlighting when you press a button. We intercept it here and immediately return to
	// update the ui while re-sending the keypress. Then, on the next call to this, we actually handle the keypress.
	if m.keySent {
		m.keySent = false
		return nil, false
	}
	if m.state == statePrompt || m.state == stateHelp || m.state == stateConfirm || m.state == stateWorkspace || m.state == stateQuickInteract || m.state == stateInlineAttach {
		return nil, false
	}
	// If it's in the global keymap, we should try to highlight it.
	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		return nil, false
	}

	m.keySent = true
	return tea.Batch(
		func() tea.Msg { return msg },
		m.keydownCallback(name)), true
}

func (m *home) handleKeyPress(msg tea.KeyMsg) (mod tea.Model, cmd tea.Cmd) {
	cmd, returnEarly := m.handleMenuHighlighting(msg)
	if returnEarly {
		return m, cmd
	}

	if m.state == stateHelp {
		return m.handleHelpState(msg)
	}

	if m.state == stateNew {
		// Handle quit commands first. Don't handle q because the user might want to type that.
		if msg.String() == "ctrl+c" {
			m.state = stateDefault
			m.promptAfterName = false
			m.list.Kill()
			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			)
		}

		instance := m.list.GetInstances()[m.list.NumInstances()-1]
		switch msg.Type {
		// Start the instance (enable previews etc) and go back to the main menu state.
		case tea.KeyEnter:
			if len(instance.Title) == 0 {
				return m, m.handleError(fmt.Errorf("title cannot be empty"))
			}

			// If promptAfterName, show prompt+branch overlay before starting
			if m.promptAfterName {
				m.promptAfterName = false
				m.state = statePrompt
				m.menu.SetState(ui.StatePrompt)
				m.textInputOverlay = m.newPromptOverlay()
				// Trigger initial branch search (no debounce, version 0)
				initialSearch := m.runBranchSearch("", m.textInputOverlay.BranchFilterVersion())
				return m, tea.Batch(tea.WindowSize(), initialSearch)
			}

			// Set Loading status and finalize into the list immediately
			instance.SetStatus(session.Loading)
			m.newInstanceFinalizer()
			m.promptAfterName = false
			m.state = stateDefault
			m.menu.SetState(ui.StateDefault)

			// Return a tea.Cmd that runs instance.Start in the background
			startCmd := func() tea.Msg {
				err := instance.Start(true)
				return instanceStartedMsg{
					instance:        instance,
					err:             err,
					promptAfterName: false,
				}
			}

			return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
		case tea.KeyRunes:
			if runewidth.StringWidth(instance.Title) >= 32 {
				return m, m.handleError(fmt.Errorf("title cannot be longer than 32 characters"))
			}
			if err := instance.SetTitle(instance.Title + string(msg.Runes)); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeyBackspace:
			runes := []rune(instance.Title)
			if len(runes) == 0 {
				return m, nil
			}
			if err := instance.SetTitle(string(runes[:len(runes)-1])); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeySpace:
			if err := instance.SetTitle(instance.Title + " "); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeyEsc:
			m.list.Kill()
			m.state = stateDefault
			m.instanceChanged()

			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			)
		default:
		}
		return m, nil
	} else if m.state == statePrompt {
		// Handle cancel via ctrl+c before delegating to the overlay
		if msg.String() == "ctrl+c" {
			return m, m.cancelPromptOverlay()
		}

		// Use the new TextInputOverlay component to handle all key events
		shouldClose, branchFilterChanged := m.textInputOverlay.HandleKeyPress(msg)

		// Check if the form was submitted or canceled
		if shouldClose {
			selected := m.list.GetSelectedInstance()
			if selected == nil {
				return m, nil
			}

			if m.textInputOverlay.IsCanceled() {
				return m, m.cancelPromptOverlay()
			}

			if m.textInputOverlay.IsSubmitted() {
				prompt := m.textInputOverlay.GetValue()
				selectedBranch := m.textInputOverlay.GetSelectedBranch()
				selectedProgram := m.textInputOverlay.GetSelectedProgram()

				if !selected.Started() {
					// Shift+N flow: instance not started yet — set branch, start, then send prompt
					if selectedBranch != "" {
						selected.SetSelectedBranch(selectedBranch)
					}
					if selectedProgram != "" {
						selected.Program = selectedProgram
					}
					selected.Prompt = prompt

					// Finalize into list and start
					selected.SetStatus(session.Loading)
					m.newInstanceFinalizer()
					m.textInputOverlay = nil
					m.state = stateDefault
					m.menu.SetState(ui.StateDefault)

					startCmd := func() tea.Msg {
						err := selected.Start(true)
						return instanceStartedMsg{
							instance:        selected,
							err:             err,
							promptAfterName: false,
							selectedBranch:  selectedBranch,
						}
					}

					return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
				}

				// Regular flow: instance already running, just send prompt
				if err := selected.SendPrompt(prompt); err != nil {
					return m, m.handleError(err)
				}
			}

			// Close the overlay and reset state
			m.textInputOverlay = nil
			m.state = stateDefault
			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					m.showHelpScreen(helpStart(selected), nil)
					return nil
				},
			)
		}

		// Schedule a debounced branch search if the filter changed
		if branchFilterChanged {
			filter := m.textInputOverlay.BranchFilter()
			version := m.textInputOverlay.BranchFilterVersion()
			return m, m.scheduleBranchSearch(filter, version)
		}

		return m, nil
	}

	if m.state == stateInlineAttach {
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Paused() || !selected.TmuxAlive() {
			m.splitPane.SetInlineAttach(false)
			m.splitPane.SetFocusedPane(ui.FocusAgent)
			m.state = stateDefault
			m.menu.SetState(ui.StateDefault)
			return m, tea.WindowSize()
		}

		// ctrl+q exits inline attach
		if msg.Type == tea.KeyCtrlQ {
			m.splitPane.SetInlineAttach(false)
			m.splitPane.SetFocusedPane(ui.FocusAgent)
			m.state = stateDefault
			m.menu.SetState(ui.StateDefault)
			return m, tea.WindowSize()
		}

		// Convert key to bytes and forward to the focused pane's tmux session
		b := keyMsgToBytes(msg)
		if b != nil {
			var err error
			if m.splitPane.GetFocusedPane() == ui.FocusTerminal {
				err = m.splitPane.SendTerminalKeysRaw(b)
			} else {
				err = selected.SendKeysRaw(b)
			}
			if err != nil {
				log.ErrorLog.Printf("inline attach send error: %v", err)
			}
		}
		return m, nil
	}

	if m.state == stateQuickInteract {
		if m.quickInputBar == nil {
			m.state = stateDefault
			return m, nil
		}

		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Paused() || !selected.TmuxAlive() {
			m.quickInputBar = nil
			m.state = stateDefault
			m.menu.SetState(ui.StateDefault)
			return m, tea.WindowSize()
		}

		action := m.quickInputBar.HandleKeyPress(msg)
		switch action {
		case ui.QuickInputSubmit:
			text := m.quickInputBar.Value()
			var err error
			switch m.quickInputBar.Target {
			case ui.QuickInputTargetTerminal:
				err = m.splitPane.SendTerminalPrompt(text)
			case ui.QuickInputTargetAgent:
				err = selected.SendPrompt(text)
			}
			m.quickInputBar = nil
			m.state = stateDefault
			m.menu.SetState(ui.StateDefault)
			if err != nil {
				return m, tea.Batch(tea.WindowSize(), m.handleError(err))
			}
			return m, tea.WindowSize()
		case ui.QuickInputCancel:
			m.quickInputBar = nil
			m.state = stateDefault
			m.menu.SetState(ui.StateDefault)
			return m, tea.WindowSize()
		}
		return m, nil
	}

	// Handle workspace picker state
	if m.state == stateWorkspace {
		committed, _ := m.workspacePicker.HandleKeyPress(msg)
		if committed {
			if m.workspacePicker.IsStartup() {
				// Startup single-select: activate the chosen workspace.
				selected := m.workspacePicker.GetSelectedWorkspace()
				m.workspacePicker = nil
				m.state = stateDefault
				if selected != nil {
					wsCtx := config.WorkspaceContextFor(selected)
					m.activeCtx = wsCtx
					if err := m.activateWorkspace(*selected); err != nil {
						return m, m.handleError(fmt.Errorf("failed to activate workspace: %w", err))
					}
					m.loadSlot(0)
					m.updateTabBarStatuses()
					if m.registry != nil {
						_ = m.registry.UpdateLastUsed(selected.Name)
					}
				}
				// else: Global selected, keep current (global) state.
				return m, tea.WindowSize()
			}
			// Mid-session toggle: diff active workspaces.
			desired := m.workspacePicker.GetActiveWorkspaces()
			m.workspacePicker = nil
			m.state = stateDefault
			return m, m.applyWorkspaceToggle(desired)
		}
		return m, nil
	}

	// Handle confirmation state
	if m.state == stateConfirm {
		shouldClose := m.confirmationOverlay.HandleKeyPress(msg)
		if shouldClose {
			if m.pendingPreAction != nil {
				m.pendingPreAction()
				m.pendingPreAction = nil
			}
			cmd := m.pendingAction
			m.pendingAction = nil
			m.confirmationOverlay = nil
			m.state = stateDefault
			return m, tea.Batch(cmd, m.instanceChanged())
		}
		return m, nil
	}

	if msg.Type == tea.KeyEsc {
		// Dismiss diff overlay first
		if m.splitPane.IsDiffVisible() {
			m.splitPane.ToggleDiff()
			return m, m.instanceChanged()
		}
		// Exit agent scroll mode
		if m.splitPane.IsAgentInScrollMode() {
			selected := m.list.GetSelectedInstance()
			err := m.splitPane.ResetAgentToNormalMode(selected)
			if err != nil {
				return m, m.handleError(err)
			}
			return m, m.instanceChanged()
		}
		// Exit terminal scroll mode
		if m.splitPane.IsTerminalInScrollMode() {
			m.splitPane.ResetTerminalToNormalMode()
			return m, m.instanceChanged()
		}
	}

	// Handle quit commands first
	if msg.String() == "ctrl+c" || msg.String() == "q" {
		return m.handleQuit()
	}

	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		return m, nil
	}

	switch name {
	case keys.KeyHelp:
		return m.showHelpScreen(helpTypeGeneral{}, nil)
	case keys.KeyPrompt:
		if m.list.NumInstances() >= GlobalInstanceLimit {
			return m, m.handleError(
				fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
		}

		// Start a background fetch so branches are up to date by the time the picker opens
		repoDir := m.repoPath()
		fetchCmd := func() tea.Msg {
			git.FetchBranches(repoDir)
			return nil
		}

		instance, err := session.NewInstance(session.InstanceOptions{
			Title:     "",
			Path:      repoDir,
			Program:   m.program,
			ConfigDir: m.configDir(),
		})
		if err != nil {
			return m, m.handleError(err)
		}

		m.newInstanceFinalizer = m.list.AddInstance(instance)
		m.list.SetSelectedInstance(m.list.NumInstances() - 1)
		m.state = stateNew
		m.menu.SetState(ui.StateNewInstance)
		m.promptAfterName = true

		return m, fetchCmd
	case keys.KeyNew:
		if m.list.NumInstances() >= GlobalInstanceLimit {
			return m, m.handleError(
				fmt.Errorf("you can't create more than %d instances", GlobalInstanceLimit))
		}
		instance, err := session.NewInstance(session.InstanceOptions{
			Title:     "",
			Path:      m.repoPath(),
			Program:   m.program,
			ConfigDir: m.configDir(),
		})
		if err != nil {
			return m, m.handleError(err)
		}

		m.newInstanceFinalizer = m.list.AddInstance(instance)
		m.list.SetSelectedInstance(m.list.NumInstances() - 1)
		m.state = stateNew
		m.menu.SetState(ui.StateNewInstance)

		return m, nil
	case keys.KeyUp:
		m.list.Up()
		return m, m.instanceChanged()
	case keys.KeyDown:
		m.list.Down()
		return m, m.instanceChanged()
	case keys.KeyDiff:
		m.splitPane.ToggleDiff()
		return m, m.instanceChanged()
	case keys.KeyKill:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.IsWorkspaceTerminal {
			return m, nil
		}
		previousStatus := selected.GetStatus()
		if previousStatus == session.Loading || previousStatus == session.Deleting {
			return m, nil
		}

		title := selected.Title

		// preAction runs synchronously in the main goroutine when the user
		// confirms. It marks the instance as Deleting immediately.
		preAction := func() {
			selected.SetStatus(session.Deleting)
		}

		// killAction runs in a goroutine — only I/O, no state mutations.
		killAction := func() tea.Msg {
			// Get worktree and check if branch is checked out
			worktree, err := selected.GetGitWorktree()
			if err != nil {
				return killFailedMsg{title: title, previousStatus: previousStatus, err: err}
			}

			checkedOut, err := worktree.IsBranchCheckedOut()
			if err != nil {
				return killFailedMsg{title: title, previousStatus: previousStatus, err: err}
			}

			if checkedOut {
				return killFailedMsg{
					title:          title,
					previousStatus: previousStatus,
					err:            fmt.Errorf("instance %s is currently checked out", selected.Title),
				}
			}

			// Kill the instance (tmux + worktree cleanup)
			if err := selected.Kill(); err != nil {
				log.ErrorLog.Printf("could not kill instance: %v", err)
			}

			// Delete from persistent storage
			if err := m.storage.DeleteInstance(selected.Title); err != nil {
				return killFailedMsg{title: title, previousStatus: previousStatus, err: err}
			}

			return killInstanceMsg{title: title}
		}

		message := fmt.Sprintf("[!] Kill session '%s'?", selected.Title)
		m.pendingPreAction = preAction
		return m, m.confirmAction(message, killAction)
	case keys.KeySubmit:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.IsWorkspaceTerminal {
			return m, nil
		}
		if s := selected.GetStatus(); s == session.Loading || s == session.Deleting {
			return m, nil
		}

		// Create the push action as a tea.Cmd
		pushAction := func() tea.Msg {
			// Default commit message with timestamp
			commitMsg := fmt.Sprintf("[claudesquad] update from '%s' on %s", selected.Title, time.Now().Format(time.RFC822))
			worktree, err := selected.GetGitWorktree()
			if err != nil {
				return err
			}
			if err = worktree.PushChanges(commitMsg, true); err != nil {
				return err
			}
			return nil
		}

		// Show confirmation modal
		message := fmt.Sprintf("[!] Push changes from session '%s'?", selected.Title)
		return m, m.confirmAction(message, pushAction)
	case keys.KeyCheckout:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.IsWorkspaceTerminal {
			return m, nil
		}
		if s := selected.GetStatus(); s == session.Loading || s == session.Deleting {
			return m, nil
		}

		pauseTitle := selected.Title
		pauseAction := func() tea.Msg {
			if err := selected.Pause(); err != nil {
				return err
			}
			return pauseInstanceMsg{title: pauseTitle}
		}

		// Show help screen before confirming pause
		m.showHelpScreen(helpTypeInstanceCheckout{}, func() {
			message := fmt.Sprintf("[!] Pause session '%s'?", selected.Title)
			m.confirmAction(message, pauseAction)
		})
		return m, nil
	case keys.KeyResume:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.IsWorkspaceTerminal {
			return m, nil
		}
		if s := selected.GetStatus(); s == session.Loading || s == session.Deleting {
			return m, nil
		}
		if err := selected.Resume(); err != nil {
			return m, m.handleError(err)
		}
		return m, tea.WindowSize()
	case keys.KeyDirectAttachAgent:
		if m.list.NumInstances() == 0 {
			return m, nil
		}
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Paused() || !selected.TmuxAlive() {
			return m, nil
		}
		if s := selected.GetStatus(); s == session.Loading || s == session.Deleting {
			return m, nil
		}
		m.splitPane.SetFocusedPane(ui.FocusAgent)
		m.splitPane.SetInlineAttach(true)
		m.state = stateInlineAttach
		m.menu.SetState(ui.StateInlineAttach)
		return m, tea.WindowSize()
	case keys.KeyDirectAttachTerminal:
		if m.list.NumInstances() == 0 {
			return m, nil
		}
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Paused() || !selected.TmuxAlive() {
			return m, nil
		}
		if s := selected.GetStatus(); s == session.Loading || s == session.Deleting {
			return m, nil
		}
		m.splitPane.SetFocusedPane(ui.FocusTerminal)
		m.splitPane.SetInlineAttach(true)
		m.state = stateInlineAttach
		m.menu.SetState(ui.StateInlineAttach)
		return m, tea.WindowSize()
	case keys.KeyWorkspace:
		registry, err := config.LoadWorkspaceRegistry()
		if err != nil {
			return m, m.handleError(fmt.Errorf("failed to load workspace registry: %w", err))
		}
		if len(registry.Workspaces) == 0 {
			return m, m.handleError(fmt.Errorf("no workspaces registered"))
		}
		activeNames := make(map[string]bool, len(m.slots))
		for _, slot := range m.slots {
			activeNames[slot.wsCtx.Name] = true
		}
		m.workspacePicker = overlay.NewWorkspacePicker(registry.Workspaces, activeNames)
		m.state = stateWorkspace
		return m, nil
	case keys.KeyWorkspaceLeft:
		if len(m.slots) <= 1 {
			return m, nil
		}
		m.saveCurrentSlot()
		newIdx := (m.focusedSlot - 1 + len(m.slots)) % len(m.slots)
		m.loadSlot(newIdx)
		m.updateTabBarStatuses()
		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case keys.KeyWorkspaceRight:
		if len(m.slots) <= 1 {
			return m, nil
		}
		m.saveCurrentSlot()
		newIdx := (m.focusedSlot + 1) % len(m.slots)
		m.loadSlot(newIdx)
		m.updateTabBarStatuses()
		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case keys.KeyQuickInputAgent:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Paused() || !selected.TmuxAlive() {
			return m, nil
		}
		if s := selected.GetStatus(); s == session.Loading || s == session.Deleting {
			return m, nil
		}
		if m.splitPane.IsDiffVisible() {
			return m, nil
		}
		m.state = stateQuickInteract
		m.quickInputBar = ui.NewQuickInputBar(ui.QuickInputTargetAgent)
		m.menu.SetState(ui.StateQuickInteract)
		return m, tea.WindowSize()
	case keys.KeyQuickInputTerminal:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Paused() || !selected.TmuxAlive() {
			return m, nil
		}
		if s := selected.GetStatus(); s == session.Loading || s == session.Deleting {
			return m, nil
		}
		if m.splitPane.IsDiffVisible() {
			return m, nil
		}
		m.state = stateQuickInteract
		m.quickInputBar = ui.NewQuickInputBar(ui.QuickInputTargetTerminal)
		m.menu.SetState(ui.StateQuickInteract)
		return m, tea.WindowSize()
	case keys.KeyFullScreenAttachAgent:
		if m.list.NumInstances() == 0 {
			return m, nil
		}
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.IsWorkspaceTerminal || selected.Paused() || !selected.TmuxAlive() {
			return m, nil
		}
		if s := selected.GetStatus(); s == session.Loading || s == session.Deleting {
			return m, nil
		}
		m.showHelpScreen(helpTypeInstanceAttach{}, func() {
			ch, err := m.list.Attach()
			if err != nil {
				m.handleError(err)
				return
			}
			<-ch
			m.state = stateDefault
		})
		return m, nil
	case keys.KeyFullScreenAttachTerminal:
		if m.list.NumInstances() == 0 {
			return m, nil
		}
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.IsWorkspaceTerminal || selected.Paused() || !selected.TmuxAlive() {
			return m, nil
		}
		if s := selected.GetStatus(); s == session.Loading || s == session.Deleting {
			return m, nil
		}
		m.showHelpScreen(helpTypeInstanceAttach{}, func() {
			ch, err := m.splitPane.AttachTerminal()
			if err != nil {
				m.handleError(err)
				return
			}
			<-ch
			m.state = stateDefault
		})
		return m, nil
	default:
		return m, nil
	}
}

// instanceChanged updates the preview pane, menu, and diff pane based on the selected instance. It returns an error
// Cmd if there was any error.
func (m *home) instanceChanged() tea.Cmd {
	// selected may be nil
	selected := m.list.GetSelectedInstance()

	m.splitPane.UpdateDiff(selected)
	m.splitPane.SetInstance(selected)
	// Update menu with current instance
	m.menu.SetInstance(selected)

	if err := m.splitPane.UpdateAgent(selected); err != nil {
		return m.handleError(err)
	}
	if err := m.splitPane.UpdateTerminal(selected); err != nil {
		return m.handleError(err)
	}
	return nil
}

type keyupMsg struct{}

// keydownCallback clears the menu option highlighting after 500ms.
func (m *home) keydownCallback(name keys.KeyName) tea.Cmd {
	m.menu.Keydown(name)
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}

		return keyupMsg{}
	}
}

// hideErrMsg implements tea.Msg and clears the error text from the screen.
type hideErrMsg struct{}

// previewTickMsg implements tea.Msg and triggers a preview update
type previewTickMsg struct{}

type tickUpdateMetadataMessage struct{}

type instanceChangedMsg struct{}

// killInstanceMsg is returned by the killAction goroutine after I/O cleanup
// (git checks, instance kill, storage deletion) is complete. The main event loop
// handles the list removal so it doesn't race with rendering.
type killInstanceMsg struct {
	title string
}

// killFailedMsg is returned when background cleanup fails. The main event
// loop reverts the instance status so the user can retry.
type killFailedMsg struct {
	title          string
	previousStatus session.Status
	err            error
}

// pauseInstanceMsg is returned by the pauseAction goroutine after the instance
// has been paused. Terminal cleanup happens in the main event loop.
type pauseInstanceMsg struct {
	title string
}

// workspaceRegisteredMsg is sent after a pending directory is registered as a workspace.
type workspaceRegisteredMsg struct {
	dir string
}

type instanceStartedMsg struct {
	instance        *session.Instance
	err             error
	promptAfterName bool
	selectedBranch  string
}

// branchSearchDebounceMsg fires after the debounce interval to trigger a search.
type branchSearchDebounceMsg struct {
	filter  string
	version uint64
}

// branchSearchResultMsg carries search results back to Update.
type branchSearchResultMsg struct {
	branches []string
	version  uint64
}

const branchSearchDebounce = 150 * time.Millisecond

// scheduleBranchSearch returns a debounced tea.Cmd: sleeps, then triggers a search message.
func (m *home) scheduleBranchSearch(filter string, version uint64) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(branchSearchDebounce)
		return branchSearchDebounceMsg{filter: filter, version: version}
	}
}

// runBranchSearch returns a tea.Cmd that performs the git search in the background.
func (m *home) runBranchSearch(filter string, version uint64) tea.Cmd {
	repoDir := m.repoPath()
	return func() tea.Msg {
		branches, err := git.SearchBranches(repoDir, filter)
		if err != nil {
			log.WarningLog.Printf("branch search failed: %v", err)
			return nil
		}
		return branchSearchResultMsg{branches: branches, version: version}
	}
}

// tickUpdateMetadataCmd is the callback to update the metadata of the instances every 500ms. Note that we iterate
// overall the instances and capture their output. It's a pretty expensive operation. Let's do it 2x a second only.
var tickUpdateMetadataCmd = func() tea.Msg {
	time.Sleep(500 * time.Millisecond)
	return tickUpdateMetadataMessage{}
}

// handleError handles all errors which get bubbled up to the app. sets the error message. We return a callback tea.Cmd that returns a hideErrMsg message
// which clears the error message after 3 seconds.
func (m *home) handleError(err error) tea.Cmd {
	log.ErrorLog.Printf("%v", err)
	m.errBox.SetError(err)
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(3 * time.Second):
		}

		return hideErrMsg{}
	}
}

func (m *home) newPromptOverlay() *overlay.TextInputOverlay {
	return overlay.NewTextInputOverlayWithBranchPicker("Enter prompt", "", m.appConfig.GetProfiles())
}

// cancelPromptOverlay cancels the prompt overlay, cleaning up unstarted instances.
func (m *home) cancelPromptOverlay() tea.Cmd {
	selected := m.list.GetSelectedInstance()
	if selected != nil && !selected.Started() {
		m.list.Kill()
	}
	m.textInputOverlay = nil
	m.state = stateDefault
	return tea.Sequence(
		tea.WindowSize(),
		func() tea.Msg {
			m.menu.SetState(ui.StateDefault)
			return nil
		},
	)
}

// confirmAction shows a confirmation modal and stores the action to execute on confirm
func (m *home) confirmAction(message string, action tea.Cmd) tea.Cmd {
	m.state = stateConfirm
	m.pendingAction = action

	// Create and show the confirmation overlay using ConfirmationOverlay
	m.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	// Set a fixed width for consistent appearance
	m.confirmationOverlay.SetWidth(50)

	// Set callbacks for confirmation and cancellation
	m.confirmationOverlay.OnCancel = func() {
		m.pendingAction = nil
		m.pendingPreAction = nil
	}

	return nil
}

// repoPath returns the git repository path for the current context.
// When a workspace is active it returns the workspace's registered path;
// otherwise it falls back to the process working directory.
func (m *home) repoPath() string {
	if len(m.slots) > 0 && m.focusedSlot >= 0 && m.focusedSlot < len(m.slots) {
		if p := m.slots[m.focusedSlot].wsCtx.RepoPath; p != "" {
			return p
		}
	}
	cwd, _ := os.Getwd()
	return cwd
}

// configDir returns the config directory for the focused workspace slot.
// Mirrors repoPath() so both functions stay consistent if focusedSlot moves
// out of sync with activeCtx. Returns empty string when no workspace is
// active (triggers fallback to GetConfigDir).
func (m *home) configDir() string {
	if len(m.slots) > 0 && m.focusedSlot >= 0 && m.focusedSlot < len(m.slots) {
		return m.slots[m.focusedSlot].wsCtx.ConfigDir
	}
	if m.activeCtx != nil {
		return m.activeCtx.ConfigDir
	}
	return ""
}

// activateWorkspace loads a workspace's state, config, instances and UI
// components, appending a new slot to m.slots.
func (m *home) activateWorkspace(ws config.Workspace) error {
	wsCtx := config.WorkspaceContextFor(&ws)
	state := config.LoadStateFrom(wsCtx.ConfigDir)
	appConfig := config.LoadConfigFrom(wsCtx.ConfigDir)
	storage, err := session.NewStorage(state, wsCtx.ConfigDir)
	if err != nil {
		return fmt.Errorf("failed to create storage for workspace %s: %w", ws.Name, err)
	}

	instances, err := storage.LoadInstances()
	if err != nil {
		log.ErrorLog.Printf("failed to load instances for workspace %s: %v", ws.Name, err)
	}
	list := ui.NewList(&m.spinner, m.autoYes)
	hasWorkspaceTerminal := false
	for _, inst := range instances {
		if inst.IsWorkspaceTerminal {
			hasWorkspaceTerminal = true
		}
		list.AddInstance(inst)()
		if m.autoYes {
			inst.AutoYes = true
		}
	}

	// Auto-create workspace terminal if none exists
	if !hasWorkspaceTerminal && wsCtx.RepoPath != "" {
		wtTitle := ws.Name
		if wtTitle == "" {
			wtTitle = "Workspace Terminal"
		}
		wtInstance, wtErr := session.NewInstance(session.InstanceOptions{
			Title:               wtTitle,
			Path:                wsCtx.RepoPath,
			Program:             appConfig.GetProgram(),
			IsWorkspaceTerminal: true,
			ConfigDir:           wsCtx.ConfigDir,
		})
		if wtErr != nil {
			log.ErrorLog.Printf("failed to create workspace terminal for %s: %v", ws.Name, wtErr)
		} else {
			list.AddInstance(wtInstance)()
			if startErr := wtInstance.Start(true); startErr != nil {
				log.ErrorLog.Printf("failed to start workspace terminal for %s: %v", ws.Name, startErr)
			}
		}
	}

	list.SetWorkspaceName(ws.Name)

	splitPane := ui.NewSplitPane(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())

	// Pre-size components if terminal dimensions are known.
	if m.lastWidth > 0 && m.lastHeight > 0 {
		listWidth := int(float32(m.lastWidth) * 0.2)
		paneWidth := m.lastWidth - listWidth
		contentHeight := m.lastHeight - m.tabBar.Height() - 2
		list.SetSize(listWidth, contentHeight)
		splitPane.SetSize(paneWidth, contentHeight)
	}

	m.slots = append(m.slots, workspaceSlot{
		wsCtx:     wsCtx,
		storage:   storage,
		appConfig: appConfig,
		appState:  state,
		list:      list,
		splitPane: splitPane,
	})
	return nil
}

// deactivateWorkspace saves and removes a workspace slot by name.
func (m *home) deactivateWorkspace(name string) {
	idx := -1
	for i, slot := range m.slots {
		if slot.wsCtx.Name == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return
	}

	slot := m.slots[idx]
	_ = slot.storage.SaveInstances(slot.list.GetInstances())

	m.slots = append(m.slots[:idx], m.slots[idx+1:]...)

	if m.focusedSlot >= len(m.slots) && len(m.slots) > 0 {
		m.focusedSlot = len(m.slots) - 1
	} else if m.focusedSlot > idx {
		m.focusedSlot--
	}
}

// saveCurrentSlot writes the home's active UI fields back into the focused slot.
func (m *home) saveCurrentSlot() {
	if len(m.slots) == 0 {
		return
	}
	s := &m.slots[m.focusedSlot]
	s.list = m.list
	s.splitPane = m.splitPane
	s.storage = m.storage
	s.appConfig = m.appConfig
	s.appState = m.appState
}

// loadSlot copies a slot's fields onto home and updates the active workspace context.
func (m *home) loadSlot(idx int) {
	if idx < 0 || idx >= len(m.slots) {
		return
	}
	slot := m.slots[idx]
	m.focusedSlot = idx
	m.activeCtx = slot.wsCtx
	m.list = slot.list
	m.splitPane = slot.splitPane
	m.storage = slot.storage
	m.appConfig = slot.appConfig
	m.appState = slot.appState
	m.list.SetWorkspaceName(slot.wsCtx.Name)
	m.tabBar.SetWorkspaces(m.slotNames(), m.focusedSlot)
	// Resize immediately using the now-correct tab bar height. Without this,
	// the first View() after a workspace switch uses components pre-sized when
	// the tab bar had 0 names (height=0 instead of 3), producing 3 extra lines
	// that Bubble Tea clips from the top, cutting off the workspace tab bar.
	if m.lastWidth > 0 && m.lastHeight > 0 {
		listWidth := int(float32(m.lastWidth) * 0.2)
		paneWidth := m.lastWidth - listWidth
		contentHeight := m.lastHeight - m.tabBar.Height() - 2
		m.list.SetSize(listWidth, contentHeight)
		m.splitPane.SetSize(paneWidth, contentHeight)
	}
}

// applyWorkspaceToggle diffs the current slots against the desired list,
// activating and deactivating workspaces as needed.
// Activates new workspaces first so that if activation fails, the old
// workspace is still available.
func (m *home) applyWorkspaceToggle(desired []config.Workspace) tea.Cmd {
	m.saveCurrentSlot()

	desiredNames := make(map[string]bool, len(desired))
	for _, ws := range desired {
		desiredNames[ws.Name] = true
	}

	// 1. Activate new workspaces first (safe — adds to slots without removing).
	currentNames := make(map[string]bool, len(m.slots))
	for _, slot := range m.slots {
		currentNames[slot.wsCtx.Name] = true
	}
	var activationErrors []string
	for _, ws := range desired {
		if !currentNames[ws.Name] {
			if err := m.activateWorkspace(ws); err != nil {
				activationErrors = append(activationErrors,
					fmt.Sprintf("%s: %v", ws.Name, err))
			}
		}
	}

	// 2. Deactivate slots not in desired (reverse order to keep indices stable).
	for i := len(m.slots) - 1; i >= 0; i-- {
		if !desiredNames[m.slots[i].wsCtx.Name] {
			m.deactivateWorkspace(m.slots[i].wsCtx.Name)
		}
	}

	// 3. Load focused slot (or first available).
	if len(m.slots) > 0 {
		if m.focusedSlot >= len(m.slots) {
			m.focusedSlot = 0
		}
		m.loadSlot(m.focusedSlot)
	}

	m.tabBar.SetWorkspaces(m.slotNames(), m.focusedSlot)

	// 4. Surface activation errors to the user.
	if len(activationErrors) > 0 {
		return tea.Batch(tea.WindowSize(),
			m.handleError(fmt.Errorf("failed to activate: %s",
				strings.Join(activationErrors, "; "))))
	}
	return tea.WindowSize()
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
		var instances []*session.Instance
		if i == m.focusedSlot {
			instances = m.list.GetInstances()
		} else {
			instances = slot.list.GetInstances()
		}
		for _, inst := range instances {
			if !inst.Started() {
				continue
			}
			ts := sessionToTabStatus(inst.GetStatus())
			if ts > statuses[i] {
				statuses[i] = ts
			}
		}
	}
	m.tabBar.SetStatuses(statuses)
}

// slotNames returns the names of all active workspace slots.
func (m *home) slotNames() []string {
	names := make([]string, len(m.slots))
	for i, slot := range m.slots {
		names[i] = slot.wsCtx.Name
	}
	return names
}

func (m *home) View() string {
	listView := m.list.String()
	rightContent := m.splitPane.String()
	listAndPreview := lipgloss.JoinHorizontal(lipgloss.Top, listView, rightContent)

	sections := []string{}
	if tabBarStr := m.tabBar.String(); tabBarStr != "" {
		sections = append(sections, tabBarStr)
	}

	// Bottom bar: quick input or inline attach hint replaces the status line
	if m.state == stateQuickInteract && m.quickInputBar != nil {
		// Quick input is 2 lines, replaces both status line and error box so panes don't shift.
		sections = append(sections, listAndPreview, m.quickInputBar.View())
	} else if m.state == stateInlineAttach {
		hint := inlineAttachHintStyle.Render("▶ CAPTURING INPUT  ·  ctrl+q to detach, then alt+a/alt+t for fullscreen")
		sections = append(sections, listAndPreview, hint, m.errBox.String())
	} else {
		statusLine := statusLineStyle.Render("? help · q quit")
		sections = append(sections, listAndPreview, statusLine, m.errBox.String())
	}

	mainView := lipgloss.JoinVertical(
		lipgloss.Center,
		sections...,
	)

	if m.state == statePrompt {
		if m.textInputOverlay == nil {
			log.ErrorLog.Printf("text input overlay is nil")
			return mainView
		}
		return overlay.PlaceOverlay(0, 0, m.textInputOverlay.Render(), mainView, true, true)
	} else if m.state == stateHelp {
		if m.textOverlay == nil {
			log.ErrorLog.Printf("text overlay is nil")
			return mainView
		}
		return overlay.PlaceOverlay(0, 0, m.textOverlay.Render(), mainView, true, true)
	} else if m.state == stateConfirm {
		if m.confirmationOverlay == nil {
			log.ErrorLog.Printf("confirmation overlay is nil")
			return mainView
		}
		return overlay.PlaceOverlay(0, 0, m.confirmationOverlay.Render(), mainView, true, true)
	} else if m.state == stateWorkspace {
		if m.workspacePicker == nil {
			log.ErrorLog.Printf("workspace picker is nil")
			return mainView
		}
		if m.workspacePicker.IsStartup() {
			// Fullscreen: centered picker on blank background.
			return lipgloss.Place(m.lastWidth, m.lastHeight,
				lipgloss.Center, lipgloss.Center,
				m.workspacePicker.Render())
		}
		// Mid-session toggle: overlay on main view.
		return overlay.PlaceOverlay(0, 0, m.workspacePicker.Render(), mainView, true, true)
	}

	return mainView
}
