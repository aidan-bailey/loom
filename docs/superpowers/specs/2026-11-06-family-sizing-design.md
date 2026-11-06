# Family sizing: learning from a job's near relatives

Status: design approved 2026-11-06; amended while planning (marked *amended while planning*).

Builds on [the smart scheduler](2026-11-05-smart-scheduler-design.md), [job scaling](2026-11-05-job-scaling-design.md) and [dependent jobs](2026-11-05-dependent-jobs-design.md).

## 1. Problem

The scheduler learns a job's claim from earlier runs of the same signature: repo, folder, the command with its wrappers removed, the wrappers' `NAME=value` pairs and, for a dependent, its prerequisite's signature (`sched/signature.go`). One earlier run is enough for `Size` to learn from. But signatures are so exact that most jobs have none. Measured on the live `~/.loom/jobs/beliefs.json` on 2026-11-06 (577 beliefs, 857 runs over about 1.5 days):

- **Only 33% of runs had an earlier run of their own signature.** 36% on 2026-11-05, 25% on 2026-11-06: new variants arrive faster than old ones repeat.
- **What splits signatures, largest cause first:**
  - Commands that really differ: 248 beliefs are compound `sh -c`/`bash -c` scripts (`cargo nextest … && cargo clippy … && cargo fmt --check`), and test filters (`-run TestX`, nextest's positional filters) give 25 beliefs of `cargo nextest run -p prader-app-core <filter>` alone.
  - Cosmetic differences: `| tail -40` vs `| tail -30`, `2>&1`, `VAR=1 cmd` inside a script vs `env VAR=1`, flag order, `-p` order. Normalizing all of these into the signature merges only 577 → 537 (7%).
  - The prerequisite: `go test -race ./...` is 5 beliefs (18 runs split 9/5/2/1/1) because it ran after 4 different prerequisites.
- **The advisor sizes most jobs, and loosely.** 545 beliefs needed an advisor prior. For jobs peaking at 1 GB or more, an advisor prior claims a median 1.75× the job's peak (p90 3.68×), against 1.40× (p90 2.69×) for learned claims. Its durations are within 2× of the actual run 46% of the time, against 75% for learned ones.
- **Borrowing already works when the advisor does it.** A prior naming a `like` belief claims a median 1.42× the peak; one without claims 2.07×. The advisor sees only the 20 most recently seen beliefs of the repo (`knownSameRepo`, `core/queue_advisor.go`), so the right relative is often not among them.

A prototype of the family key in §5.1 (a Python replay of the store in run order) shows:

| A run could draw on | Runs |
|---|---|
| an earlier run of its own signature | 287 (33%) |
| none of its own, but an earlier run of its family | 219 (26%) |
| neither | 351 (41%) |

| Source, jobs peaking at 1 GB or more, whole-GB claims | claim ÷ peak, median (p90) | claims below the peak |
|---|---|---|
| advisor prior | 1.75 (3.68) | 7% |
| own runs (`learned`) | 1.40 (2.69) | 8% |
| family runs, margin 1.15 | 1.40 (2.34) | 13% |

Family durations (the family's median wall time) are within 2× of the actual run 71% of the time.

Success, checked from `jobs.json` and `beliefs.json` after a week of use:
- about 59% of runs sized from their own or their family's runs, up from 33%;
- family-sourced memory claims below the peak in no more than about 13% of runs;
- fewer advisor prior calls per submitted job than before.

## 2. Decisions

- **A deterministic family key, parsed per tool** (brainstorm approach A). Rejected: fuzzy token similarity (B), whose commonest near match is wrong (`cargo test -p prader-core` and `cargo test -p prader-app` share almost every token and differ several-fold in memory), needs a tuned threshold and can't say why it matched; better advisor candidates alone (C), which still makes every new job wait on a model call and freezes the borrowed numbers at the prior's time. C's ranking is kept as a complement for jobs whose family has no runs (§5.5).
- **Family history uses the same margin as a job's own runs (1.15× the largest full peak)** (owner's choice). Rejected: 1.30× (median 1.51×, 9% below the peak) and 1.50× (1.77×, 7%). The ladder (squeeze, pause, requeue) and the greedy admission's count of a running job at its measured use plus margin catch a job that outgrows its claim when memory runs short. A gate on how consistent a family's peaks are was tried in the prototype and dropped: it raised the under-claims, since a family of one run is perfectly consistent.
- **The family is a fallback, never an identity.** `SignatureID` is unchanged, a run is recorded only in its own belief, and `beliefs.json` is unchanged.
- **Family history fills claims and durations only.** Curves, holds, grants, overdue and overrun stay with a job's own runs (§5.4).
- **Rejected along the way:** cosmetic normalization of the signature (7% gain, and every changed ID orphans a belief unless `LoadBeliefs` migrates it); tags set by the session (free-text labels fragment as commands do — the same signature already carries descriptions like `g04: env CARGO_INCREMENTAL=0 …` and `final gate 4: sweep build + lab pytest cells` — and a wrong tag lends a small job's claim to a big one); kind tags (`build`, `test`, `lint`), whose spread inside one repo is 20× in memory (prader-rs tests: p10 0.22 GB, p90 5.0 GB) and which loom can derive from the command anyway.

## 3. Scope

In: `sched.FamilyOf` and its shell lexer; the family pool; `Size`, `NeedsPrior`, `Usual` and `Surprise` taking family history; the `family` claim source and `FamilyRuns`; the advisor's candidate ranking; the claim's tag and description; the family line in `loom job beliefs show`; the wire fields; the `sched/CLAUDE.md` rules.

Out (§12): changing `SignatureID`, families across repos, borrowing curves or profiles, tags of any kind, environment-sensitive families.

## 4. Concepts and names

- **Step:** one command of a job, reduced to what decides its size (§5.1), as a canonical string such as `cargo nextest run -p prader-app-core` or `go test -race ./...`.
- **Family:** a repo, a folder and the sorted set of a job's steps. `sched.Family` has `Repo`, `Folder` and `Steps []string`; `Key()` is the JSON array `[repo, folder, steps]`, `Label()` the steps joined by ` && `.
- **Family pool:** the 10 (`window`) most recent runs of every other belief of the same family, as a `*sched.Belief` with only `Runs` set.
- **`SourceFamily`** (`"family"`): a claim part taken from the family pool. The name "borrow" belongs to the memory lending of the `aidanb/changing-resource-alocation` design (`Borrow`, `Borrower`, `Lent`); this design never uses it.

## 5. Behaviour

### 5.1 The family key

`sched.FamilyOf(sig Signature) (Family, bool)` is a pure function of the signature. It reads `sig.Repo`, `sig.Folder` and `sig.Command` (wrappers already removed by `Normalize`); it ignores `sig.Env` and `sig.After`, so a family spans a command's warm and cold variants and its environments.

**Steps of a command:**
- A shell script: `sig.Command[0]` is `sh`, `bash`, `dash` or `zsh` (base name), `sig.Command[1]` is `-c` or a one-dash cluster ending in `c` (`-ec`, `-lc`, `-euc`), and `sig.Command[2]` is the script; words after it (`$0`, `$1`, …) are ignored. The lexer splits the script into steps (below).
- Anything else is one step: `sig.Command` itself.

**The lexer** reads the script as a POSIX shell would split words and lists, and no further:
- Separators between steps: unquoted `&&`, `||`, `;`, `|`, `&` and newlines.
- Words: split on unquoted blanks; single quotes, double quotes and backslash escapes are honoured and removed; `$(…)` and backquotes, balanced, stay inside the word they appear in.
- Redirections (`>`, `>>`, `<`, `2>&1`, `&>`, a digit before `>` or `<`) are dropped with their target word.
- An unquoted `#` at the start of a word drops the rest of its line.
- It fails, and the job has no family, on: an unbalanced quote, `$(…)` or backquote; an unquoted `(`, `)`, `{` or `}` outside `$(…)`; a here-document (`<<`); a word `if`, `then`, `else`, `elif`, `fi`, `for`, `while`, `until`, `do`, `done`, `case`, `esac` or `function` in command position.

**Each step**, in order:
1. Leading `NAME=value` words (`isAssignment`) are dropped, then the rest goes through `Normalize`, so `env`, `nice`, `time` and the other wrappers are removed inside a script as they are from an argv (*amended while planning*).
2. A step whose program is `tail`, `head`, `grep`, `sed`, `wc`, `sort`, `cat`, `echo`, `true`, `export` or `set` is dropped whole.
3. The program word becomes `programWord` of it (an absolute path's base name, a relative path cleaned).
4. The step's string, by program:
   - **`cd`:** `cd DIR`, DIR as written. `cd prader-app && pnpm test` and `cd prader-admin && pnpm test` are two families.
   - **`cargo`:** the subcommand is the first word that starts with neither `-` nor `+`; for `nextest`, the word after it joins it (`nextest run`). Kept, in this order: `--workspace` (also written `--all`), `--doc`, `--release`, then the crates of `-p X`, `--package X` and `--package=X`, sorted and de-duplicated, each as `-p X`. Everything else is ignored: test filters, `--lib`, `--all-features`, `--exclude`, `--no-fail-fast`, everything after `--`.
   - **`go`:** the subcommand is the first word that doesn't start with `-`. Kept, in this order: `-race`, `-tags V` (from `-tags V` or `-tags=V`), then the package patterns (words that are `.` or start with `./` or `../`, one trailing `/` dropped so `./sched/` is `./sched`, *amended while planning*), sorted and de-duplicated. Everything else is ignored, `-run`, `-count` and `-v` included.
   - **Anything else:** the program and its first two words that don't start with `-`: `pnpm exec playwright`, `python3 scripts/gen-architecture-tables.py`, `bash scripts/verify-affected.sh`, `make test`.
5. The steps are de-duplicated and sorted.

The job has no family when the lexer fails or when no step but `cd` steps remains (`echo hi`, `cd x && tail log`). A job with no family borrows nothing: it is sized as today.

### 5.2 The family pool

`sched.FamilyPool(bs *Beliefs, self *Belief) *Belief` is pure. It returns nil when `self` is nil or has no family, or no other belief of its family has a run; otherwise a belief whose `Runs` are the runs of every belief of `self`'s family but `self`, sorted newest first by `Ended` (ties by `Job`), cut to `window`. It has no ID, signature, prior or notes.

*Amended while planning:* the pool is found from a signature, not a belief, since a submit sizes its job before `Beliefs.Ensure` makes the belief. Families are memoized in a `sched.Families` the model's queue state holds, keyed by signature ID (an ID names one signature, so an entry never goes stale; the memo starts over past `2 × MaxBeliefs` entries), so the admission pass, which re-sizes every waiting job (`resizeQueued`), parses no command. An unexported field on `Belief` was rejected: `assert.Equal` compares unexported fields, so every round-trip test of `beliefs.json` would break, and a belief doesn't exist yet at a job's first sizing.

- `Families.Of(sig Signature) (Family, bool)`;
- `Families.Pool(bs []Belief, sig Signature) *Belief`: the pool of §5.2's first paragraph, `sig`'s own belief excluded by ID;
- `Families.Members(bs []Belief, sig Signature) int`: the beliefs of `sig`'s family, its own included;
- `Families.Kin(a, b *Belief) (same bool, shared int)`: for §5.5.

The model reads a pool through `queueState.familyPool(sig sched.Signature) *sched.Belief`, nil while learning is off (`beliefsErr`), and `queueState.poolOf(id SignatureID)`, through the belief's signature (nil without one). `sizeRecord` takes the submit's signature, nil from `resizeQueued`.

### 5.3 Sizing

`Size(asked Claim, b, family *Belief, p Policy) Sized`. Each part of the claim is taken from the first of these that has it: what was asked (`explicit`), the job's own runs (`learned`), the family pool (`family`), the advisor's prior (`agent`), the default. The family pool gives:

- **Cores:** the median busy cores of its runs that teach cores, × `coreHeadroom`, clamped to 1 and the default claim's cores: the job's own `haveBusy` rule over the pool's runs. A pool never makes a job an overrunner: the `sc.Overruns` case reads only `ScalingOf(b)`.
- **Memory:** the largest full peak of its runs (`largestPeak` over the pool at the claim's cores), with `withMargin` (1.15×). Only full peaks count: a family member's failed run neither sizes nor raises the job's memory. The job's own failed peaks still raise any claim that isn't explicit, family-sourced ones included. A family claim above the cap is held to it and marked `AboveCap`, as a learned one is.
- **I/O:** its runs' median bytes written against `ioThreshold`.

`Sized` and `Record` gain `FamilyRuns int` (`family_runs` in `jobs.json`): the runs of the pool, when a part is family-sourced; 0 otherwise. `Learned` keeps counting only the job's own runs.

`NeedsPrior(asked Claim, b, family *Belief) bool` counts a part the pool gives as learned, so a job whose family covers every part it didn't ask for makes no advisor call. A job whose family covers some parts still asks for the rest, as today.

`priorOf` (the `like` copy) calls `Size(Claim{}, like, nil, policy)`: it copies only the `like` belief's own learned parts, as today.

### 5.4 Durations, surprises, and what stays exact

- **`Usual(b, family *Belief, cores int) (usual, p90 time.Duration, n int, src DurationSource)`.** `DurationSource` is `DurationLearned`, `DurationFamily`, `DurationPrior` or `DurationNone`, in place of today's `fromPrior` bool. The views keep their `UsualFromPrior` field and gain `UsualFromFamily` (*amended while planning*: the wire keeps its field names). It takes the job's own duration runs, scaled as today; with none, the pool's duration runs, unscaled (a pool mixes members whose scaling differs); with none of those, the prior's duration; else none.
- **`OverdueAfter`** calls `Usual(b, nil, cores)`: overdue needs 3 of the job's own duration runs. A family of filtered runs must not flag a full suite overdue.
- **`Surprise`** counts a peak above a `family`-sourced memory claim as it counts one above a learned claim, so the advisor's reconcile runs on the job's own belief.
- **Unchanged, own runs only:** `Predict` and `CurvedRuns` (the greedy admission's curves), `Hold` and `profiledRuns`, `ScalingOf`, and `Grant`, which already lends only to a job whose cores are `learned` — a family-sourced job gets no spare cores. A pooled curve or profile could release memory a job still needs.

### 5.5 The advisor's candidates

`knownBeliefs` orders the same-repo beliefs it may show the advisor by, in turn:
1. the same family as the job first;
2. more steps shared with the job's family first (`cd` steps don't count; a belief with no family shares none);
3. most recently seen first;

and keeps the first `knownSameRepo`. The other-repo list, the counts and the prompt are unchanged. The advisor is now asked mostly when the family has no runs, and this puts the relatives a compound script shares a step with in front of it.

### 5.6 Learning and re-sizing

A settled run is recorded only in its own belief (`recordRun`), as today. Since `resizeQueued` re-sizes every waiting job at each admission pass, a family member that settles changes its waiting relatives' claims at the next pass.

## 6. Display

- **The tag** (`ClaimSources.Tag`): `family·N` for a family-sourced memory claim, N being `FamilyRuns`.
- **The description** (`ClaimSources.Describe(learned, familyRuns int, familyLabel string)`): the `family` source reads "from N runs of its family (<label>)", e.g. "cores and I/O learned from 3 runs, memory from 6 runs of its family (cargo nextest run -p prader-app-core)". The CLI's job lines, the work view and the belief pager use it.
- **Durations** (*amended while planning*): a family duration reads as an estimate, "about 3m" in the CLI's sizing line (as a prior's does), and "about 3m (its family's runs)" in the belief pager. `JobView` gains `Family` (the label) and `FamilyRuns`.
- **`loom job beliefs show`** and the work view's `b` pager gain a `family` line in the label column: the family's label and how many beliefs share it ("`cargo nextest run -p prader-app-core` (25 beliefs)"). A belief with no family shows no line. `BeliefView` gains `Family string`, `FamilySize int` and `UsualFromFamily bool`.
- `loom job beliefs` (the list) is unchanged.

## 7. Storage and older builds

- `beliefs.json` is unchanged: the family is derived from the signature it already holds.
- `jobs.json` stays at version 1. `Record` gains `family_runs` (omitempty) and `Sources` may hold `"family"`. Nothing validates source strings at load. An older daemon shows no tag for the source, treats it as no surprise source, and drops `family_runs` when it saves; its next admission pass re-sizes every waiting job its own way.

## 8. Wire

`Record.FamilyRuns`, `JobView.Family`, `JobView.FamilyRuns`, `JobView.UsualFromFamily`, `BeliefView.Family`, `BeliefView.FamilySize` and `BeliefView.UsualFromFamily` are optional fields and `"family"` a new value of an existing string, so `rpc.Protocol` stays at 2 (`core/rpc/rpc.go`). `docs/specs/protocol.md` is regenerated (`go test ./core/rpc -run TestProtocolReference -update`).

## 9. Components

- `sched/family.go` (new): `Family`, `FamilyOf`, `FamilyPool`, the step rules.
- `sched/shellwords.go` (new): the lexer (§5.1). No shell lexer is vendored.
- `sched/family.go` also holds `Families`, the memo (§5.2).
- `core/queue_ladder.go`: a requeue's new record keeps `FamilyRuns` (*amended while planning*).
- `internal/cli/job_sizing.go`, `internal/cli/job_beliefs.go`, `ui/work.go`: `Tag`'s and `Describe`'s new arguments, the family duration's "about".
- `sched/size.go`: `SourceFamily`, the `family` cases in `Size`, `Sized.FamilyRuns`, `Tag` and `Describe`.
- `sched/surprise.go`: `NeedsPrior`'s pool, `Surprise`'s source list.
- `sched/durations.go`: `Usual`'s pool and `DurationSource`; `OverdueAfter` passes nil.
- `sched/job.go`: `Record.FamilyRuns`.
- `sched/beliefs_view.go`, `sched/belief_text.go`: the family line.
- `core/queue_beliefs.go`: `familyPool`, `sizeRecord` passes it.
- `core/queue_ops.go`: `askForPrior` gets the pool.
- `core/queue_greedy.go`, `core/queue_view.go` and every other `Usual` caller: the pool and `DurationSource`.
- `core/queue_advisor.go`: `knownBeliefs`' order; `priorOf` passes nil.
- `sched/CLAUDE.md`, three rules, each with its test (§11): keep `FamilyOf` and `FamilyPool` pure, and give a job no family on any doubt; keep the family out of `SignatureID` and out of `recordRun`; keep `Predict`, `Hold`, `Grant`, `ScalingOf` and `OverdueAfter` on a job's own runs. `go run ./tools/claudemd -v` showed 3,413 bytes of headroom in `sched/CLAUDE.md` on 2026-11-06.
- `sched/README.md`: the family, the pool and the source.

The `aidanb/changing-resource-alocation` design touches `core/queue_greedy.go`, `core/queue_view.go` and `sched.Record` too; whichever branch merges second rebases those overlaps.

## 10. Failure handling

- Learning off (`beliefs.json` failed to load): no belief, no pool; sizing as today.
- A script the lexer can't read, or with no step but `cd`: no family; sizing as today.
- A family whose runs all failed: no full peak, so memory falls through to the prior or the default; cores and I/O come from whatever runs teach them.
- A forgotten belief (`loom job beliefs forget`) leaves the pools it was in at the next pass.

## 11. Testing

- **`TestFamilyOf`** (`sched/family_test.go`), a table that pins both sides of every rule:
  - merged: test filters (`-run`, nextest positional filters), `| tail -N` and the other dropped programs, `2>&1` and other redirections, leading `NAME=value`, flag order, `-p` order, `--package=X` against `-p X`, `--all` against `--workspace`, `-tags=V` against `-tags V`, `After`, `Env`, an absolute program path;
  - kept apart: `cd DIR`, `--release`, `--doc`, `-race`, a different `-tags` value, a different crate or package set, a different folder, a different repo, a different subcommand;
  - steps de-duplicated and compared as sets; `bash -ec` and `sh -c` alike;
  - no family: an unbalanced quote, a subshell, a here-document, `for … do … done`, a script of only dropped steps, a script of only `cd`.

  Fixtures use loom's own commands (`go test`, `go vet`, `sh -c '… && …'`) and invented cargo ones, not other projects' commands.
- **`TestShellWords`** (`sched/shellwords_test.go`): quotes, escapes, `$(…)` and backquotes inside a word, newlines and `&` as separators, comments, each failure case.
- **`TestFamilyPool`**: `self` excluded; members' runs merged newest first and cut to `window`; nil for no family, for a family with no other runs, for a nil `self`.
- **`TestSize`** cases: a family fills a part the job's own runs lack; the job's own runs win over the family; parts mix (own cores, family memory); a job's own failed peak raises a family memory claim; a family member's failed peak neither sizes nor raises; a family never makes an overrunner; explicit wins over family; family wins over the prior; a family claim above the cap is held and marked `AboveCap`; `FamilyRuns` set only when a part is family-sourced.
- **`TestNeedsPrior`**: a family covering every part asks nothing; a family with no full peak asks.
- **`TestUsual`**: own runs, then family (unscaled), then prior, then none, each with its `DurationSource`. **`TestOverdueAfter`**: a belief with no duration runs of its own and a family with three is never overdue; one with three of its own is, as today.
- **`TestSurprise`**: a peak above a family-sourced memory claim is a surprise; below it is not.
- **`TestGrant`**: a job whose cores are family-sourced gets no grant; the same job with learned cores does, as today.
- **`TestSignature_IDWithAPrerequisite`** and the existing pinned IDs stay unchanged.
- **Model tests** (`core`), through `submitJob` and the settle path (`recordRun`), not hand-built records:
  - a job whose relative has run is sized `family` with `FamilyRuns` set, and no advisor call is queued;
  - a waiting job is re-sized at the next pass when a relative settles;
  - learning off: no pool, sizing as today;
  - `knownBeliefs` puts the same family first, then shared steps, then recency.
- **Display:** `Tag` and `Describe` cases for `family`; the `family` line in the belief pager and `loom job beliefs show`.
- **Wire:** `TestProtocolReference` after regenerating.

## 12. Known limits and later work

- The environment is ignored: `GOOS=windows go build ./...` and `go build ./...` are one family, and so are runs with and without `CARGO_INCREMENTAL=0`.
- Go packages named by import path (`go test github.com/…/sched`) aren't recognized as packages, so such commands merge across packages.
- `-count=1` is ignored, so cached and uncached `go test` runs share a family: the largest peak errs high, the median duration mixes.
- Test filters mostly change duration, so a family's durations mix filtered and full runs (71% within 2× in the prototype).
- The 13% below-the-peak rate comes from the prototype's replay; the Go rules differ in detail (`cd` kept, two words for other programs), so re-run the replay against the shipped rules in the week's check.
- Families don't cross repos; the advisor's other-repo candidates still do.
- Later: environment names that split a family (`GOOS`, `GOARCH`, `RUSTFLAGS`), if the week's check shows cross builds under-claiming; borrowing curves for the greedy admission, once family claims prove out.
