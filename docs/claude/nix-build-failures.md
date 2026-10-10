# A test passes locally but fails in the Nix build, CI or under load

**Symptoms:** `nix build .#loom` (or a NixOS rebuild that builds loom from this flake) fails in `checkPhase` on a test that passes on your machine; the failure tail names one package while others are broken too; a time comparison differs only in its location; a tmux listing comes back with `_` where tabs were; a real-tmux scroll test gets "exactly 10" lines on a 10-row pane; a tmux server or the daemon fails to bind its socket.

**Cause.** The Nix sandbox and CI differ from a developer's shell in ways the code notices:

| What | Sandbox | CI (ubuntu) | Your shell |
|---|---|---|---|
| Time zone | no `/etc/localtime`, `TZ` unset: `time.Local` is UTC | UTC | yours |
| User and home | `nixbld`, `HOME=TMPDIR=/build` (default branch prefix `nixbld/`) | `runner` | yours |
| Locale | no `LANG` or `LC_*` | set | set |
| `$TMUX` | unset | unset | set, inside loom |
| `.git` | absent: no VCS stamp in the binary | present | present |
| Load | shares the machine with a whole rebuild | shared runners | idle |

`buildGoModule` runs the check phase one package dir at a time and stops at the first failing package, so the tail you see hides every later package's failures. `flake.nix` excludes `tools` and `e2e` (`excludedPackages`), and everything else runs, `TestProtocolReference` included.

**Rule:** reproduce the sandbox's environment before changing a test, read the whole build log, and fix the dependency on the environment rather than the assertion.

## Reproduce

1. **Approximate locally:** `env -u TMUX -u LANG -u LC_ALL -u LC_CTYPE TZ= CGO_ENABLED=0 go test ./...`. Run it from inside loom too: `$TMUX` alone masks a tmux client's locale bug, since tmux treats a client with `$TMUX` set as UTF-8.
2. **Read the whole log:** `nix log <drv>` for the failed derivation, not the tail the build printed.
3. **Build as the consumer does.** A NixOS config that builds loom pins its own nixpkgs, while loom's `flake.lock` pins another, which downloads a whole toolchain. Build with the consumer's: `nix build .#loom --no-link --override-input nixpkgs github:NixOS/nixpkgs/<rev from the consumer's flake.lock>`. A fix reaches a consumer that builds from GitHub only after a push and a `nix flake update` of its loom input.
4. **Instrument the real sandbox** when the approximation passes: write a scratch `<scratchpad>/diag.nix`, outside the worktree, with `pkgs.buildGoModule`, `src = builtins.path` over the repo, a postPatch step that copies in a throwaway diagnostic `_test.go`, a trivial build phase, and a check phase that prints the environment, runs `go test -v -run <pattern> ./<pkg>`, then `exit 1` so the log is kept. Build it with `nix build --impure -f diag.nix` and read it with `nix log $(nix eval --impure --raw -f diag.nix drvPath)`. Test logs land in `$TMPDIR/loom.log`; set `LOOM_LOG_LEVEL=debug` for more.
5. **Reproduce load** for timing failures: start one busy loop per CPU (`timeout 100 sh -c 'while :; do :; done' &`, once per core), then run the test.

## Known causes

- **Times.** With `time.Local` as UTC, a JSON round trip of a local time changes its `Location`, so an `assert.Equal` on times differs only there. Compare instants (`Equal`, or `time.Now().Round(0)` fixtures), never `Location`.
- **tmux under no UTF-8 locale.** Without `$TMUX` or a UTF-8 `LC_ALL`/`LC_CTYPE`/`LANG`, tmux sanitizes a client's output: every byte outside printable ASCII, tabs included, becomes `_`, so a `-F '#{session_name}\t#{session_path}'` listing reads as one name. `tmux.Command` passes `-u` for this; a test that runs tmux some other way meets it.
- **User names.** The default branch prefix comes from the user, `nixbld/` in the sandbox; a test asserting a literal prefix fails there.
- **Burst redraw.** tmux repaints attached clients on its own flushes. When more than a screenful arrives between two flushes (a loaded machine, the sandbox during a big rebuild), it clears and redraws the last screen instead of scrolling, so at most one screen reaches the emulator's scrollback: "got exactly 10" on a 10-row pane, where a missing alt-screen filter would give 0. Pace the test's output (a short sleep per line) and gate later phases on the last line reaching the screen; never assert on burst output (`session/vt/CLAUDE.md`).
- **Socket paths.** A unix socket path is capped at 108 bytes (`sun_path`), and a long `TMPDIR` overflows it: tmux can't create its server socket, and real-tmux tests keep theirs short for this reason. The daemon uses `<globalDir>/run/serve.sock` only when the path fits (`maxSocketPath`), else a socket in the temp dir.
- **Unique sockets per run.** A tmux socket named only by PID races the previous server's teardown under `-count>1`; make each test's socket unique per invocation.
- **No VCS stamp.** The sandbox has no `.git`, so the binary carries no `vcs.time`; `flake.nix` stamps the commit time through ldflags instead (`rpc.commitUnix`). Code that reads build info must tolerate its absence.

## What the gates won't tell you

CI runs neither the Nix build nor the sandbox's environment, so a test can be green in every workflow and still break a NixOS rebuild. Run the approximation in step 1 before merging anything that touches time, locale, tmux or paths.
