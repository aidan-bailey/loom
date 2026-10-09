# Testing pitfalls: fakes, fixtures, guards and red checks

**Symptoms:** a bug passes every unit test and several reviews, then shows in the first live run; a regression test is green before the fix as well as after; a guard's tests pass while every ordinary use of the guarded path is broken; a mutation "passes" a test that should catch it; `git status` shows a file you never meant to add, or ` D` for one you deleted.

**Cause.** A test proves only what its fixture lets happen. In this repo the same few shapes have hidden real bugs again and again: a fake or fixture that differs from production in exactly the way the bug depends on, a guard tested on one side only, and a red check that never ran against the code it claims to pin.

**Rule:** build fixtures the way production builds them, pin both sides of every guard, and watch each regression test fail before you trust it.

## Fakes and fixtures

- **A fake shares the real component's geometry.** A `fakeScrollSource` modelled the screen as `rows` lines where the emulator has `rows+1`, which collapsed a coordinate mismatch: a one-line-unreachable scroll bug passed eight reviews and a live check. When reviewing a fake, ask whether it makes the same size, coordinate and ordering assumptions as the real thing.
- **Build fixtures through the production path.** A `&Instance{...}` literal skips what production always runs. A reopened-twin guard was tested on a never-started instance, but a reconciled record is always started and Paused, so the guard never matched in production; build such fixtures through `ReconcileAndRestore`. Start completions were tested on unstarted Loading instances, workspace-terminal tick tests probed unstarted terminals, and a circuit-breaker mock always answered "alive", so every `Restart` failed with "already exists".
- **Ask what the fixture leaves nil that production fills.** A bare test home with no app state made `applyStoredRatio` a no-op, so a ratio-revert bug was invisible to every test until one used a real `config.LoadStateFrom(t.TempDir())`.
- **A fixture that rebuilds every view hides a missing applier.** `wireCore` (`app/testcore_test.go`) seeds each slot's views fresh, so a test of a published event must change model state and drain without the reseed. Details: [`adding-a-core-event.md`](adding-a-core-event.md).
- **A synchronous test client hides ordering.** The app tests' client pings before every read and after every cast, so a production path that casts and then reads back the effect passes there. Ordering tests belong in `core/rpc`, which uses production clients.
- **A parity probe pins only the corpus you chose.** A `send-keys -l` against PTY-write parity gate passed until a reviewer tried a trailing `;` and 17 KiB of text, which falsified the design. Attack a gate's corpus, not just its verdict.

## Guards

- **Pin both sides.** For every "skip if X" or "refuse when X" guard, test the case it must spare and the ordinary case it must still act on, against real dependencies where mocks would answer "nothing there". A Kill guard ("don't close a session another workspace holds") was tested only with a foreign session; it read the record's home after Kill had cleared it, so every normal kill left the agent running and deleted its worktree. Take a guard's inputs from the snapshot the operation took before it cleared anything.
- **Mutation-check each term of a compound guard separately.** Tightening `a || b` with a new term let two tests reach the same branch through the new term, so they stopped pinning the old one, and nothing failed when it was removed.
- **Copies need freshness tests, one per reread site.** Where the TUI rereads a view after an action (`syncViews` and friends), a test fails when that reread is removed (`app/views_freshness_test.go`); a copy that is read again after an action is the bug to look for.

## Proving a test goes red

1. Stage or commit the fix first. Restoring a mutated file with `git checkout <file>` discards your uncommitted fix in that file too; one guard was lost that way and found only by grepping.
2. Mutate the source (revert the fix, or flip the guard), run the test, see it fail for the reason you expect, then restore by re-applying the edit.
3. Keep mutation backups in the scratchpad, never in the worktree (next section).
4. `go test -overlay` is a cleaner mutant, with two catches. Its keys must be the path Go resolves: this checkout is reached through a symlink, so build each key from `go list -f '{{.Dir}}' ./pkg` or the overlay is silently ignored and every mutant "passes". And tests that parse source themselves (the enforcement tests, through go/parser) read the disk and never see an overlay; check those with a temporary real file outside the worktree's tracked tree, or a copy of the repo.

## The worktree's index

Loom runs `git add -N .` in every session worktree on each diff probe (`session/git/diff.go`), and a session's own repo checkout is such a worktree. A file that exists there even for seconds gets an intent-to-add entry: new files show as `A` before anyone stages them, and one deleted afterwards shows as ` D`, a "deleted" file that was never committed. A mutation backup of `core/load.go` once appeared this way in another agent's `git status`.

- Put scratch files, backups and tool output in the scratchpad, never in the worktree.
- If one lands in the worktree, delete it and `git rm --cached` it.
- Commit with explicit `git commit -m … -- <paths>`, never `-a`, `git add -A` or `git add .`; check `git status` before every commit.
- Several agents in one worktree share one index: `git commit -- <file>` commits the whole working-tree file, another agent's uncommitted hunks included, so check the committed diff. Never amend, rebase or `git stash` there: the stash stack is shared by every worktree of the repo.

## Claude and tmux premises

- **Probe a new Claude-behaviour premise live before building on it.** A live probe of Claude's hooks falsified a key assumption of the hook-status design (the roster moves before the hook's timestamp), and `claude -p` turned out to get no scratchpad. Probe on a private server: `env -u TMUX tmux -L <name> …`, targeting windows by name (a user's `base-index` shifts numbers) and quoting `'=target'` in zsh. The opt-in contract tests run with `LOOM_TEST_REAL_CLAUDE=1` and some cost money.
- **Real-tmux tests run on their own server** (`privateTmux(t, tag)` in `session/tmux`, `isolateTmux(t)` in `core` and `app`): a tmux server exits when its last session dies, so a shared one makes the next test's `new-session` fail with "server exited unexpectedly". Keep socket paths short; `sun_path` caps them at 108 bytes, and a long `TMPDIR` overflows it.
- **Pace output a scroll test asserts on**: [`nix-build-failures.md`](nix-build-failures.md) explains tmux's burst redraw.

## What the gates won't tell you

No test checks a fake against the component it stands in for, or that a guard has a positive control. Review asks those questions, and only if someone asks them on purpose.
