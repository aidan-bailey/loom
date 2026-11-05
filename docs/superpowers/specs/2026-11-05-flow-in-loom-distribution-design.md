# Flow in loom, part 1: distribution

Status: design approved section by section 2026-11-05; awaiting review of this file.

The first of three sub-projects that move the agent flow (`~/.claude/flow`, the private repo `aidan-bailey/claude-flow`) into loom. Part 2, coordination, gives flow loom primitives for waits and step chains in place of `agent-wait.sh`. Part 3, orchestration, moves the supervisor's bookkeeping into the daemon. Visibility and control (plan progress in the TUI and on the phone) is deferred.

## 1. Problem

Flow is the supervised plan-execution system: 12 pinned agents, 5 skills (`/flow-init`, `/flow-draft`, `/flow-supervise`, `/flow-retro`, the preloaded `flow-habits`), about 5,000 lines of Python checkers with about 4,500 lines of tests, `agent-wait.sh`, per-repo profiles and run records. It reaches Claude by symlinks:
- `install.sh` links `agents/*.md` into `~/.claude/agents/`, `skills/*/` into `~/.claude/skills/`, and `home.md` to `~/.claude/rules/flow.md`;
- each loom account has its own `CLAUDE_CONFIG_DIR`, so `install.sh` runs again under every account;
- agents and skills call scripts by absolute paths (`python3 "$HOME/.claude/flow/lib/flowconf.py"`, more than 40 references);
- loom knows nothing of flow, so flow fakes what loom has: `agent-wait.sh` wraps each step in `loom job run` (flow d937675), and no flow step can use `--after` (the 2026-11-05 finding: none of the 53 jobs in the store had one, because agent-wait never shows an agent its job ID).

Moving flow into loom puts one codebase behind parts 2 and 3, and lets flow come with loom: no symlinks, no per-account install, the same version as the build that launched the session.

## 2. Decisions

- **The order is distribution, then coordination, then orchestration** (brainstorm option "1 → 2 → 3"). Rejected: coordination first; orchestration first to shape the other two; one combined spec.
- **Flow's source lives in loom's repo.** Agents, skills, scripts and `dispatch.md` ship in loom's binary. Rejected: a pinned snapshot vendored from claude-flow (two repos to change, one to ship); claude-flow kept separate, with loom only pointing sessions at a checkout (other loom users can't get it).
- **A `flow` plugin per launch** carries the agents and skills; the git guard becomes a loom PreToolUse hook keyed on `agent_type`. Rejected: agents through `--agents` JSON (about 50 KB, past tmux's 16 KiB message limit, so it needs a launcher or a file); loom writing install.sh's links into each account's config dir (every Claude session, loom's or not, would see flow).
- **Scripts behind a `loom flow` CLI, Python for now.** Agents call stable `loom flow <tool>` subcommands; loom embeds the Python and runs it with `python3`. Parts 2 and 3 port tools to Go as they need them, without renaming what agents call. Rejected: porting everything to Go now (a rewrite several times larger); Python called by its path inside the plugin (agents have no plugin-root variable, and every later port renames call sites).
- **Profiles in each client repo; records and retros in `~/.loom/flow`.** claude-flow is archived read-only. Rejected: claude-flow as a data-only repo with its backup push; everything under `~/.loom/flow`.
- **Each launch gets its own copy.** Claude writes `tsconfig.json` and `.claude-plugin/types/` into every `--plugin-dir` it loads (the mod's 2026-10-14 probe, `docs/superpowers/plans/2026-10-14-loom-claude-mod.md`), so a root shared by sessions would be rewritten by each. The flow plugin and its scripts are written into the launch's hooks folder beside the mod, which `hooks.Prepare` empties per launch. This replaces the content-hashed shared root and its sweep from the brainstorm's Section A.

## 3. Scope

In: the `flow/` tree in loom; the per-launch copy; the plugin's names; the guard hook; the context fragment; the `flow` setting; `loom flow` and its tools; the Python changes the move needs; `loom flow import`; the cutover; CI; docs.

Out: coordination primitives (part 2); daemon orchestration (part 3); visibility and control (deferred); porting any tool to Go; flow on Windows, where loom prepares no hooks folder (`prepareHooks` returns early) and so passes no plugin.

## 4. Concepts and names

- **The flow tree:** `flow/` at loom's repo root, embedded in the binary by the Go package `flow`.
- **The plugin:** `flow/plugin/`, a Claude plugin named `flow`. Plugin names are namespaced, so the skills are `/flow:init`, `/flow:draft`, `/flow:supervise` and `/flow:retro`, the preloaded skill is `flow:habits`, and the agents are `flow:verifier`, `flow:intermediate-dev` and so on.
- **The launch's flow root:** `<hooks folder>/flow/`, holding `plugin/`, `lib/` and `ROSTER`. Its path reaches the session's processes as `LOOM_FLOW_ROOT`.
- **The roster ID:** `ROSTER`'s content, loom's version and the first 12 hex digits of the SHA-256 of the plugin tree as embedded. It replaces `flow @ <sha> roster <12 hex>` in run records.
- **The flow data dir:** `<global dir>/flow/`, `~/.loom/flow/` by default (beside `state.json`), holding `records/<key>/` and `records/retros/`.
- **The guarded agents:** `flow:junior-dev`, `flow:intermediate-dev` and `flow:senior-dev` (the guard on `Bash`), and `flow:drafter` (on `Bash|Write|Edit`, with `--drafter`).

## 5. Behaviour

### 5.1 The flow tree

```
flow/
  CLAUDE.md            rules for changing flow (the pin rule, §5.9)
  README.md            what flow is, its layout, its tools
  PROVENANCE.md        pin changes from the move on, each with its date and the owner's instruction
  embed.go             package flow: the embed, Write, the roster ID
  plugin/
    .claude-plugin/plugin.json   name "flow"
    agents/*.md        the 12 agents
    skills/{init,draft,supervise,retro,habits}/SKILL.md
    docs/dispatch.md, docs/records.md
  lib/                 the Python tools, their tests, agent-wait.sh, prices.toml, banned-tokens.txt, test.sh
```

The move changes only what it must in the prompts:
- `python3 "$HOME/.claude/flow/lib/X.py"` becomes `loom flow X` (§5.5's names);
- agent names in dispatch text (`subagent_type`) and the `skills:` preload take their namespaced form;
- `hooks:` blocks leave the four guarded agents (§5.3 carries the guard);
- paths to flow's docs become `loom flow path docs/<file>` for agents and `${CLAUDE_SKILL_DIR}/../../docs/<file>` for skills;
- `/flow-init`, `/flow-draft`, `/flow-supervise`, `/flow-retro` become `/flow:init`, `/flow:draft`, `/flow:supervise`, `/flow:retro` wherever a prompt or doc names them.

Not moved: `install.sh`, claude-flow's commit hooks, `lib/install_test.py` and `lib/post_commit_test.py` (they test what is not moved), `repos/`, `records/`, `docs/evidence.md`, the old `docs/PROVENANCE.md` and flow's own specs and plans, which name client repos or describe claude-flow's history. The embed leaves out `lib/*_test.py` and `__pycache__`.

### 5.2 Each launch's copy

When the `flow` setting is on (default on) and loom prepares a hooks folder for a Claude launch, `prepareFlow` (beside `prepareMod`, in `session/`) does three things:
- it writes the embedded plugin and scripts into `<hooks folder>/flow/`, and the roster ID into `<hooks folder>/flow/ROSTER`;
- it adds `--plugin-dir <hooks folder>/flow/plugin` beside the mod's;
- it adds `LOOM_FLOW_ROOT=<hooks folder>/flow` to the launch's environment.

Workspace terminals get the same as worktree sessions. A launch keeps the copy it was given until its next launch, so a relaunch or resume takes the current build's. With the hooks folder emptied by `hooks.Prepare` on every launch, nothing is left to prune.

### 5.3 The git guard

When flow is on, `hooks.SettingsJSON` adds a PreToolUse hook with the matcher `Bash|Write|Edit` running `loom flow guard`. The guard's Go front end:
- reads the hook input on stdin and exits 0 at once unless `agent_type` names a guarded agent and the tool is one it guards (§4);
- otherwise runs `git_guard.py` (with `--drafter` for the drafter) from `LOOM_FLOW_ROOT`, passing the hook input and returning its exit code and output unchanged.

A guarded agent's call fails closed: if `LOOM_FLOW_ROOT` is unset or missing, or `python3` can't run the guard, the guard exits 2 with the reason on stderr, which blocks the call and shows the agent why. A call from the main thread or from any other agent passes untouched, whatever the guard's state.

### 5.4 Loom's context

`home.md`'s rule becomes `session/loom_context_flow.md`, a fragment joined into the main context and the subagent brief when flow is on. It keeps the rule's runtime condition, `loom flow conf status` naming a profile, so loom doesn't check for profiles at launch. It tells agents that flow's docs are read through `loom flow path docs/<file>`, since the context files are per workspace and a launch's root is per session.

### 5.5 `loom flow`

`loom flow <tool> [args…]` runs `python3 -I <root>/lib/<script>`, with stdin, stdout, stderr and the exit code passed through. `<root>` is `LOOM_FLOW_ROOT` when it is set. In a shell outside a session, `<root>` is the binary's own copy, written to a temp dir for the call and removed after. When `LOOM_FLOW_ROOT` is set but not a flow root, the command refuses and exits 125 rather than run another build's copy. It sets `FLOW_ROOT`, `FLOW_DATA` (`~/.loom/flow`) and `FLOW_ROSTER` (the root's `ROSTER`) for the script.

| Tool | Runs |
|---|---|
| `conf` | `flowconf.py` (key, status, show, state, drafts, vacuous, roster, gates, doctor) |
| `check-plan` | `check_plan.py` |
| `packet` | `plan_packet.py` |
| `join` | `plan_join.py` |
| `premises` | `plan_premises.py` |
| `conformance` | `plan_conformance.py` |
| `costs` | `agent_costs.py` |
| `doctor` | `doctor.py` |
| `packages` | `packages.py` |
| `check-agents` | `check-agents.sh`, through bash |
| `wait` | `agent-wait.sh`, through bash |
| `guard` | §5.3's Go front end |
| `path <file>` | Go: prints the absolute path of `<root>/plugin/<file>`; exits 1 when it doesn't exist |
| `import <checkout>` | Go: §5.7 |

Without `python3` on PATH, a Python tool refuses with a message naming it and exits 125.

### 5.6 The Python changes the move needs

- `flowconf.py`: `find_profile` reads only `<worktree>/.claude/flow.toml`; the home-store lookup (`repos/<key>.toml`) and `flow_home()` go. Its message for a missing profile names `/flow:init`.
- `flowconf.py roster` prints `FLOW_ROSTER` and no longer runs `git ls-tree` on a flow repo.
- The records path is `FLOW_DATA/records/<key>/`; a profile's `paths.records` copy into the repo stays as it is.
- The key (the main checkout's path with every character outside `[A-Za-z0-9]` made `-`) and the state dir (`<git-dir>/flow`, one per worktree) are unchanged.
- `check-agents.sh` reads the agents from `plugin/agents/` and checks the namespaced names and preloads.
- Tests that build a fake `FLOW_HOME` build a fake `FLOW_ROOT` and `FLOW_DATA` instead.

### 5.7 Profiles, records and the import

- Each client repo commits its own `.claude/flow.toml`. A worktree cut before the profile's commit has none, and `loom flow conf status` says so.
- `loom flow import <claude-flow checkout>` copies `records/` into `~/.loom/flow/records/`. A file already there with the same bytes is skipped; one with other bytes is refused and listed, and the import exits 1 after copying the rest. It then prints, for each `repos/<key>.toml`, the `.claude/flow.toml` path it belongs at (the key's checkout, when it exists). It commits nothing in any repo: each profile's commit is the owner's or that repo's session's.
- Records and retros in `~/.loom/flow` have no git history and no off-machine backup; the owner chose that over a data repo.

### 5.8 Cutover

The namespaced names don't collide with the home copy's (`flow:verifier` against `verifier`), so both can be loaded at once:
1. A loom build with flow ships. Sessions launched before it keep the home copy until they relaunch.
2. When no supervised run uses the home copy, the owner runs `install.sh --uninstall`, then again with `CLAUDE_CONFIG_DIR` set to each loom account's config dir. That removes the agent and skill links and `rules/flow.md`, which otherwise sits beside loom's fragment with the old paths.
3. The owner runs `loom flow import`, commits each repo's profile, and archives claude-flow.

### 5.9 Retros and pins after the move

`/flow:retro` reads `~/.loom/flow/records/` across every repo. Its changes to the roster land in loom's `flow/plugin/`, so a retro runs in a loom session on loom's repo and commits there. `flow/CLAUDE.md` carries claude-flow's pin rule: an agent's model, effort, maxTurns, tools and cache lifetime change only on the owner's instruction, recorded with its date in `flow/PROVENANCE.md`.

## 6. Components

- `flow/` (new): §5.1's tree; `embed.go` exports `Write(dir string) error` and `RosterID() string`.
- `session/claude_flow.go` (new): `prepareFlow`, called from `prepareHooks` after `prepareMod`; adds the plugin dir and `LOOM_FLOW_ROOT`.
- `session/hooks/hooks.go`: `SettingsJSON` and `Prepare` take whether flow is on and add the guard hook.
- `session/loom_context_flow.md` (new) and `session/loom_context.go`: the fragment, joined when flow is on.
- `config/`: `Settings.Flow`, a `*bool` whose getter returns true when it is nil.
- `internal/cli/flow.go` (new): `loom flow` and its tools.
- `.github/workflows/flow.yml` (new): `flow/lib/test.sh` and `check-agents.sh` on changes under `flow/**`. `.github/workflows/build.yml`'s path filter adds `flow/**`, since the binary embeds it.
- Docs: `docs/ARCHITECTURE.md` (the flow tree, the per-launch copy, the data dir), `USAGE.md` (`loom flow`, the setting), `flow/README.md`, and the `CLAUDE.md` rules of the packages touched.

## 7. Probes

They gate the build. An opt-in test under `LOOM_TEST_REAL_CLAUDE=1` checks, against the Claude Code on PATH:
1. what `agent_type` a hook sees inside a plugin agent (`flow:junior-dev` or `junior-dev`);
2. that `skills: [flow:habits]` in a plugin agent preloads the plugin's skill;
3. that a session-level PreToolUse hook (`--settings`) fires for a plugin subagent's tool calls, and that its exit 2 blocks the call;
4. that two `--plugin-dir` flags (the mod's and flow's) both load, and that `agent.ApplyClaudePluginDir` adds a second without replacing the first.

When an answer breaks §5.2 or §5.3, the design goes back to the owner before code.

## 8. Failure handling

- The copy can't be written: the launch goes ahead without flow (no plugin dir, no guard hook, no fragment, no `LOOM_FLOW_ROOT`), logged as `flow.prepare_failed`. It never starts with part of a copy.
- The guard can't run for a guarded agent: the call is blocked with the reason (§5.3).
- `LOOM_FLOW_ROOT` set but not a flow root, or no `python3`: `loom flow` refuses and exits 125 (§5.5).
- `import` meets a record that differs: it refuses that file, lists it and exits 1 (§5.7).
- The setting off: no plugin, guard hook, fragment or environment variable.

## 9. Testing

- Go:
  - `Write` is idempotent, writes every embedded file, leaves out the tests, and `RosterID` changes when a plugin file does;
  - the launch adds the flow plugin dir and `LOOM_FLOW_ROOT` only when the setting is on, keeps the mod's plugin dir, and is built through `prepareHooks` as production runs it;
  - `SettingsJSON` carries the guard hook only when flow is on;
  - the guard's front end, both sides: each guarded agent on each tool it guards runs the guard; an unguarded agent, a guarded agent on another tool, and the main thread pass with exit 0; a guarded agent with no root exits 2;
  - `loom flow` passes exit codes and stdio through, refuses a bad `LOOM_FLOW_ROOT` and a missing `python3` with 125, and `path` exits 1 for a missing file;
  - the fragment is in the main context and the brief when flow is on, and absent when it is off;
  - `import` copies new files, skips identical ones, refuses and lists differing ones.
- Python: `flow/lib/test.sh` and `check-agents.sh` in CI (`flow.yml`); the Nix build keeps building from `./.` and runs no Python.
- The opt-in probes of §7.

## 10. Known limits and later work

- No backup of records and retros (§5.7).
- Flow is loom-only: a plain `claude` outside loom gets no flow once the home links are removed.
- A session launched by one build and a `loom` binary on PATH from another: `loom flow` runs the launch's scripts (`LOOM_FLOW_ROOT`), so prompts and scripts match; only the Go tools (`guard`, `path`, `import`) come from the newer binary.
- Part 2: `loom flow wait` on loom's queue with job IDs, chains and waits that take no slot; part 3: the supervisor's bookkeeping in the daemon; later, plan progress in the TUI and on the phone.
