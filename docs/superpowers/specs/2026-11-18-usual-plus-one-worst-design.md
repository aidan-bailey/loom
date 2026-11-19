# Usual plus one worst: the greedy fit's memory sum

Status: approved 2026-11-18 (the owner chose "typical + one worst"). Changes how [the greedy scheduler](2026-11-05-cgroup-greedy-scheduler-design.md) (§7.2) and [memory borrowing](2026-11-06-memory-borrowing-design.md) (§5.2, §5.3, §6.5) sum predictions.

## Problem

`PeakSum` added up every job's prediction at its worst: a curve prediction is the envelope (the most) of every profiled run, and a flat one is the claim, which `Size` takes from the largest recent peak. The fit therefore assumed every running job reaches its worst-ever peak at the same moment. On 2026-11-18 at 22:24 the jobs' cgroups used 8.5 GB with 12 GB available, while the fit counted about 54 GB against a 16 GB budget, so every queued job waited on "forecast leaves 0" and the owner force-started 14 jobs that day (waits up to 67 minutes). Across that day's 313 done runs, claims were 1.9× the peaks in GB-hours, and the median run claimed 4.2× its peak: `verify-affected` claims 8–10 GB, peaks at 1.9 GB on a warm checkout and 6.8 GB on a cold one.

The same sum kept two lent borrowers (jobs 8612 and 8642) frozen for 55 minutes: a resume needs every borrower's prediction to fit, and their worst cases together (about 19 GB) passed any budget the host could reach.

## Decisions

- **A span carries the usual level beside the worst.** `Span.ExcessKB` is how far `KB` (the worst) is above what a usual run uses at that time; 0, the zero value, says nothing is known below the worst, so every prediction built as before keeps its meaning.
- **`PeakSum` counts each prediction at its usual level and adds the largest single excess.** At each point it sums `KB − ExcessKB` over the predictions and adds the largest `ExcessKB` among them: one job at a time at its worst is the case history supports, and the ladder and give-backs take the rarer coincidence. A single prediction's sum is still its worst, so `alone`, the wait message's "needs ~N GB" and the logs' predicted peaks are unchanged. Every fit uses it alike: `AdmitGreedy`, the forecast, the firm fit, the borrow fit and `BorrowFits` (resume).
- **A curve prediction's usual level is the median run.** `envelope` takes each curve's spans apart: per stretch, `KB` is the most of any run and the usual level is the median of the runs' levels (the upper one for an even count), a run with no span there counting 0, since a usual run is over by then.
- **A flat prediction's usual level is the belief's usual peak.** `UsualPeakKB` is the median of the full peaks (`Run.FullPeak`) among the belief's newest `window` runs, with sizing's 15% margin, and only with at least `holdRuns` of them. It reads the job's own runs only, never the family pool, which mixes filtered and full runs. A flat claim or hold counts at the lower of it and its level (`UsualAtMost`); a job with no usual peak, a laddered one, or one requeued `MaxRequeues` times keeps excess 0.
- **A running job's usual level is never below what it uses.** `predictRunning` raises every span's usual level to its measured use with the margin (`UsualAtLeast`), capped at the span's worst; above the worst, the prediction is flat at the use, as before.
- Out of scope: CPU gates (claims and pressure, unchanged), `Size` (claims stay the largest peak: the ladder's squeeze and the over-cap skip read them), the fallback `Admit` by claims, and the "no borrower while a job is lent" rule.

## Risks

A workload whose runs split in two (warm and cold builds) has a median at the warm level; two cold builds at once can pass the budget, which the ladder then squeezes, pauses or requeues. Watch `queue.ladder` and `queue.gave_back` after deploying.

## Tests

- `PeakSum`: the usual levels plus the largest excess; a single prediction at its worst; predictions without a usual level as before.
- `UsualAtMost`/`UsualAtLeast`: never above the worst nor below 0.
- `Predict`: three runs at different levels give the most as `KB` and the median as the usual level.
- `UsualPeakKB`: the median of the window's full peaks with the margin; failed, adopted and laddered runs and runs past the window skipped; fewer than three is no answer.
- `AdmitGreedy` and `BorrowFits`: a job the worst-case sum held back fits by the usual-plus-one-worst sum, and the other side of the budget still waits.
- Core: a learned job's flat prediction carries its belief's usual peak, and a running job's measured use raises its usual level.
