# The advisor's run context: recent runs and the checkout

Status: design approved 2026-11-05.

Builds on [the smart scheduler](2026-11-05-smart-scheduler-design.md) (§5.1 signatures, §5.6 the advisor), whose advisor runs Haiku 5.5 at low effort since the same day (its §2).

## 1. Problem

The advisor's prior for a command no run has measured gets the duration wrong about as often as right: 41 of the 87 priors scored by 2026-11-05 15:19 came within 2× of their first run's wall time. Memory holds: 3 of 81 under-estimated it.

Whether the build is warm decides most of the misses. Of the 13 worst duration misses on 2026-11-05:

- 8 were warm builds the prior took for full compiles: they ended in 1–21 s, writing at most 131 MB;
- 2 were cold builds given a `like` whose runs had been warm: they ran 7× and 31× past the prior, writing 7.7 GB and 226 MB;
- the other 3 were two runs that failed within 2 s and a sub-second job.

The request can't show it:

- A known belief carries its learned claim and its usual and p90 durations, not its runs. A cold 700 s run and warm 8 s runs fold into one median, and the bytes each wrote, which tell them apart (23 of 141 recorded runs wrote over 1 GB, 32 under 10 MB), never reach the model.
- No run records where it ran. Every worktree of a repository shares one signature (§5.1 of the smart scheduler), while each builds into its own tree (`target/`, `node_modules/`), so a belief's runs mix checkouts, and only a run in the job's own checkout says what the job will find.
- A known belief's `NAME=value` pairs are left out, so two beliefs that differ only by `CARGO_INCREMENTAL=0` read as one command.

Success, checked from `beliefs.json` a week after the daemon runs this:

- 60% or more of the priors made since then come within 2× of their first duration run (47% before);
- memory under-estimates no more often than before (3 of 81).

## 2. Decisions

- **Show runs, not labels** (approach 1 of 3). Each known belief carries its last runs as measured, with whether each ran in the job's checkout, and the prompt says how to read them. Rejected: loom labelling each run warm or cold by a write threshold (it hides a partly warm run, like the one that wrote 131 MB, and puts a heuristic in code); loom predicting the job's own state (it needs to know which commands share a build, `cargo -p a` against `cargo -p b`, which is the model's kind of judgement).
- **The checkout is recorded on every run and used only by the advisor** (the owner, 2026-11-05). Learned claims, durations and overdue marks stay as they are. Rejected for now: durations that prefer runs from the job's checkout; claims learned from warm runs for a job expected to run warm, which under-claims memory when the guess is wrong.
- **The checkout is the directory that holds the `.git`, symlinks resolved.** On fresco one tree is reached as both `/home/aidanb/Source` and `/tb/Source`, so the submitted folder is no key; `FindRepo` resolves links already.
- **Three runs per known belief.** About 3k more input tokens a call at 30 beliefs: $0.0003 on Haiku at low effort.

## 3. Scope

In: the checkout on `Record` and `Run`; recent runs and `NAME=value` pairs on each known belief in a request; the prompt's lines on reading them.

Out: sizing from the checkout (§2); checkouts in the belief pager's text (`--json` carries them); a `CARGO_TARGET_DIR` shared across checkouts (§12); a reconcile's own `runs`, which it already gets whole and which gain the field with `Run`.

## 4. Concepts and names

- **Checkout.** The top directory of the working tree a job ran in: the directory holding the `.git` that `FindRepo` stops at, symlinks resolved, or the job's folder when no `.git` is above it. A repository's main tree and each of its worktrees are separate checkouts of one signature.
- **Recent runs** (`advisor.KnownRun`). A known belief's last runs as the advisor is told of them.
- **Here.** A recent run's `here`: it ran in the checkout of the job the request is about.

## 5. Behaviour

### 5.1 The checkout

- `sched.FindRepo(dir)` returns `(repo, folder, checkout string, err error)`: the checkout is the directory holding the `.git` it found (the `top` it walks), and `dir` resolved when there is none.
- `core`'s `jobSignature` returns the checkout with the signature, and `submitJob` stores it on the record: `Record.Checkout` (`json:"checkout,omitempty"`), empty when the job has no signature.
- `sched.RunOf` copies it to `Run.Checkout` (`json:"checkout,omitempty"`).
- Neither `jobs.json`'s nor `beliefs.json`'s version changes: both fields are optional, and a build without them ignores them (and drops them when it rewrites the file, losing nothing it used).

### 5.2 Recent runs in a request

`advisor.Known` gains:

- `Env []string` (`json:"env,omitempty"`): the belief's signature pairs, through `AllowedEnv`, the filter the job's environment goes through, since the pairs come from other jobs' command lines;
- `Recent []KnownRun` (`json:"recent,omitempty"`), from `advisor.RecentRuns(runs []sched.Run, checkout string, now time.Time) []KnownRun`: the newest `MaxRecentRuns` (3) runs, newest first, each as

| Field | From the run | |
|---|---|---|
| `outcome` | `Outcome` | `done`, `failed`, `signal` or `cancelled` |
| `minutes_ago` | `now − Ended` | whole minutes, at least 0 |
| `cores` | `Claim.CPUs + Spare` | the cores it ran with; left out for an exclusive claim |
| `wall_seconds` | `WallSeconds` | rounded |
| `peak_gb` | `PeakKB` | GiB to one decimal |
| `wrote_mb` | `WriteBytes` | MiB, rounded; left out when the run measured no I/O |
| `here` | `Checkout` | true when it is the job's checkout, false when it is another, left out when either is unknown |

`core`'s `known` takes the job's checkout and the time and fills both fields; `advisorInputFor` passes the job record's checkout, for a prior's known beliefs and a reconcile's belief alike.

### 5.3 The prompt

`prompt.md` gains, after "Reason from the measurements in "beliefs"…":

> Each belief's "recent" lists its last runs, newest first. A run with "wrote_mb" of 1024 or more built from cold; one that wrote little reused an earlier build. A run whose outcome is not "done" stopped early, so its time and memory are lower bounds. "here" says whether a run was in this job's checkout, where it left its build output: expect this job to reuse a build when a recent run here built the same crates or packages with the same flags and environment, and to build from cold otherwise. Don't give a "like" whose runs reused a build for a job you expect to build from cold, or the other way round: give your own numbers.

### 5.4 Runs recorded before the change

They have no checkout, so their `here` is left out; the model still has what they wrote. Three runs per belief reach a request, so new runs push them out as they land.

## 6. The CLI, the view and the context

Nothing changes. `loom job beliefs --json` carries each run's checkout.

## 7. Wire

`sched.Run` reaches the wire inside `BeliefsPage`. The new field is optional, so `rpc.Protocol` stays: adding a field needs no bump. `docs/specs/protocol.md` is regenerated if it changes (`go test ./core/rpc -run TestProtocolReference -update`).

## 8. Components

| Package | Change |
|---|---|
| `sched` | `FindRepo`'s checkout; `Record.Checkout`, `Run.Checkout`, `RunOf` |
| `sched/advisor` | `KnownRun`, `MaxRecentRuns`, `RecentRuns`; `Known.Env` and `Known.Recent`; `prompt.md` |
| `core` | `jobSignature` and `submitJob` store the checkout; `known` fills `Env` and `Recent` |
| docs | `sched/README.md`, `sched/advisor/README.md`, a pointer from the smart scheduler spec's §5.6 |

## 9. Failure handling

- `FindRepo` fails: no signature and no checkout, as today; nothing is recorded.
- A run with no checkout, or a job with none: `here` is left out.
- A run that failed, was signalled or was cancelled is listed with its outcome; the prompt says its numbers are lower bounds.

## 10. Testing

- `sched`: `TestFindRepo` checks the checkout in each layout it covers (a checkout, a worktree, a relative `gitdir:`, no `.git`) and through a symlinked path; `TestRunOf` copies it; a store round trip keeps it.
- `sched/advisor`: `TestRecentRuns`: newest first and at most three; `minutes_ago`; the rounding of wall time, peak and bytes; `wrote_mb` absent when unmeasured; `here` true, false and absent (either checkout unknown); `cores` with spare cores, and absent for an exclusive claim.
- `core`, through the production path (real shims, a temporary repository with a worktree): a command runs in the main checkout; then another command is submitted in the main checkout and one in the worktree. The fake claude's saved requests show the first command's run `here: true` in the first request and `here: false` in the second, its pairs in `env` with a pair whose name holds `TOKEN` left out; each record's checkout is its resolved checkout.
- Live: the contract test (`-tags advisor_live`) still passes.

## 11. Build order

1. `sched` and `core`: the checkout, from `FindRepo` to the record and the run.
2. `sched/advisor` and `core`: recent runs, the pairs and the prompt.
3. Docs.

## 12. Known limits and later work

- A `CARGO_TARGET_DIR` or other build cache shared by checkouts makes `here` stricter than the build: a run elsewhere may have warmed it. The variable is in the request when the job sets it, and the model can weigh it.
- A checkout's build goes stale as its code changes: a run here hours ago, before a rebase, may not have left a warm build. `minutes_ago` is all the request says.
- An adopted run's peak is a floor (sampled from the daemon's restart) and is shown like a full one.
- Sizing from the checkout (§2), once runs carry it.
