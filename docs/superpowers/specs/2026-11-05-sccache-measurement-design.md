# Measuring sccache'd builds and short-lived processes

Status: design approved 2026-11-05.

Builds on [the smart scheduler](2026-11-05-smart-scheduler-design.md) and [job scaling](2026-11-05-job-scaling-design.md), which learn claims, durations and scaling from what each job measured.

## 1. Problem

A job's measurements cover its own process tree: the shim's rusage of the command (CPU time, `max_rss`) and a sample every 3 s of its process group (memory, live cores). On fresco two things fall outside that, measured on 2026-11-05:

- **sccache.** `RUSTC_WRAPPER=sccache` is set for every shell. sccache hands each cacheable compile (`rustc`, and the `cc` crate's C/C++) to one long-lived server, a daemon under PID 1 that runs the compiler as its own child. Three cargo jobs claimed 12 cores and used 2.3 in their trees while the server used 14.3 cores and held 5.6 GB. So cargo jobs' busy cores, parallel fraction and memory peaks are understated, their memory claims miss their compile memory, and the server, which holds itself to the machine's cores, ignores the claims.
- **The live sampler.** It sums `ps -o time=` (whole seconds, a live process's own time) over the job's process group. A process that starts and exits between samples never counts: the work view showed 0.0c for four cores of 0.4 s processes, the shape of a build or a test suite. A process that changes group (coreutils `timeout` calls `setpgid`) leaves the sum, and the sampled memory peak with it.

The exit-time accounting itself is exact: the shim's CPU figure matched the job's own `times` to 0.01 CPU-s, and four busy loops read 3.98 cores on a quiet host.

Success looks like this:
- a cargo job's recorded CPU and peak include its compiles (its busy cores track its claim, not the client's fraction);
- the work view's live cores for a build read near what `times` reports over the same span;
- a `timeout`-wrapped command's processes count toward its sampled peak.

## 2. Decisions

- **Client-side sccache, as a hint** (brainstorm option "client-side mode hint"). sccache 0.18 can run each compile in its client (`SCCACHE_CLIENT_SIDE=1`, "the recommended mode", expected to become the only one), with the daemon kept as a gateway to cache storage. The queue sets it for its jobs. Measured on fresco: the compile's CPU moved into the caller's tree (1.75 CPU-s where server-side showed 0.06), the existing cache served hits in client-side mode, and a cached compile costs about 0.1 CPU-s more (a fully cached 41-crate build took 1.25 s instead of 0.30 s). Rejected: crediting the shared server's compilers to jobs by the `LOOM_JOB` in their environment (sampled, approximate, new run fields); a server per job inside its tree (the most moving parts, and concurrent servers sharing one disk cache).
- **An exact live sampler** (option "hint + exact live sampler"): on Linux, `/proc` replaces `ps`, CPU includes reaped children's time, and a job's usage sums its session. Rejected: the hint alone (the live figure stays near 0 for builds); per-job cgroups (the largest change, Linux with systemd only).
- **The hint goes on every job**, as the parallelism variables do: sccache may be configured in `.cargo/config.toml`, which the environment doesn't show, and the variable is inert without sccache.
- **The lost-job kill proof keeps process groups.** Signals go to the group, and the safety rules prove the group; only usage moves to the session.

## 3. Scope

In: the `SCCACHE_CLIENT_SIDE` hint; the Linux `/proc` sampler with session IDs and children's CPU; `Snapshot.Session`; usage by session in the model; docs and a rule; a `sccache_live` contract test.

Out (§12): nix-daemon builds, per-job cgroups, crediting a shared daemon's work, processes that call `setsid` inside a job.

## 4. Concepts and names

- **The hint:** `SCCACHE_CLIENT_SIDE=1`, added by `sched.Hints` beside `CARGO_BUILD_JOBS` and the rest when the job's environment doesn't set it.
- **Session:** a job's processes are the shim's session: the daemon starts the shim with `Setsid`, so its pid is the session ID, and every descendant shares it unless it calls `setsid`. `Proc.SID`; `Snapshot.Session(sid)`.
- **A process's CPU:** its own user and system time plus its waited-for children's (`utime + stime + cutime + cstime`). Summed over a session, it is the session's CPU so far, including processes already reaped inside it.

## 5. Behaviour

### 5.1 The hint

`sched.Hints` adds `SCCACHE_CLIENT_SIDE=1` when the job's environment lacks it; any value the submitter set stays, so `SCCACHE_CLIENT_SIDE=0` (or `false`, or empty) opts a job out, and sccache then runs server-side (verified on 0.18.0: only `1` turns it on).

With it, each compile runs in the sccache client, a child of cargo in the job's tree, and the shared daemon only stores and serves entries:
- the shim's rusage and the session's samples include the compiles;
- cargo's jobserver reaches the compiles, so the claim's cores bound them and a job's grant of spare cores is what it uses;
- entries the server-side mode cached are hits.

sccache ignores the variable when the job sets `SCCACHE_LOG`, uses distributed compilation, or predates client-side mode; those jobs measure as before (§12).

### 5.2 The sampler

- **Linux.** `sched.Sample` reads `/proc/<pid>/stat` for every numeric entry of `/proc`: the pid, the process group, the session, the resident pages (as KB), and the CPU as in §4, in clock ticks of 10 ms (`USER_HZ`). A process that exits between the listing and its read is skipped. A scan that lists no process, or whose context ends, is an error, and the pass skips its sample as it does today. Parsing `comm` takes everything up to the last `)`, since a name may hold spaces and parentheses.
- **Elsewhere** (macOS): `ps` as today; `Proc.SID` is the process group, so a session is the group there.
- **A job's usage** (`applyUsage`) sums its session: `Snapshot.Session(rec.PID)` gives the RSS, the CPU and the members. The live cores are the CPU's change over the time between samples, held at 0 or more; the peak, which memory claims learn from, is the session's largest RSS sum; the work view's totals follow.
  - A short-lived process counts: once its parent reaps it, its time is in the parent's children's time, still in the session.
  - `timeout` and job-control shells count: they change group, not session.
  - A process that calls `setsid` leaves, the shared sccache daemon included, on purpose: a shared daemon's work is not one job's.
  - A process orphaned and reaped by PID 1 takes its time out of the sum; the rate's clamp at 0 absorbs the dip.
- **The lost-job kill proof** (`orphanedGroup`) keeps `Snapshot.Group`: a group with members and no process holding the shim's pid.

### 5.3 Beliefs learned before the change

Cargo beliefs hold runs measured without their compiles. Memory, the largest of the last 10 peaks, corrects at the first new run; that run is admitted on the old claim, but its compile memory was in use before too, in the daemon, so only the accounting moves. Cores and the parallel fraction, medians, shift over the next few runs; `loom job beliefs forget <id>` relearns a command at once. A command's first new run may peak above its old claim, which is a surprise and asks the agent for one note, at most once per command in six hours.

## 6. The CLI, the view and the context

Nothing new is shown: the work view's live cores and peaks, `list`'s claims and `beliefs` read the better numbers. USAGE lists the hint with the others and says how to opt out, and that the live cores count short-lived processes.

## 7. Wire

No change: `Proc` and `Snapshot` stay inside the daemon, and `JobView`'s `CPU`, `RSSKB` and `PeakRSSKB` keep their meaning.

## 8. Components

| unit | change |
|---|---|
| `sched/hints.go` | `SCCACHE_CLIENT_SIDE=1` among the hints |
| `sched/sample.go` | `Proc.SID`; `Snapshot.Session`; the `ps` path sets `SID` to the group |
| `sched/sample_linux.go` (new) | `Sample` from `/proc`; `parseProcStat` |
| `sched/sample_other.go` (new) | `Sample` from `ps`, as today |
| `core/queue_watch.go` | `applyUsage` sums the session |
| `sched/README.md`, `sched/CLAUDE.md`, `docs/claude/the-work-queue.md`, `USAGE.md` | the hint, the sampler, the session rule |

## 9. Failure handling

- An unreadable `/proc` entry is skipped; a `/proc` that lists nothing fails the sample, and usage stays at its last values, as a failed `ps` does today.
- A job whose environment sets `SCCACHE_CLIENT_SIDE` keeps its value; one where sccache ignores it measures as before.
- The kill proof is unchanged, so no signal reaches a group the sampler change could misjudge.

## 10. Testing

- **`sched`:**
  - `TestHints`: the hint is added, and a set `0`, `false` or empty value stays.
  - `TestParseProcStat`: a `comm` with spaces and `)`, a zombie, the field positions and units.
  - `TestSnapshot_Session`: a member of the session in another group counts toward the session, not the group.
  - Linux live tests: the sample shows this process with its session and group; a session's CPU includes children it has reaped (a parent that runs short busy children, sampled after they exit); a child under `timeout` counts toward the session.
- **`core`:** `applyUsage` counts a session member outside the job's group; the kill-proof tests stand unchanged.
- **The contract** (`-tags sccache_live`, skipped without `sccache` and `cargo`): with `SCCACHE_CLIENT_SIDE=1`, a compile's CPU is in the caller's tree; without it, it is not.
- `GOOS=windows` and `GOOS=darwin` builds, since the sampler gains a Linux file.

## 11. Build order

One package, one commit: the hint, the sampler, the model, the docs.

## 12. Known limits and later work

- **sccache that won't run client-side**: a job with `SCCACHE_LOG`, distributed compilation, an older sccache, or an opt-out keeps the gap.
- **nix-daemon builds** stay invisible: the daemon runs them, outside every job.
- **A process that calls `setsid`** inside a job leaves its accounting, a daemon it starts included.
- **Cached cargo builds cost more**: about 0.1 CPU-s per cached compile.
- **Later:** per-job cgroups would measure everything a job forks, `setsid` or not, and give exact `memory.peak` and a `cgroup.kill` for cancels.
