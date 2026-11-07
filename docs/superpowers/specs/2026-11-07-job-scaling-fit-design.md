# Job scaling: p from two widths, and a floor under base cores

Status: design approved 2026-11-07.

Amends [job scaling](2026-11-05-job-scaling-design.md) §5.1 (what a belief learns) and §5.2 (base claims). Everything else there stands.

## 1. Problem

The owner asked "is the job scaling working?" on 2026-11-07. Grants run (78 of 1,677 recorded runs had spare cores, 36 of them that day), but loom's `go test ./...` fell into a state it can't leave:

- Its claim was 2 cores. Three runs on a contended host kept 0.48–0.63 cores busy, against about 1.0 before, and the window's median busy cores fell to 0.6, so `Size` gave ⌈1.25 × 0.6⌉ = 1 core.
- At 1 core the hint is `GOFLAGS=-p=1`, so test packages run one at a time: jobs 2261 and 2267 took about 870 s each, against 241–328 s at 2 cores, on a host at load 2.6 of 16.
- At `-p=1` a run never keeps more than a core busy, so the busy rule never gives 2 cores back.
- `ScalingOf` takes p from one run at a time, assuming a 1-core run would keep a core busy. `go test ./...` mostly waits (tmux, sleeps, timeouts), so its 2-core runs, keeping about 1 core busy, read as p = 0, and `Grant` never lends it a core either.

For a command that waits more than it computes, the busy rule is a fixed point at whatever width it ran: claim c gives wall W(c), busy CPU ÷ W(c), and the next claim ⌈1.25 × CPU ÷ W(c)⌉. Both 1 and 2 are fixed points for `go test ./...`; contention moved it from 2 to 1.

Raw wall times across widths can't fix this. They swing with cache state far more than with cores: `cargo clippy -p prader-app` took a median 16 s at 2 cores, 28 s at 3, 387 s at 4 and 26 s at 8; `check-claude-md.sh` 28 s at 1 core and 800 s at 2. Holding the work fixed removes most of that: wall time per CPU-second is 1 ÷ busy, so the speedup between two widths is the ratio of their busy cores.

Success, read from `beliefs.json` after a week: `go test ./...` claims 2 cores or more, and no belief's base cores sit below a width its runs showed at least 1.25× faster.

## 2. Decisions

- **p from comparing busy cores at two widths** (owner's option (a), refined: the work held fixed, since raw wall times are dominated by warm and cold caches). Rejected: raw wall times; leaving contended runs out of the core figures (option (b), which needs the host's CPU pressure recorded per run); a floor with no model (option (c)).
- **Grants and a floor** (owner's choice). The fitted p drives grants, ideal widths, durations and surprises, and puts a floor under base cores. Rejected: grants only, which leaves `go test ./...` at `-p=1` whenever anything waits; splitting the hint's width from the claim's cores, which changes every job's hint path and lets a mis-fitted CPU-bound job overrun its claim.
- **The fit only raises p.** Today's p is a lower bound under the model below, so the larger of the two is taken.
- **The floor needs a 1.25× gain and stays within the ideal width and the default claim.** The 50% efficiency bound alone holds at 2 cores for every p above 0, which would hand `dev-postgres.sh` a second core for a 6% gain.

## 3. Concepts and names

- **A width:** the cores a run started with (`Run.Claim.CPUs`, spare cores included), for runs that teach cores (`teachesCores`: done, with CPU time, not laddered) and are not exclusive.
- **busy(w):** the median busy cores (CPU time ÷ wall time) of the window's runs at width w.
- **The model:** busy(c) = B₁ × c ÷ (c(1 − p) + p), where B₁ is the busy cores at 1 core. A CPU-bound command has B₁ ≈ 1, which is what today's one-run p assumes; one that waits has B₁ < 1, and its true p is above today's figure.
- **A pair's p:** for widths a < b with R = busy(b) ÷ busy(a): 0 when R ≤ 1, otherwise (R − 1) ÷ (R(1 − 1/b) − (1 − 1/a)), held to 1. For R > 1 the denominator is at least 1/a − 1/b > 0.
- **The fitted p (`Scaling.Fit`):** the median of every pair's p, with 2 widths or more (`Scaling.Widths`, ascending).
- **The floor:** the widest width in `Widths` that is at most the default claim's cores, at most the ideal width under the fitted p alone, and at which the fitted p predicts a speed at least `floorGain` (1.25) times that at the busy rule's cores: f(cores) ÷ f(w) ≥ 1.25, with f(c) = (1 − p) + p/c. 0 when no width qualifies.

## 4. Behaviour

### 4.1 `ScalingOf`

Unchanged: the runs it reads (the done runs with CPU time among the last 10, exclusive ones left out), the overrun test, and the one-run p of each run with 2 cores or more (`busy p` below, their median).

New, unless the command overruns: the runs are grouped by width. With 2 widths or more, `Widths` lists them and `Fit` is the fitted p. Then:

- `Known` is true when there is a busy p or a fit.
- `Parallel` is the larger of the busy p and `Fit`, either one alone when only it exists.
- `Runs` is the runs the larger came from: the runs with 2 cores or more for the busy p, every run read for the fit.
- `Ideal` is as before, from `Parallel`, at most twice the most cores a run had.

`ScalingOf` stays a pure function of the belief.

### 4.2 `Size`

The busy rule (⌈1.25 × median busy⌉, held to 1 and the default claim's cores) is unchanged. When it set the cores from the job's own runs (the `haveBusy` case: not explicit, not an overrunner, not a family's, not a prior), the cores become the larger of that and the floor (`Scaling.floor(cores, p.DefaultClaim.CPUs)`). Memory is sized after the cores, as now, so it is learned at the floored cores.

### 4.3 What follows without change

`SpareWidth`/`Grant`, `Usual`/`OverdueAfter`, the forecast and the duration surprise read `Parallel` and `Ideal`, so they use the fitted p as they stand.

### 4.4 On fresco's beliefs (replayed 2026-11-07)

Of 684 beliefs with a p, 89 have 2 widths or more. The floor raises 11 base claims, none down and none past a width already run; the ideal width changes for 22.

| belief | busy by width | p | cores | ideal |
|---|---|---|---|---|
| `go test ./...` | 1c 0.31, 2c 0.63 | 0 → 1.00 | 1 → 2 | 1 → 4 |
| `go test -race ./...` | 3c 1.61, 4c 2.17 | 0.57 → 1.00 | 3 → 4 | 3 → 8 |
| `cargo nextest run -p prader-web …` | 2c 0.57, 4c 0.99 | 0 → 0.91 | 1 → 4 | 1 → 8 |
| `RUSTDOCFLAGS=… cargo doc …` | 1c 0.51, 2c 0.70 | 0 → 0.53 | 1 → 2 | 1 → 3 |
| `cargo clippy -p prader-app` | 2c 0.92, 3c 0.93, 4c 3.37, 8c 1.02 | 0 → 0.28 | 2 → 2 | 1 → 2 |
| `bash scripts/dev-postgres.sh start` | 1c 0.83, 2c 0.88 | 0 → 0.11 | 2 → 2 | 1 → 2 |

## 5. Surfaces

- **The scaling line** (`loom job beliefs show`, the `b` pager): when the fit gave `Parallel`, `scaling   100% parallel from runs at 1 and 2 cores · efficient up to 4 cores · ~…` (the widths joined with `JoinAnd`); otherwise as today.
- **SCALES, `--json`:** SCALES prints `Parallel` as today. `BeliefView.Scaling` carries `Widths` and `Fit` (`widths`, `fit`, both omitted when empty) in `--json`.
- `beliefs.json` and `jobs.json` don't change.

## 6. Components

| unit | change |
|---|---|
| `sched/scaling.go` | the widths, the pair's p, the fit, `Scaling.Widths`/`Fit`, the combined `Parallel`, `Scaling.floor` and `floorGain` |
| `sched/size.go` | the floor in the `haveBusy` case |
| `sched/belief_text.go` | the scaling line's "from runs at …" |
| `sched/README.md`, `docs/claude/the-work-queue.md` if it states the rule | the fit and the floor |

## 7. Testing

- `TestScalingOf`: two widths give the fitted p (`go test ./...`'s numbers give 1); R ≤ 1 gives 0; the fit only raises p (a CPU-bound belief whose fit is below its busy p keeps the busy p); the median over pairs (four widths, one of them a cold outlier); one width gives no fit; exclusive and laddered runs leave their width out; an overrunner has no fit.
- `TestSize`: `go test ./...`'s runs give 2 cores, not 1; the floor is held to the default claim's cores and to the ideal width under the fitted p; a width under the 1.25× gain is no floor; no floor from a family pool, a prior or explicit cores; an overrunner's cores are unchanged.
- `TestFormatBelief_Scaling`: the "from runs at 1 and 2 cores" line.

## 8. Known limits

- **Contention still lowers busy cores at every width,** which can lower the busy rule and the fit alike. Option (b) is not built.
- **The floor lapses with its evidence.** Ten runs at the floor push the narrower runs out of the window; with one width left there is no fit, a contended host can lower the busy rule again, and one slow run brings the fit back. On an idle host, grants put a wider width in the window and keep the fit.
- **A width with one run can be an outlier** (a cold build): runs at the floored width then outnumber it and correct the median.
- **The fit reads widths, not phases:** like the one-run p, it can't tell a command with serial phases from one that is evenly parallel.
