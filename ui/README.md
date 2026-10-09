# ui

Bubble Tea view components. Everything here renders `core.InstanceView` copies that `app/` hands in: the rail, cards, overview, menu, split pane, panes and workbench hold copies, never an instance, and none of them mutates the model. Event handling lives in `app/`.

## Layout

- **Rail** (`list.go`, 20% width, hidden with `\`). It reads its rows from an `InstanceSource`: the slot's view store with the TUI's overlays and draft row (app's `slotRows`). The list only reads it, so adds, removals and replacements are edits of the store. It keeps the selection by `InstanceID` (`SelectID`), re-resolved on every read (`resolveSelection`): a row added or removed above it moves its index, not the selection, and a selection whose row is removed moves to the row that slid into its place, or the new last row (`lostSelectionRow`). Rows are live mini-cards built by `card.go` (title and output tail, with a left accent bar: gold = needs input, blue = selected, green = running, purple = workspace terminal), with dimmed peer-workspace summaries at the bottom. Branch and diff stats live in the agent pane's title and the overview cards, not the rail. A Prompting card shows Claude's own wait reason verbatim, truncated, in place of the generic "awaiting input" (`CardData`, `statusLabel`), and a stopped Claude session's card shows the end of its last message (`MessageTailLines`).
- **Right panel** (`split_pane.go`, 80% width): agent and terminal panes stacked vertically (70/30 default split, resized with `ctrl+up`/`ctrl+down`, terminal hidden with `T`) and a hotkey-toggled diff overlay (`diff.go`). `SplitPane.HitTest` maps a cell to pane content for mouse selection, which works over the rendered text (`selection.go`).
- **Overview** (`overview.go`): the fleet card grid of overview mode.
- **Workbench** (`workbench.go`): the right half of workbench mode, with markdown (`markdown.go`; Glamour styles in `markdown_style.go`), diff, files, terminal and review tabs. It holds the review pane through the `ReviewPane` interface.
- `quick_input.go` is the inline input bar that sends text to tmux; `workspace_tab_bar.go` renders the workspace tabs; `account_strip.go` the accounts row (`AccountStrip`, whose `SetWarning` leads the row with the credential-override warning); `menu.go` the bottom menu; `err.go` the error and info bar.

## Panes and scrolling

- `panes.go` holds `PaneClients`, the agent panes' attach clients keyed by tmux session name (two open workspaces sharing a title share one client), and `Pane`, the surface every agent-pane render, scroll, cursor, mouse event, paste, inline-attach key and status scrape goes through. `Ensure` attaches a client; `Retain` and `Replace` drop clients from the registry and hand them back for closing; `Alive` and `Pane.Attached` say whether a client is usable (`TmuxSession.Attached`). `Ensure` leaves a client whose pump exited on its own within `quickExitHoldoff` of attaching to the health tick, warning once per burst (`pane.client_exited_after_attach`).
- `scroll.go` defines `ScrollModel`, the shared scroll state machine (offset, anchoring, wheel damping, alt-screen routing). `PreviewPane` (the agent pane, `preview.go`) and `terminal.go`'s `TerminalPane` delegate to it on the emulator path, where it windows the history seeded at attach plus the emulator's scrollback and screen (`RenderWindow`, `ScrollbackLen`). Each pane falls back to legacy capture-pane windowing when its client has no emulator (`LOOM_PANE_RENDERER=snapshot`, Windows).
- `terminal.go` renders a pane from the embedded VT emulator, with a jump-to-bottom footer and mouse drag-select/copy. The terminal pane also owns the `loom_term_` shells, one per started row: it starts one only for an active row (`ensureSessionLocked`) and lets go of them with `DetachAll`/`DetachExcept`, which leave the shell running.

## Themes

`theme.go` defines the color-role vars, `ApplyTheme` and `RegisterThemeHook`. Every theme-derived style is built inside a hook, so a theme picked from the settings overlay's Theme row repaints at once.

## Sub-packages

- `ui/overlay/` — modal dialogs: text input, text, confirmation, branch, profile and workspace pickers, file explorer, settings (with the Accounts and Profiles managers and the Claude Preferences screen), merge picker, Session Launch Options and issue picker. The settings overlay's `SetAccountNotice` puts a screen-wide notice above the Accounts screen's rows (`AccountsManager.SetNotice`); Session Launch Options' `SetAccountNotice` puts one under its Account row.
- `ui/review/` (package `reviewui`) — the embedded review pane, vendored from kevindutra/crit (MIT, see `NOTICE.md`) with theme-hooked styles: doc-review and code-review modes behind one `Pane` type, which implements `ui.ReviewPane`. `NewDocPane`/`NewCodePane` build it; `LoadCmd` reads documents, diffs and review state off the Update goroutine; `Busy()` reports a capture-all state (comment modal, tab search, visual selection); while idle, `claimsIdleKey` claims only the keys the pane acts on; persistence results arrive as `SavedMsg`. Package `ui` holds only the `ReviewPane` interface, because `reviewui` imports `ui`.

## Tests

`ui/main_test.go` isolates the package from the developer's tmux server and loom dirs. Fake attach PTYs (here and in `app/`) are `internal/testpty` pairs, open until the test ends or closes the peer: a `/dev/null` PTY reads EOF at once, which a client takes for a dead session (`Attached` false). Rendering geometry is measured, not reasoned about: `ui/pane_border_test.go`, `ui/card_test.go` and `ui/overview_test.go` render and measure.
