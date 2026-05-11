## [0.1.3] - 2026-05-11

### 🚀 Features

- *(session)* Add orphan worktree discovery
- *(ui/overlay)* Add orphan recovery picker overlay
- *(app)* Wire orphan recovery into startup flow

### 🐛 Bug Fixes

- *(config,app)* Preserve workspace registry on picker toggle
- *(session,app)* Restart dead workspace terminal tmux sessions
- *(session)* Preserve instances on transient reconcile failures
- *(app)* Round-trip global ↔ workspace mode transition
- *(app)* Trigger orphan recovery on workspace registration
- *(session,app)* Preserve reconcile failures across all load paths
- *(app)* Route recovered orphans to their source workspace
## [0.1.2] - 2026-05-04

### 🚀 Features

- *(script,app)* Wake panes from scroll mode on input/attach
- *(script,ui)* Add inst:send_terminal_keys lua method

### 🐛 Bug Fixes

- *(session,app)* Persist instances during kill window

### 📚 Documentation

- *(script)* Add open_emacs sample script
## [0.1.1] - 2026-04-21

### 🚀 Features

- *(session/files)* Add git-aware file listing package
- *(ui/overlay)* Add fuzzy subsequence matcher
- *(ui/overlay)* Add file explorer overlay
- *(script)* Add toggle_file_explorer action and intent
- *(app)* Wire file explorer overlay into state machine

### 🐛 Bug Fixes

- *(session,app)* Make storage deletion idempotent on kill path
- *(app,ui)* Capture repo name before kill to prevent counter leak

### ⚙️ Miscellaneous Tasks

- *(release)* Pass changelog file path directly to goreleaser
- *(ui)* Rebrand fallback splash from claude-squad to loom
## [0.1.0] - 2026-04-20

### 🚀 Features

- *(session/git)* Add support for checking out worktrees ([#5](https://github.com/aidan-bailey/loom/pull/5))
- *(ui, session/worktree)* Show `git diff` output in diff pane ([#9](https://github.com/aidan-bailey/loom/pull/9))
- *(app, session, ui)* Add support for poweruser instance creation ([#15](https://github.com/aidan-bailey/loom/pull/15))
- *(ui)* Allow scrolling diff pane with mouse scroll ([#17](https://github.com/aidan-bailey/loom/pull/17))
- *(ui)* Underline menu items on select ([#19](https://github.com/aidan-bailey/loom/pull/19))
- *(ui/overlay)* Add cursor to text input, allow moving cursor
- *(session/worktree)* Open submitted branch
- *(ui)* Add help screen ([#34](https://github.com/aidan-bailey/loom/pull/34))
- *(app, ui)* Add help screen ([#36](https://github.com/aidan-bailey/loom/pull/36))
- *(app/help)* Only show just-in-time help screens once ([#37](https://github.com/aidan-bailey/loom/pull/37))
- *(web)* Add landing page ([#77](https://github.com/aidan-bailey/loom/pull/77))
- *(web)* Add video to landing page ([#78](https://github.com/aidan-bailey/loom/pull/78))
- *(web)* Add copy button for install script ([#79](https://github.com/aidan-bailey/loom/pull/79))
- *(worktree)* Add configurable branch name prefix
- *(release)* Add brew section to goreleaser ([#99](https://github.com/aidan-bailey/loom/pull/99))
- Confirmation modal for `D` and `p` ([#105](https://github.com/aidan-bailey/loom/pull/105))
- *(web)* Add brew instructions to landing page ([#108](https://github.com/aidan-bailey/loom/pull/108))
- Detect username for branch prefix
- Add Gemini support ([#145](https://github.com/aidan-bailey/loom/pull/145))
- *(ui)* Support scrolling in preview pane ([#176](https://github.com/aidan-bailey/loom/pull/176))
- Show working indicator while creating worktrees ([#252](https://github.com/aidan-bailey/loom/pull/252))
- Add Terminal tab for interactive shell access ([#247](https://github.com/aidan-bailey/loom/pull/247))
- Enable selecting source branch for session ([#262](https://github.com/aidan-bailey/loom/pull/262))
- Allow configuring preset profiles for creating sessions ([#264](https://github.com/aidan-bailey/loom/pull/264))
- *(config)* Support CLAUDE_SQUAD_HOME env var for custom config directory
- *(config)* Add workspace registry and CLI subcommands
- Add workspace switching UI and startup detection
- *(cli)* Add workspace migrate command
- Add workspace tab bar UI, navigation keys, and state isolation
- Add workspace slot infrastructure and convert picker to checkbox toggle
- Wire workspace slots into app lifecycle and delete old reload path
- *(ui)* Show prompt indicator on workspace tabs when sessions await input
- *(ui)* Add startup mode to workspace picker overlay
- *(cli)* Add workspace use, rename, and status subcommands
- *(cli)* Add optional directory arg and fullscreen workspace picker
- *(keys)* Add KeyQuickInteract keybinding for 'i' key
- *(ui)* Add QuickInputBar component for inline tmux input
- *(ui)* Add SendPrompt to TerminalPane and TabbedWindow
- *(app)* Wire stateQuickInteract state machine, view rendering, and menu state
- *(session)* Add permanent workspace terminal pinned at top of instance list
- *(ui)* Add distinct highlight styling for workspace terminal
- *(keys)* Add h/l as workspace tab navigation aliases
- *(app)* Add keyMsgToBytes conversion for tmux PTY forwarding
- *(keys)* Add inline attach state and fullscreen attach keybinding
- *(app)* Implement core inline attach behavior
- Increase inline attach refresh rate to ~30fps
- *(ui)* Add prompting status indicator for permission prompts
- *(app)* Use Esc to detach inline attach and add capturing indicator
- *(keys)* Remap vim navigation from hjkl to jkl;
- *(keys)* Add KeyDiff binding for d key
- *(ui)* Add SplitPane component for agent+terminal layout
- *(ui)* Update menu for split pane layout with diff hotkey
- *(app)* Replace TabbedWindow with SplitPane in app
- *(app)* Add confirmation dialog before pausing a session
- *(ui)* Add titled pane borders and replace menu with status line
- *(keys)* Add targeted input and direct attach shortcuts
- *(app)* Auto-focus agent pane after new session creation
- *(session)* Add Deleting status to Instance enum
- *(ui)* Render Deleting status with ✕ icon in instance list
- *(app)* Set Deleting status immediately on kill confirm, revert on failure
- Add alt+a/alt+t fullscreen attach, remove broken O binding
- *(config)* Add AtomicWriteFile helper (temp+fsync+rename)
- *(session)* Add RWMutex to Instance, guard Status access
- *(daemon)* Reload instances.json on every poll tick
- *(session)* Add crash recovery health check and action determination
- *(app)* Reconcile instance state on startup for crash recovery
- *(session)* Clean up orphaned tmux sessions on startup
- *(session)* Append --continue to claude on crash recovery restart
- *(session)* Checkpoint save during Pause/Resume for crash resilience
- *(ui)* Grey out and skip deleting instances
- *(app)* Restore open workspace tabs across launches
- *(script)* Add Lua scripting engine with sandbox and userdata API
- *(app)* Dispatch Lua scripts after built-in key miss
- *(script)* Add Intent types and IntentID generator
- *(script)* Add coroutine tracking and Engine.Resume
- *(script)* Add cs.await for coroutine-based intent waiting
- *(script)* Add cs.bind and cs.unbind with coroutine-run actions
- *(script)* Expose sync cs.actions primitives via Host
- *(script)* Add deferred cs.actions lifecycle primitives
- *(script)* Add cs.actions.new_instance/show_help/open_workspace_picker
- *(script)* Add cs.actions attach + quick_input primitives
- *(app)* Drain script intents into scriptDoneMsg
- *(app)* Dispatch script intents via existing runXYZ handlers
- *(script)* Embed defaults.lua and load before user scripts
- *(app)* Add --no-scripts flag and narrow reserved keyset
- *(app)* Hard-reserve ctrl+c ahead of script dispatch
- *(log)* Add Debug level, subsystem loggers, and --log-level flag
- *(log)* Add context-based trace IDs for cross-goroutine correlation
- *(script)* Thread ctx through dispatch and trace keystroke flow
- *(session)* Add per-instance logger and lifecycle entry/exit logs
- *(daemon)* Add per-tick visibility and migrate to KV logging
- *(git)* Log every git command and worktree lifecycle ops
- *(script)* Add Engine.CleanupAllCoroutines for graceful drain
- *(app)* Drain script coroutines and close engine on shutdown
- *(log)* Enforce level gate on legacy loggers and rotate mid-run
- *(ui)* Add scroll primitives to preview, terminal, diff panes
- *(ui)* Route keyboard scroll through SplitPane dispatchers
- *(ui)* Add page and jump navigation to session list
- *(script)* Expose scroll and list nav primitives to Lua
- *(script)* Bind scroll and list navigation keys in defaults.lua
- *(app)* Route mouse wheel by cursor position
- *(ui)* Show scroll position indicator in pane titles

### 🐛 Bug Fixes

- *(app)* Allow quitting with Ctrl-C
- *(session)* Bound SIGWINCH channel to prevent unbounded events during resize
- *(ui)* Remove extraneous lines from preview
- *(app, session)* Allow a freeform session title
- *(ui)* Prevent layout shift between selected and unselected items
- *(app)* Pressing escape when creating a new instance should cancel
- *(ui/preview)* Add paused empty state
- *(session/worktree)* Use HEAD commit for worktree base
- *(ci)* Release workflow
- *(ci)* Escape changelog characters
- *(ci)* Use goreleaser instead
- *(session)* Ensure we automatically pass initial prompt ([#20](https://github.com/aidan-bailey/loom/pull/20))
- *(app)* Fix delete and reset bugs ([#21](https://github.com/aidan-bailey/loom/pull/21))
- *(worktree)* Improve commit message to have human readable date
- *(ui)* Use nicer enter key icon
- *(app)* Ensure auto yes mode works on aider ([#22](https://github.com/aidan-bailey/loom/pull/22))
- *(ui)* Make menu shorter and improve underlining ([#23](https://github.com/aidan-bailey/loom/pull/23))
- *(ui)* Set size on error box
- Install script
- *(session)* Replace . with _ for tmux session names
- *(session)* Reduce text for claude autoyes mode
- *(ui/overlay)* Update wrapText to account for spaces
- *(ui/overlay)* Use textarea bubble instead of custom implementation
- *(ui)* Truncate errors that do not fit on the screen
- *(ui/preview)* Guard against negative padding
- Validate that app is used within git repository ([#30](https://github.com/aidan-bailey/loom/pull/30))
- *(tmux)* Ensure consistent state after detach error ([#33](https://github.com/aidan-bailey/loom/pull/33))
- *(ui)* Use dynamic size for prompt input and fix cursorline background
- *(ui)* Remove focused style from prompt
- *(session/worktree)* Don't open branch when pausing instance
- *(keys)* Better description for tab
- *(app)* Ensure reset correctly cleans up claudesquad instances
- *(ui)* Improve help text
- *(menu)* Add empty state, fixes action highlight
- *(help)* Reorder help sections
- *(app)* Fix attaching bugs ([#38](https://github.com/aidan-bailey/loom/pull/38))
- *(app)* Use shift tab for auto yes ([#40](https://github.com/aidan-bailey/loom/pull/40))
- *(diff)* Compute git diff in a better way ([#41](https://github.com/aidan-bailey/loom/pull/41))
- *(app)* Prevent deleting checked out instance ([#45](https://github.com/aidan-bailey/loom/pull/45))
- *(session/git)* Include untracked files in the diff
- *(app)* Ensure autoyes mode still works ([#48](https://github.com/aidan-bailey/loom/pull/48))
- *(ui)* Prevent nil deref
- *(ui)* Remove limit from prompt text input ([#55](https://github.com/aidan-bailey/loom/pull/55))
- *(session/worktree)* Commit with `--no-verify`
- *(install.sh)* Fix windows platform detection
- *(worktree)* Better error message when using cs in an uninitialized repo
- *(worktree)* Traverse dirs to find repository root
- *(app)* Don't open UI when pausing
- Rename workflow file
- *(install.sh)* Upgrade if binary already installed
- *(release)* Add base to gorelease brew script
- Don't show empty state when agents exist ([#95](https://github.com/aidan-bailey/loom/pull/95))
- Show error message when exiting tmux session without detaching ([#110](https://github.com/aidan-bailey/loom/pull/110))
- Update binary version number, fix lint workflow
- Resolve claude binary path, accounting for command aliasing
- Initialize logger in app_test.go
- `c` Command Checkout Locally Only ([#123](https://github.com/aidan-bailey/loom/pull/123))
- *(session)* Retain stdout when pausing/checking out a session ([#177](https://github.com/aidan-bailey/loom/pull/177))
- Exempt from CLA directly
- Auto-accept claude trust prompt
- Make session creation faster
- Fix nil pointer dereference panic in debug command ([#196](https://github.com/aidan-bailey/loom/pull/196))
- Sanitize final branch name to handle invalid characters from branch prefix ([#221](https://github.com/aidan-bailey/loom/pull/221))
- Proper Unicode/Chinese character handling in UI ([#234](https://github.com/aidan-bailey/loom/pull/234))
- *(ui)* Remove extra background ([#255](https://github.com/aidan-bailey/loom/pull/255))
- Correct typosquatted org name in website install command ([#259](https://github.com/aidan-bailey/loom/pull/259))
- Update claude trust prompt handling ([#263](https://github.com/aidan-bailey/loom/pull/263))
- Resolve nil panics, lost state, and silent failures across core paths
- *(tmux)* Prevent panic on dead session detach and add defensive nil checks
- *(app)* Use workspace repo path instead of process cwd for worktree creation
- Resolve resume fallback, diff baseline loss, and pause state corruption
- *(app)* Auto-pause instances with dead tmux sessions instead of spamming errors
- Add DoesSessionExist check and make quick input discoverable
- *(app)* Create workspace terminal via workspace picker and review fixes
- *(ui)* Reset scroll mode when selected instance changes
- *(ui)* Prevent accidental scroll mode entry and auto-exit at bottom
- *(ui)* Scroll up one line when entering scroll mode
- Add ShiftTab, modifier+arrow, Insert mappings to keyMsgToBytes
- Trigger WindowSize on instance death during inline attach
- *(ui)* Prevent workspace indicators from blinking on workspace switch
- *(tmux)* Drain PTY output to prevent inline attach input deadlock
- *(keys)* Update tab help text from 'switch tab' to 'focus'
- *(ui)* Address code review findings
- *(ui)* Differentiate workspace tab annotations by session status
- *(ui)* Fix layout overflow and route inline attach to focused pane
- *(ui)* Add top border to split pane to prevent agent clipping
- *(ui)* Prevent pane shift when showing quick input bar
- *(app)* Preserve state transitions from help screen OnDismiss callbacks
- *(ui)* Add viewport windowing to session list to prevent screen scroll
- *(keys)* Remap vim navigation from jkl; to standard hjkl
- *(ui)* Refresh terminal pane independently of agent content hash
- *(app)* Move state mutations out of Cmd goroutines to prevent data race crash
- *(tmux)* Replace panics in Detach with graceful error handling
- *(log)* Overhaul logging infrastructure
- *(app)* Skip Deleting instances in key handlers and metadata tick
- *(app)* Exclude Deleting instances from persistence on quit
- Update help screens and menu groups for removed keybindings
- *(config)* Write state.json atomically to survive mid-write crashes
- *(config)* Write config.json atomically
- *(config)* Write workspaces.json atomically
- *(daemon)* Write pidfile atomically
- *(config,session)* Address code review of phase 1 / phase 2 start
- *(session)* Guard Instance.diffStats and Branch with mutex
- *(session)* Guard tmuxSession/gitWorktree/started with mutex
- *(session)* Add GetBranch accessor and fuse Start's worktree writes
- *(session)* Serialize Instance fields under RLock in Snapshot
- *(session)* Make Kill idempotent, nil handles after cleanup
- *(session)* Make Start idempotent
- *(daemon)* Do not write state.json on shutdown
- *(daemon)* Respect per-instance AutoYes instead of forcing true
- *(app)* Resize slot components after tab bar update on workspace switch
- *(ui)* Size list title block for variable-width status icons
- *(ui)* Prevent terminal pane height overflow from wrapped lines
- *(session)* Wire logRecoveryAction into ReconcileAndRestore
- *(tmux)* Close prior PTY before Restore to stop FD leak
- *(session)* Stop agent on pause and resume with --continue
- *(app)* Reconcile instances on workspace activation
- *(session)* Match claude by basename in BuildRecoveryCommand
- *(session)* Add subprocess timeouts to prevent indefinite UI hangs
- *(app)* Run metadata gather off update goroutine
- *(app)* Run terminal-pane cleanup off update goroutine for kill/pause
- *(app)* Run list kill off update goroutine
- *(app)* Run instance resume off update goroutine
- *(app)* Guard Resume paused status at call site
- *(app)* Flip to Loading on pause confirm so the spinner shows
- *(session)* Avoid PTY spawn in DeleteInstance and UpdateInstance
- *(cmd)* Surface corrupt data during workspace migrate
- *(git)* Block path traversal in sanitizeBranchName
- *(git)* Delete branches during CleanupWorktrees
- *(session)* Close TOCTOU in Start idempotency guard
- *(config)* Write .gitignore atomically in EnsureGitignore
- *(script)* Close ResumeWithHost race and QuitIntent coroutine leak
- *(app)* Surface previously-swallowed TransitionTo and registry errors
- *(tmux)* Bound pump-exit wait in Close/Restore/PausePreview
- *(daemon)* Bound per-instance tick to isolate wedged sessions
- *(script)* Cs.await with no args yields last-enqueued intent id
- *(config)* Quarantine corrupt state/config on parse failure
- *(app)* Unify handleQuit save-error policy across slot paths
- *(session)* Propagate Pause/Resume saveState errors
- *(session)* Classify branch-gone errors and hint recovery on Resume
- *(git)* Use fresh context for fallback push after gh sync
- *(tmux)* Initialize monitor in constructor
- *(script)* Honour lastEnqueued in bare cs.await()
- *(script)* Align scroll bindings with approved plan
- *(log)* Return init errors, stderr rotation, and replace racy timer
- *(app)* Return startup errors via cobra RunE instead of os.Exit
- *(session)* Join errors and propagate CaptureAndProcess failures
- *(git)* Log non-absent worktree cleanup failures
- *(tmux)* Interrupt blocked pump Read via SetReadDeadline on stop
- *(daemon)* Bound per-daemon goroutine pool across ticks
- *(app)* Kill orphan tmux before workspace terminal auto-create
- *(config)* Emit both-dirs-present warning to stderr, add migration tests
- *(daemon)* Eliminate tickInstanceTimeout race

### 💼 Other

- Add menu
- Working on layout
- Layout and up/down keystrokes done
- Added error message and finished list except for new
- Add spinners
- Add comments to spinner
- Add comments to spinner in list component
- Improve menu visibility
- Add ready icon
- Improve error styling
- Don't wrap list navigation
- Add tmux session interface
- Add worktree, pushing branch
- Context switching in/out working
- Fix window sizes ([#2](https://github.com/aidan-bailey/loom/pull/2))
- Separate start from struct initialization
- Add ability to enter name for session ([#4](https://github.com/aidan-bailey/loom/pull/4))
- Update empty state
- Add tabs to ui and render preview in tabs
- Add tab switching
- Make claudesquad two words in fallback text for small terminals
- Update instance styles to use background with no border ([#6](https://github.com/aidan-bailey/loom/pull/6))
- Show branch name in list
- Nuke control characters when attaching
- Clarify paused state better
- Allow for quitting while paused ([#8](https://github.com/aidan-bailey/loom/pull/8))
- Update statuses depending on claude stdout ([#7](https://github.com/aidan-bailey/loom/pull/7))
- Add agpl 3.0 license
- Skip 'do you trust files' screen in instance
- Add git diff +- to list card
- Show repo name when multiple repos are used ([#10](https://github.com/aidan-bailey/loom/pull/10))
- Add yolo mode ([#12](https://github.com/aidan-bailey/loom/pull/12))
- Make auto-yes mode config / cli flag only ([#13](https://github.com/aidan-bailey/loom/pull/13))
- Add log package to introduce logging channels ([#11](https://github.com/aidan-bailey/loom/pull/11))
- Make diff scroll text smaller
- Remove extra whitespace from preview window
- Add vibe coded readme file ([#14](https://github.com/aidan-bailey/loom/pull/14))
- Introduce autoyes daemon ([#16](https://github.com/aidan-bailey/loom/pull/16))
- Mention that autoyes works for claude code and aider only
- Update auto-yes text search

### 🚜 Refactor

- *(config)* Use config directory everywhere
- Convert --reset flag to reset subcommand
- *(help screen)* Improve enums ([#168](https://github.com/aidan-bailey/loom/pull/168))
- *(config)* Add WorkspaceContext type and config directory helpers
- Replace env var propagation with explicit workspace context threading
- Simplify config loading, remove redundant state, and fix consistency issues
- *(ui)* Remove TabbedWindow (replaced by SplitPane)
- Remove tab, i, enter/o, shift+up/down keybindings
- Route all Instance.Status access through Get/SetStatus
- *(session)* Snapshot IsWorkspaceTerminal under Kill's lock
- *(keys)* Remap to vim keys k/j for nav, l/; for workspace tabs
- *(tmux)* Replace custom attach with tea.ExecProcess
- *(cmd)* Plumb cmd.Executor through git worktree ops
- *(session)* Add Status state machine with allow-list transitions
- *(session)* Introduce SessionBackend for worktree vs terminal routing
- *(ui)* Centralize theme colors and layout constants
- *(overlay)* Add Overlay interface with unified HandleKey
- *(config)* Require explicit dir in Load helpers and thread WorkspaceContext
- *(app)* Adopt overlay/theme/status primitives and add ActionRegistry
- *(theme,help,log)* Consolidate colors, unify help text, guard Every mutex
- *(config,daemon,session)* Thread WorkspaceContext, dedup Executor, drop SetStatus shim
- *(app)* Finish ActionRegistry migration; delete handleKeyPress switch
- *(overlay)* Collapse four overlay pointers to one + kind tag
- *(app)* Split handleKeyPress into per-state handlers
- *(session)* Introduce AgentAdapter consolidating per-agent logic
- *(session,daemon)* Decouple FromInstanceData from PTY attach (DAEMON-05)
- *(storage,cmd)* Add SchemaVersion + Migrate, harden workspace migrate
- *(config,log)* Remove Config.configDir, add slog backend + test gaps
- *(script)* Cs.register_action becomes thin alias over cs.bind
- Retire Go ActionRegistry in favor of Lua-driven keymap
- Migrate legacy Printf sites to structured log.For
- Rename Go module to github.com/aidan-bailey/loom
- Rename runtime identifiers and add claude-squad → loom migration
- Update release infrastructure for loom rebrand

### 📚 Documentation

- Update CONTRIBUTING.md
- Update README to reflect CLI usage
- Update README.md
- Update README.md
- Add demo video to README
- Update README.md
- Add homebrew installation steps
- Update README.md
- Fix Gemini CLI link
- Add CLAUDE.md with project guidance for Claude Code
- Update CLAUDE.md with CLI usage, env vars, and fix session lifecycle
- Update CLAUDE.md with workspace feature and fix outdated details
- Add workspaces spec and link from CLAUDE.md
- Update workspace spec for WorkspaceContext and new CLI commands
- Update CLAUDE.md for workspace context refactoring and new CLI commands
- Add comprehensive USAGE.md with TUI layout, lifecycle, and CLI reference
- Update CLAUDE.md with keybindings, workspace terminals, CI, and missing references
- Update keybindings for inline attach and fullscreen modes
- Add interactive preview design and implementation plan
- Add design for diff tab removal and split pane layout
- Add implementation plan for diff tab removal
- Add design for immediate UI feedback on session deletion
- Add implementation plan for immediate delete UI feedback
- Add design for removing tab-based pane selection
- Add implementation plan for removing tab-based pane selection
- Update keybinding references for removed keys
- Add implementation plan for race condition fixes
- *(daemon)* Cleanup orphaned shutdown comment, note DAEMON-05 cost
- Add crash recovery design for OOM/unclean shutdown resilience
- Add crash recovery implementation plan
- Refresh CLAUDE.md for action dispatch + schema/log additions
- Document Lua scripting package and gotchas
- Add scripting spec and link from CLAUDE.md index
- *(plans)* Scriptable hotkeys migration design
- *(plans)* Scriptable hotkeys implementation plan
- Update scripting spec for bind/actions/await migration
- *(rebrand)* Rewrite docs, web site, and user-facing strings for loom
- *(usage)* Add workspace terminals, profiles, branch prefix, daemon troubleshooting
- *(go)* Add package comments to core packages
- *(session/git)* Document worktree-from-storage constructors and DiffStats.IsEmpty
- *(config)* Document Profile struct and fields
- *(session)* Elevate Instance, NewInstance, and Migrate docstrings
- *(session/tmux)* Document program constants, TmuxSession, and PTY factory
- *(script)* Document intent struct types and attach-pane constants
- *(log)* Document legacy logger vars and NewEvery
- *(app,keys)* Elevate Run, GlobalInstanceLimit, and KeyName/GlobalkeyBindings
- *(ui)* Document List, SplitPane, PreviewPane, DiffPane, TerminalPane
- *(ui/overlay)* Add package comment
- *(ui,keys)* Fill remaining exported-symbol gaps
- *(session/agent)* Document adapter method implementations
- *(app)* Document scriptHost and home Bubble Tea model methods
- *(session,tmux,log,config)* Fill remaining exported-symbol gaps

### ⚡ Performance

- Improve worktree startup time ([#256](https://github.com/aidan-bailey/loom/pull/256))
- Optimize navigation with 8 behavior-preserving improvements
- *(tmux)* Dedup hash and remove pane buffer alloc
- *(app)* Skip diff refresh when tmux pane unchanged
- *(git)* Cache untracked-file probe in diff path
- *(config)* Skip atomic write when state bytes unchanged
- *(daemon)* Parallelise auto-yes checks with worker bound
- *(git)* Rate-limit git.cmd.ok debug stream

### 🎨 Styling

- Fix gofmt alignment in session/instance.go
- Gofmt formatting fixes
- *(script)* Tidy space after comma in engine test Dispatch calls

### 🧪 Testing

- *(tmux)* Add test for name sanitization ([#85](https://github.com/aidan-bailey/loom/pull/85))
- Add tests for config.go
- Add workspace picker overlay tests
- *(app)* Add tests for Deleting status, kill failure revert, and persistence filtering
- Remove scroll handler test for removed keybindings
- *(tmux)* Lock multi-word program argv delivery
- *(app)* Migration parity for retired hotkeys
- *(cmd)* Guard workspace_migrate mirror against field-type drift
- *(session)* Verify Kill returns bounded on stuck tmux pump
- *(script)* Guard cs.bind reserved-key silent-drop behavior
- *(log)* Cover rotateIfNeeded startup and Every rate limiter
- *(ui)* Cover list page navigation cursor math
- *(ui)* Cover pane-level scroll mode entry/exit invariants
- *(git)* Skip fresh-context test when gh CLI not on PATH

### 🔧 Build

- *(nix)* Add flake with package and dev shell
- Update vendorHash and vendor directory for new dependencies
- *(deps)* Vendor gopher-lua; prune unused indirect deps
- *(nix)* Use committed vendor/ directly instead of fixed-output hash

### ⚙️ Miscellaneous Tasks

- *(config)* Add default config, allow specifying program to spawn
- Add build + release workflows, install script ([#3](https://github.com/aidan-bailey/loom/pull/3))
- Add lint workflow, run gofmt
- Use go version 1.23 in ci
- *(readme)* Update screenshot
- *(gitignore)* Add DS_Store to gitignore
- Remove unused init code
- *(ui)* Fix auto-yes mode indicator
- *(readme)* Update readme with more usage details
- Update gorelease config
- Add version command
- Install binary as `cs` and update README
- *(readme)* Update screenshot with new menu and auto yes card ([#25](https://github.com/aidan-bailey/loom/pull/25))
- *(readme)* Use hd screenshot
- *(keys)* Shift + D to kill an instance
- *(daemon, log)* Clean up error logging ([#31](https://github.com/aidan-bailey/loom/pull/31))
- *(app)* Error handling updates ([#32](https://github.com/aidan-bailey/loom/pull/32))
- Remove unused file
- *(log)* Remove extraneous log line
- *(readme)* Put install script above features
- Update install.sh to install dependencies
- Bump version
- *(daemon)* Remove noisy log
- *(tmux)* Add test infrastructure and basic test for starting tmux session ([#92](https://github.com/aidan-bailey/loom/pull/92))
- *(release)* Use goreleaser action v6
- *(release)* Set skip_upload:false for goreleaser to ensure rb file is pushed
- *(release)* Use proper token for cross repo PR generation
- *(ci)* Don't unnecessarily run actions for non-code changes
- *(ci)* Don't override GITHUB_TOKEN env var
- Cache golangci-lint action step
- Update program description
- Use golangci-lint v1.60.0
- Run CI workflows when workflow definition changes
- Bump version
- *(brew)* Remove brew tap
- Add CLA and CLA action ([#128](https://github.com/aidan-bailey/loom/pull/128))
- Update CLA message
- *(readme)* Add faq section ([#169](https://github.com/aidan-bailey/loom/pull/169))
- Add script to bump app version
- Add exceptions for CLA
- Verify release tag matches binary version
- Bump version
- Bump version
- Bump version to 1.0.17
- Add result dir to .gitignore
- Add race-detector workflow
- Drop obsolete attach plumbing after tea.ExecProcess migration
- Add .serena to gitignore
- *(ci)* Remove GitHub Pages deployment
- *(release)* Adopt git-cliff changelog and auto-triggered release
- *(build)* Upgrade setup-go action to v5
- *(lint)* Exclude vendor/ from gofmt check
- *(ci)* Enforce revive.exported docstrings across the repo
- Gitignore git-cliff/ and .claude-squad/
