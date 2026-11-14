# Flow: the run engine pre-loads each agent's package rules

Status: design approved by the owner 2026-11-14.

Claude Code loads a package's `CLAUDE.md` only when a tool touches a path under it. The 2026-11-13 trace found that drafters, which read through Bash, mostly never loaded theirs. The 2026-11-14 check looked at 1,043 headless agent runs of `/flow:run`'s engine, from 2026-11-06 to 2026-11-13, in two repos with package `CLAUDE.md` files. For each run, it took the files the run changed or reviewed, as its own `git diff`, `git diff --stat` and `git status` output showed them. It listed the package `CLAUDE.md` files covering those files, then checked whether each one ever loaded:

| Role | Runs with a package `CLAUDE.md` in scope | Loaded all | Loaded none | Covering files missed |
|---|---|---|---|---|
| intermediate-dev | 288 | 34% | 42% | 375 of 600 |
| story-reviewer | 120 | 28% | 54% | 147 of 222 |
| code-quality-reviewer | 169 | 13% | 48% | 379 of 517 |
| doc-writer | 75 | 33% | 31% | 109 of 192 |
| junior-dev | 12 | 8 runs | 2 runs | 9 of 19 |

Why the files go unloaded:

- **Bash reads do load rules in headless runs.** A plain `sed -n` or `cat` of one file loaded its package's rules 112 times in 124. That is why these roles do better than the drafters, whose subagents' Bash calls rarely triggered a load.
- **Developers write through Bash.** They write the plan's code through Python run in Bash: only 24 of 526 developer runs used Edit or Write.
- **Reviewers see code mostly through other commands.** They read it through `git diff`, `git show` and `grep`.
- **Every brief already says it.** "Its CLAUDE.md files are your rules" doesn't move the rate.
- **Few findings cite a package rule.** Only 20 of the 804 distinct review findings cite a `CLAUDE.md`, and most of those concern a stale `CLAUDE.md`, not a rule the code broke.

The defects these misses cause are not measured yet. The 2026-11-13 trace counted some: a kernel error variant against the kernel's `CLAUDE.md`, and contract types that clash with generated exports.

## 1. Decisions (owner, 2026-11-14)

- **Scope: the run engine's headless agents only.** That means the developers, the story, code-quality and whole-implementation reviews, and the doc writer, where the gap was measured and where loom builds the command line. `/flow:draft`'s subagents keep the drafters' 2026-11-13 Read rule, and the next retro measures it.
- **Pre-load natively, not by rule text and not by per-role copies.** Probed on Claude Code 2.1.323 (2026-11-14) with `flow:glance` headless in a temp repo:
  - `--add-dir <pkg>` with `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1` loads `<pkg>/CLAUDE.md` at startup. The agent answered its codeword without a tool, and answered `NONE` without the variable.
  - A later Read under `<pkg>` loads nothing again. Without `--add-dir`, the same Read loads it.
  - `--append-system-prompt-file` also reaches an `--agent` run, but its text isn't tracked as loaded, so a later read would load the same rules twice.

  Naming the paths in the brief rests on compliance, which the briefs' existing line shows is weak. Per-role slim copies would be a second copy per role beside each `CLAUDE.md`, in repos flow doesn't own. They would drift, developers and reviewers need the same rules, and a rule left out is the defect class this fixes.
- **No size cap.** An agent gets every rule that governs what it touches. A capped list brings the measured gap back. The record shows the bytes so the cost is visible. In one client repo, a story touching its two largest packages pre-loads about 110 KB, roughly 28k tokens, cached for five minutes.

## 2. Behaviour

### 2.1 Which files each agent's rules cover

| Agent | Its files |
|---|---|
| Developer, first build | the story's **Files** line |
| Fix developer (gates or findings) | the Files line, plus the files the story's commits changed so far (`base..head`) |
| Story reviewer, code-quality reviewer, re-review | the files `base..head` changed, plus the Files line |
| Doc writer | the files `base..head` changed |
| Whole-implementation review, and its fix developers | the files the run changed, `State.Base..head` |
| premise-check | none: it reads only the lines it was flagged |

A file's rules directories are every directory from the file's own up to, but not including, the repository root that holds a `CLAUDE.md` in the worktree the agent runs in: the session's worktree, or a laned story's lane. The root's `CLAUDE.md` loads on its own. A new file in a new directory still gets the rules of the packages above it. Markdown and `docs/` files count: a package's docs follow its rules. A path that leaves the tree (`..`, absolute) adds nothing.

### 2.2 Wiring

- **`check_plan.py --json`.** Each story gains `"files"`, the sorted, normalised paths of its Files line, which check-plan already parses for lanes. `flowrun.Story` gains `Files []string` (`json:"files,omitempty"`). A run started by an older build has none, so its first-build developer gets no package rules, as today.
- **The engine stays pure.** `Effects.Agent` gains a `Scope` argument: the files, and a base and head (both empty for a first build). The engine fills it from §2.1, from the state and the heads it has memoized, so a replay builds the same scope.
- **`runFX.Agent` resolves the scope on a fresh dispatch only.** It runs `git diff --name-only <base>..<head>` in the agent's worktree through `internalexec.GitCommand`, adds the files, and maps them to rules directories (§2.1). It then writes the sorted directories and their `CLAUDE.md` files' total bytes into the agent's record: new fields `Rules []string` and `RulesBytes int` on `flowrun.AgentRecord`. Last, it passes the directories to `agentArgv`.
- **The job's command.** `agentCommand` adds `--add-dir <dir>` for each rules directory. When there is one or more, `agentEnv` sets `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1`.
- **Resume and restart.** `loom flow agent answer` rebuilds the command from the record, so a resumed agent gets the same rules. An agent followed after a restart isn't dispatched again, so its rules are fixed once per dispatch.
- **Fail closed.** A diff `runFX.Agent` can't read (an unknown commit, git failing) fails the dispatch as a refused submit: a failed step with the reason, for the manager. It never quietly runs the agent without its rules.

### 2.3 The record

The run's record (`flowrun.Record`, `flow/plugin/docs/records.md`) gains a line after `- Engine:`:

`- Package rules: <n> of <m> agent runs pre-loaded rules, <avg> KB a run, most <max> KB (<agent run>, <role>)`

Here `<avg>` is over the `<n>` runs that pre-loaded rules. With none, the line reads `- Package rules: none pre-loaded`.

## 3. Components

| File | Change |
|---|---|
| `flow/lib/check_plan.py`, `flow/lib/check_plan_test.py` | `story_json` carries `files` |
| `internal/flowrun/state.go` | `Story.Files` |
| `internal/flowrun/engine.go` | `Effects.Agent`'s `Scope`; `engine.agent` and its five callers fill it per §2.1 |
| `internal/flowrun/records.go` | `AgentRecord.Rules`, `AgentRecord.RulesBytes` |
| `internal/flowrun/record.go` | the record line |
| `internal/cli/flow_run_fx.go` | the scope resolved, recorded and passed on |
| `internal/cli/flow_agent_exec.go`, `internal/cli/flow_agent.go` | `--add-dir` per directory, the variable, resume from the record |
| `internal/flowrun/CLAUDE.md` | a rule: every dispatch carries its scope per §2.1 |
| `internal/cli/CLAUDE.md` | the pins rule gains the pre-load and its failing closed. The file is 34 bytes under its budget, so the room comes from moving the two flow-agent rules' evidence into a new guide. |
| `docs/claude/flow-agent-jobs.md` (new), `docs/claude/INDEX.md` | what `--agent` leaves out of a flow agent job, what the job adds back, and each probe |
| `internal/flowrun/README.md`, `flow/plugin/docs/records.md` | `Scope`; the record line |
| `flow/PROVENANCE.md` | one dated entry under **All agents** naming the six engine agents; no pin changes |

## 4. Testing

- **`check_plan_test.py`:** a story's `--json` carries its normalised, sorted `files`.
- **`flowrun`:** each role's dispatch carries the scope §2.1 gives it, through a fake `Effects` that records scopes. The restart-at-every-save test still submits nothing twice.
- **`internal/cli`:**
  - The directory walk keeps each directory holding a `CLAUDE.md`. It skips the root, finds the parent's rules for a new file in a new directory, and adds nothing for `../x` or an absolute path.
  - `agentCommand` adds one `--add-dir` per directory, and `agentEnv` sets the variable only with rules.
  - `runFX.Agent` records `Rules` and `RulesBytes`, and turns an unreadable diff into a failed step.
  - `answer` passes the record's rules.
  - The record line has its own test.
- **A real-Claude contract test,** opt-in like `TestRealClaude_FlowAgentRun`: a headless `--agent` run with a package directory pre-loaded answers that `CLAUDE.md`'s codeword without a tool, and a later Read under it brings no second load. A Claude Code release that drops the variable fails it.
- **Suites:** `bash flow/lib/test.sh`, then `CGO_ENABLED=0 go test ./internal/flowrun/ ./internal/cli/ ./flow/ ./tools/claudemd/`.

## 5. Measure

The provenance entries are wrong if the next retro finds either of these:

- **Coverage:** re-running the transcript scan finds fewer than 95% of engine agent runs with every covering package `CLAUDE.md` loaded. The baseline is 13–34% loaded all.
- **Cost:** `loom flow costs records` shows engine runs' cost per story up more than 20% over the runs before this change.

The retro's trace also counts the defects that break a package rule, which are not measured yet.

## 6. Not covered

- `/flow:draft`'s subagents (the owner's scope).
- A `loom flow agent run` dispatched by hand outside the engine.
- Trimming oversized package `CLAUDE.md` files in client repos: theirs to do.
