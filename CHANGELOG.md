## [0.1.2] - 2026-05-04

### 🚀 Features

- *(script,ui)* Add inst:send_terminal_keys lua method
- *(script,app)* Wake panes from scroll mode on input/attach

### 🐛 Bug Fixes

- *(session,app)* Persist instances during kill window

### 📚 Documentation

- *(script)* Add open_emacs sample script

## [0.1.1] - 2026-04-22

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

### 🐛 Bug Fixes

- *(config)* Emit both-dirs-present warning to stderr, add migration tests
- *(daemon)* Eliminate tickInstanceTimeout race

### 🚜 Refactor

- Rename Go module to github.com/aidan-bailey/loom
- Rename runtime identifiers and add claude-squad → loom migration
- Update release infrastructure for loom rebrand

### 📚 Documentation

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

### ⚙️ Miscellaneous Tasks

- *(ci)* Remove GitHub Pages deployment
- *(release)* Adopt git-cliff changelog and auto-triggered release
- *(build)* Upgrade setup-go action to v5
- *(lint)* Exclude vendor/ from gofmt check
- *(ci)* Enforce revive.exported docstrings across the repo
