# session/tmux

Tmux session management: lifecycle sessions that never attach, the TUI's attach clients and their output pump, and `tmux.Command`, the one way loom invokes tmux. Full reference in [`README.md`](README.md).

## Rules when modifying this package

### Commands and targets

- **Invoke tmux only through `tmux.Command`/`tmux.CommandOnSocket`, which pass `-u` and the pinned server.** A raw exec follows `$TMUX` to whatever server encloses the process, and without `-u` a client under no UTF-8 locale gets its output sanitized (`\t` becomes `_`), so a session listing reads as one name and the held-name guard kills another workspace's session. **Enforced** by `TestCommand_ListingSurvivesANonUTF8Client_RealTmux`, and outside `command.go` by `TestNoRawTmuxExec`, which exempts that whole file and matches only the identifier `exec` with an interpreted `"tmux"` literal, so an aliased `os/exec` import or a raw-string literal escapes it: those are **Convention**.
- **Make every target exact: `"-t", SessionTarget(name)` for session commands, `"-t", PaneTarget(name)` for pane and window commands.** A bare `-t name` prefix-matches when the exact session is gone, so a command for a dead `loom_api` lands on a live `loom_api-v2` (a kill killed the sibling, and a preview PTY sent every keystroke to the other agent); plain `=name` fails on pane commands. **Enforced** by `TestTmuxTargetsAreExact`, `TestKillIsExactMatch_RealTmux` and `TestDeadSessionNeverReachesPrefixSibling_RealTmux`.
- **Never let a session name hold `:` or `.`: `ToLoomTmuxName` maps both to `_`.** tmux creates such a name literally but parses every target's `:` and `.` as window and pane separators, so the session probes Dead, can't be killed, and a failed-start cleanup removes the worktree under the running agent. **Enforced** by `TestTmuxNames_MapTargetSeparators` and `TestColonTitle_StartsProbesAliveAndCloses_RealTmux`.

### Sending text

- **Send text with `load-buffer` + `paste-buffer -d -r` (`TypeText`, `SendPrompt`), never `send-keys -l`; send key names with `PressKeys`.** `send-keys -l` fails past about 16 KiB ("command too long") and treats a trailing `;` as a command separator even after `--`. **Enforced** by `TestTypeTextMatchesPTYWrite_RealTmux`, which compares both with a write to an attach client's PTY byte for byte.

### Session state

- **Snapshot `ptmx`, `monitor` and the emulator under `stateMu`, and never hold it across PTY or subprocess I/O.** Status scans run off the TUI's Update goroutine while the attach lifecycle swaps those fields; holding the lock across I/O stalls every scan behind a slow tmux. **Convention** — races `CC=clang CGO_ENABLED=1 go test -race` finds, or a UI that freezes behind tmux.
- **Keep lifecycle's `Session` client-free: `Start` never attaches, and only the terminal pane's shells launch through `NewTmuxSession` plus `Start`.** The TUI's pane clients own every attach; a client the daemon attached would fight the TUI's over the window size. **Enforced** by `TestSession_StartLaunchesWithoutAttaching` and `TestSession_CloseOnlyKills`.
- **Set `detach-on-destroy on` for every loom session, at `Session.Start` and again at `Restore` for sessions an older loom launched.** Under a global `off`, tmux switches a destroyed session's clients to another session, so a client keyed by the dead name shows, and types into, another agent's pane and never reads EOF. **Enforced** by `TestKilledSessionsClientExits_RealTmux`.
- **Give each pump its own exit flag, and treat a client as usable only while `Attached` holds, never by `PtmxAlive`.** A late old pump must not mark a newer attach dead, and `PtmxAlive` stays true after EOF, so a client on a relaunched session would show "[exited]" forever. **Enforced** by `TestAttached_FollowsThePump` and `TestAttached_LateOldPumpExitLeavesTheNewAttach`.
- **Never call the notifier (`tmux.SetNotifier`'s hook) while holding a lock the TUI's Update path takes.** It is `tea.Program.Send`, which blocks until Update returns. **Convention** — a total UI freeze; diagnosis in [`../../docs/claude/debugging-freezes.md`](../../docs/claude/debugging-freezes.md).

### Tests

- **Dispatch tmux mocks on `cmd_test.TmuxSubcommand(argv)`, never `argv[1]`.** argv starts `tmux -u [-S …|-L …]`, so an `argv[1]` match never fires and an `assert.Empty` on what it caught passes vacuously. **Convention** — a test that asserts nothing.
- **Run a real-tmux test on `privateTmux(t, tag)`, never the default server.** It clears `$TMUX`, kills its server at the end, and keeps the socket path under the `sun_path` cap; the developer's own server holds live agents. **Convention** — a test that kills the user's sessions, or fails with a too-long socket path under a long `TMPDIR`.
- **Adding a check that a tmux rule holds everywhere?** Extend `session/tmux/command_enforce_test.go` or `session/tmux/target_enforce_test.go`, and give the matcher a detection table, which neither has yet. **Convention** — guide: [`../../docs/claude/adding-an-enforce-test.md`](../../docs/claude/adding-an-enforce-test.md)

## Pointers

- [`../CLAUDE.md`](../CLAUDE.md) — the instances that own these sessions.
- [`../vt/CLAUDE.md`](../vt/CLAUDE.md) — the emulator the pump feeds.
- [`../../docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md) — where this package sits.
