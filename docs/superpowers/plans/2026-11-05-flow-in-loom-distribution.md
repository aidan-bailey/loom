# Flow in loom, part 1: distribution — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan package by package (the owner chose inline execution, 2026-11-05). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** loom ships the agent flow: each Claude launch gets a `flow` plugin (agents, skills, dispatch guide) and the scripts behind `loom flow`, with loom's PreToolUse hook as the git guard, replacing `~/.claude/flow`'s symlinks.

**Architecture:** flow's tree moves into `flow/` (a Go package that embeds `plugin/` and `lib/`). `prepareHooks` writes a copy into each launch's hooks folder beside the mod, adds `--plugin-dir <hooks>/flow/plugin`, `LOOM_FLOW_ROOT` and a guard hook running `loom flow guard`. `loom flow <tool>` runs the embedded Python with `python3 -I`. Profiles move to each repo's `.claude/flow.toml`; records to `~/.loom/flow/records`.

**Tech stack:** Go (cobra, embed, testify), Python 3.11+ (flow's tools and their unittest suite), bash.

**Spec:** [`docs/superpowers/specs/2026-11-05-flow-in-loom-distribution-design.md`](../specs/2026-11-05-flow-in-loom-distribution-design.md) (a3456507).

---

## Planning amendments to the spec

Found while planning; P2 writes them into the spec's text.

1. **The roster ID is the hash alone:** `loom flow roster <12 hex>`. Loom's version lives in `package main` (`main.go`), which `flow` and `session` can't import, and the hash already names the content.
2. **One list of guarded agents, `flow/lib/guarded-agents.txt`** (`<agent> developer|drafter`). The Go guard reads it from the embed, and `check-agents.sh` checks it. The lint's old rule, "a developer's frontmatter runs `git_guard.HOOK_COMMAND`", is inverted: a plugin agent's `hooks:` is ignored, so `check-agents.sh` fails an agent that sets one, and fails an agent that edits files but is missing from the list. `git_guard.HOOK_COMMAND` goes away.
3. **`check_plan.py` accepts `flow:` on a story's Developer** (`flow:intermediate-dev` reads as `intermediate-dev`). Plans keep bare role names, and the prompts dispatch `flow:<name>`.
4. **The banned-words list splits.** `flow/lib/banned-tokens.txt` keeps the generic words. `check-agents.sh` also reads `$FLOW_DATA/banned-tokens.txt` (default `~/.loom/flow/banned-tokens.txt`) when it exists, and the client names (`prader`, `polymarket`, `polyoxide`) go there (owner, 2026-11-05). `git_guard_test.py`'s `prader-core` example becomes `example-core`.
5. **`doctor`:**
   - The `links` check (`install.sh --check`) becomes `flow root` (`FLOW_ROOT` holds `plugin/`, `lib/`, `ROSTER`).
   - The `shadowed` warning goes: a repo's `.claude/agents/verifier.md` can't shadow `flow:verifier`.
   - The `guard` check runs `git_guard.py` directly.
6. **The tools:** `conf`, `check-plan`, `packet`, `join`, `premises`, `conformance`, `costs`, `doctor` (= `flowconf.py doctor`; `doctor.py` has no `__main__`), `check-agents` and `wait` run scripts; `guard`, `path` and `import` are Go. `packages.py` has no command line and is reached through `conf gates`, so it gets no tool.
7. **`loom flow path` works only inside a session.** Outside one, the root is a temp copy removed when the call ends, so a printed path would point at nothing. Without `LOOM_FLOW_ROOT` it exits 1 and says so.
8. **`flow.Write` refuses a dir that exists** (in place of "idempotent"). It writes a sibling `<dir>.partial` and renames it, so a failed write leaves no part of a copy. A launch's hooks folder is emptied first anyway.
9. **The guard matches agent types as P1's probe reports them** (`flow:junior-dev` or `junior-dev`; P4 step 1 records which). With the namespaced form it matches only `flow:`-prefixed types, so a home-installed `junior-dev` during the cutover is left to its own hook.

## File map

| Path | Role |
|---|---|
| `flow/embed.go`, `flow/embed_test.go` | the embed, `Write`, `RosterID`, `Guarded`, `IsRoot`, `Mode`, `GuardedMode` |
| `flow/plugin/**` | the plugin: `.claude-plugin/plugin.json`, 12 agents, 5 skills, `docs/dispatch.md`, `docs/records.md` |
| `flow/lib/**` | flow's Python and shell tools, their tests, `guarded-agents.txt`, `banned-tokens.txt`, `prices.toml`, `test.sh` |
| `flow/CLAUDE.md`, `flow/README.md`, `flow/PROVENANCE.md` | rules, facts, pin history from the move on |
| `internal/cli/flow.go`, `internal/cli/flow_test.go` | `loom flow` and its tools; guard; path; import |
| `session/claude_flow.go`, `session/claude_flow_test.go` | `flowOn`, `prepareFlow`, `flowGuardCommand`, `flowEnv` |
| `session/claude_flow_realclaude_test.go` | the §7 probe (opt-in) |
| `session/hooks/hooks.go`, `session/hooks/hooks_test.go` | `PrepareGuarded`, the guard entry in `SettingsJSON` |
| `session/loom_context_flow.md`, `session/loom_context.go` | the fragment and its gate |
| `session/subagent_hooks.go`, `session/instance.go` | the launch wiring, `flowEnvVars` |
| `config/settings.go` | `Settings.Flow`, `FlowEnabled`, `Clone` |
| `core/load.go`, `core/settings.go` | `SetFlowEnabled` beside the other flags; the context files' flow variant |
| `main.go` | `rootCmd.AddCommand(cli.FlowCmd)` |
| `.github/workflows/flow.yml`, `.github/workflows/build.yml`, `.gitignore` | CI for flow's suite; build on `flow/**`; `__pycache__` |
| `docs/ARCHITECTURE.md`, `USAGE.md`, `session/CLAUDE.md`, `session/hooks/CLAUDE.md`, `internal/cli/CLAUDE.md` | docs and rules |

The source is claude-flow at **d937675** (`~/.claude/flow`). P2 checks that HEAD before copying.

Repo rules that bind every package:
- Scratch files go in the scratchpad (`$SP` below), never the worktree.
- Commit with `git commit -- <paths>`.
- Never run loom itself in a pane.
- Heavy suites go through `loom job`.
- `CGO_ENABLED=0` for `go test` and `go vet`.

---

## P1 — The probe (gate)

**Files:** create `session/claude_flow_realclaude_test.go`.

- [ ] **Step 1: Write the probe.**

```go
package session

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/aidan-bailey/loom/session/subagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The probe's two plugins: flowprobe carries an agent that preloads the
// plugin's own skill and runs two Bash calls, the second of which the
// session's PreToolUse hook blocks; flowprobetwo carries a second agent, so
// a run that starts both shows that two --plugin-dir flags both load.
var probePlugins = map[string]map[string]string{
	"flowprobe": {
		".claude-plugin/plugin.json": `{"name": "flowprobe", "version": "0.0.1", "description": "loom flow plugin probe"}` + "\n",
		"agents/probe-dev.md": "---\nname: probe-dev\ndescription: Probe agent for loom's flow plugin test. Use only when told to.\nmodel: haiku\ntools: Bash\nskills:\n  - flowprobe:probe-habits\n---\n\n" +
			"Run these two Bash commands, one call each, in this order: `echo PROBE-ALLOWED`, then `echo PROBE-BLOCKED`. " +
			"Then reply with the codeword your preloaded skill gives you, and what each command returned.\n",
		"skills/probe-habits/SKILL.md": "---\nname: probe-habits\ndescription: Probe skill for loom's flow plugin test.\nuser-invocable: false\n---\n\nThe codeword is QUINCE-4.\n",
	},
	"flowprobetwo": {
		".claude-plugin/plugin.json": `{"name": "flowprobetwo", "version": "0.0.1", "description": "loom flow plugin probe, second plugin"}` + "\n",
		"agents/probe-two.md":        "---\nname: probe-two\ndescription: Second probe agent for loom's flow plugin test. Use only when told to.\nmodel: haiku\ntools: Read\n---\n\nDo not use any tools. Reply with the word DONE.\n",
	},
}

// TestRealClaude_FlowPluginContract checks the four premises the flow
// plugin rests on (flow in loom spec §7) against the claude on PATH, as
// the default account: what agent_type a hook sees inside a plugin agent;
// that a plugin agent preloads its plugin's skill by its namespaced name;
// that a session's PreToolUse hook fires for a plugin agent's calls and
// its exit 2 blocks one; and that two --plugin-dir flags both load. It
// logs the agent type P4 matches on. It spends a few cents of sonnet and
// haiku usage, so it only runs with LOOM_TEST_REAL_CLAUDE=1, never in CI.
func TestRealClaude_FlowPluginContract(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"claude", "sh"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	hooksDir := filepath.Join(root, "hooks")
	_, err := hooks.Prepare(hooksDir, "", "")
	require.NoError(t, err)

	// A PreToolUse hook on Bash that keeps each payload and blocks the call
	// holding PROBE-BLOCKED.
	pre := filepath.Join(root, "pretool")
	require.NoError(t, os.MkdirAll(pre, 0o700))
	guard := `f='` + pre + `/'"$(date +%s%N)-$$"'.json'; cat > "$f"; ` +
		`if grep -q PROBE-BLOCKED "$f"; then echo 'blocked by the probe hook' >&2; exit 2; fi; exit 0`
	var doc map[string]any
	b, err := os.ReadFile(hooks.SettingsPath(hooksDir))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &doc))
	doc["hooks"].(map[string]any)["PreToolUse"] = []any{map[string]any{
		"matcher": "Bash",
		"hooks":   []any{map[string]any{"type": "command", "command": guard}},
	}}
	b, err = json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(hooks.SettingsPath(hooksDir), b, 0o600))

	var pluginDirs []string
	for name, files := range probePlugins {
		dir := filepath.Join(root, name)
		for rel, content := range files {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
			require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
		}
		pluginDirs = append(pluginDirs, "--plugin-dir", dir)
	}
	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))

	prompt := `We are testing Claude Code plugins. In ONE message, launch two subagents: one with subagent_type "flowprobe:probe-dev" and one with subagent_type "flowprobetwo:probe-two". Give both this task: "Follow your instructions." Wait for both, then reply DONE.`
	args := append([]string{"-p", "--model", "sonnet", "--settings", hooks.SettingsPath(hooksDir)}, pluginDirs...)
	args = append(args, "--allowedTools=Agent,Bash", prompt)
	cmd := exec.Command("claude", args...)
	cmd.Dir = work
	for _, kv := range os.Environ() { // run as the default account (TestMain empties CLAUDE_CONFIG_DIR)
		if !strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	starts := map[string]hooks.Event{}
	entries, err := os.ReadDir(hooks.EventsDir(hooksDir))
	require.NoError(t, err)
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(hooks.EventsDir(hooksDir), e.Name()))
		require.NoError(t, err)
		if ev, err := hooks.ParseEvent(data); err == nil && ev.Name == hooks.EventSubagentStart {
			starts[ev.AgentType] = ev
		}
	}
	typeEnding := func(suffix string) string {
		for typ := range starts {
			if strings.HasSuffix(typ, suffix) {
				return typ
			}
		}
		return ""
	}
	devType, twoType := typeEnding("probe-dev"), typeEnding("probe-two")
	t.Logf("SubagentStart agent types: %v", func() (ts []string) {
		for typ := range starts {
			ts = append(ts, typ)
		}
		return
	}())
	require.NotEmpty(t, devType, "the probe-dev agent never started:\n%s", out)
	require.NotEmpty(t, twoType, "premise 4: the second --plugin-dir's agent never started:\n%s", out)

	// Premise 1 and 3: PreToolUse fired inside the plugin agent, with its agent_type.
	var seen []string
	payloads, err := os.ReadDir(pre)
	require.NoError(t, err)
	for _, p := range payloads {
		data, err := os.ReadFile(filepath.Join(pre, p.Name()))
		require.NoError(t, err)
		var in struct {
			AgentType string `json:"agent_type"`
		}
		require.NoError(t, json.Unmarshal(data, &in))
		seen = append(seen, in.AgentType)
	}
	t.Logf("PROBE RESULT: PreToolUse agent_type inside the plugin agent: %v", seen)
	require.NotEmpty(t, seen, "premise 3: no PreToolUse payload from the plugin agent")
	assert.Contains(t, seen, devType, "premise 1: the hook's agent_type matches SubagentStart's")

	ev := starts[devType]
	meta := subagent.MetaPath(ev.TranscriptPath, ev.AgentID)
	require.NotEmpty(t, meta)
	transcript, err := os.ReadFile(strings.TrimSuffix(meta, ".meta.json") + ".jsonl")
	require.NoError(t, err)
	assert.Contains(t, string(transcript), "QUINCE-4", "premise 2: the plugin agent preloaded its plugin's skill")
	assert.Contains(t, string(transcript), "blocked by the probe hook", "premise 3: exit 2 blocked the call")
	assert.Contains(t, string(transcript), "PROBE-ALLOWED", "premise 3: the other call ran")
}
```

- [ ] **Step 2: Compile it without running it.** Run `CGO_ENABLED=0 go vet ./session/`. Expected: no output.
- [ ] **Step 3: Run the probe.** Run `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session/ -run TestRealClaude_FlowPluginContract -v -count=1`. Expected: PASS, with a `PROBE RESULT:` line naming the agent type.
  - If any assertion fails, stop and take the result to the owner. The spec's §7 gate says the design comes back before any code.
- [ ] **Step 4: Record the result** under "Execution notes" at the end of this plan: the agent type, and whether it is namespaced. Then commit.

```bash
git add -- session/claude_flow_realclaude_test.go docs/superpowers/plans/2026-11-05-flow-in-loom-distribution.md
git commit -m "test(session): probe the flow plugin's premises against real Claude" -- session/claude_flow_realclaude_test.go docs/superpowers/plans/2026-11-05-flow-in-loom-distribution.md
```

---

## P2 — The flow tree in loom

**Files:**
- Create `flow/plugin/**` and `flow/lib/**` (copies).
- Create `flow/lib/guarded-agents.txt`, `.github/workflows/flow.yml`.
- Modify `.gitignore`, and the spec (amendments).

- [ ] **Step 1: Check the source.** Run `git -C ~/.claude/flow rev-parse --short HEAD` and `git -C ~/.claude/flow status --short`. Expected: `d937675` and no output. If HEAD moved, read what changed (`git -C ~/.claude/flow log --oneline d937675..HEAD`) and carry it; if the tree is dirty, ask the owner.

- [ ] **Step 2: Ignore bytecode first.** Add `__pycache__/` to `.gitignore`. Loom's diff probe runs `git add -N .`, and a test run would otherwise leave index entries.

- [ ] **Step 3: Copy the tree.**

```bash
F=~/.claude/flow; D=flow
mkdir -p $D/plugin/.claude-plugin $D/plugin/agents $D/plugin/skills $D/plugin/docs $D/lib
cp $F/agents/*.md $D/plugin/agents/
for s in init draft supervise retro habits; do mkdir -p $D/plugin/skills/$s; cp $F/skills/flow-$s/SKILL.md $D/plugin/skills/$s/SKILL.md; done
cp $F/docs/dispatch.md $F/docs/records.md $D/plugin/docs/
for f in $F/lib/*; do case "$(basename $f)" in install_test.py|post_commit_test.py|__pycache__) ;; *) cp "$f" $D/lib/ ;; esac; done
```

`flow/plugin/.claude-plugin/plugin.json`:

```json
{
  "name": "flow",
  "version": "1.0.0",
  "description": "loom's supervised plan-execution flow: pinned agents and the owner-started /flow:init, /flow:draft, /flow:supervise and /flow:retro"
}
```

`flow/lib/guarded-agents.txt`:

```
# The agents loom's git guard (`loom flow guard`) checks, by their name in the
# flow plugin, and how. developer: every Bash call. drafter: every Bash, Write
# and Edit call, with writes confined to the drafts directory
# (git_guard.py --drafter). check-agents.sh fails an agent that can Write or
# Edit and is missing here. One `<agent> <mode>` per line.
junior-dev developer
intermediate-dev developer
senior-dev developer
drafter drafter
```

- [ ] **Step 4: Split the banned list.** Delete the lines `prader`, `polymarket` and `polyoxide` from `flow/lib/banned-tokens.txt`. Add after its header: `# check-agents.sh also reads $FLOW_DATA/banned-tokens.txt (default ~/.loom/flow/banned-tokens.txt) when it exists: words that must not ship in loom's repo, such as client repo names.` In `flow/lib/git_guard_test.py:66`, change `prader-core` to `example-core`. The owner's private list is theirs to write; P5's handoff names it.

- [ ] **Step 5: Rewrite the prompts.** Save this as `$SP/rewrite.py` (outside the worktree) and run `python3 -I $SP/rewrite.py flow`:

```python
"""One-off: rewrite flow's moved prompts for the plugin (flow in loom plan, P2)."""
import re
import sys
from pathlib import Path

root = Path(sys.argv[1])
AGENTS = ["code-quality-reviewer", "drafter", "glance", "intermediate-dev", "junior-dev",
          "plan-execution-supervisor", "premise-check", "recon", "senior-dev", "spec-reviewer",
          "story-reviewer", "verifier"]
TOOLS = {"flowconf": "conf", "check_plan": "check-plan", "plan_packet": "packet", "plan_join": "join",
         "plan_premises": "premises", "plan_conformance": "conformance", "agent_costs": "costs"}
SKILLS = ["init", "draft", "supervise", "retro"]


def rewrite(text: str, in_skill: bool) -> str:
    for script, tool in TOOLS.items():
        text = re.sub(r'python3 (?:-I )?"?(?:\$HOME|~)/\.claude/flow/lib/' + script + r'\.py"?', f"loom flow {tool}", text)
    text = re.sub(r'(?:bash )?"?(?:\$HOME|~)/\.claude/flow/lib/agent-wait\.sh"?', "loom flow wait", text)
    docs = "${CLAUDE_SKILL_DIR}/../../docs/" if in_skill else "$(loom flow path docs/"
    for doc in ("dispatch.md", "records.md"):
        target = docs + doc if in_skill else f"$(loom flow path docs/{doc})"
        text = text.replace(f"~/.claude/flow/docs/{doc}", target).replace(f"$HOME/.claude/flow/docs/{doc}", target)
    text = text.replace("~/.claude/flow/skills/flow-habits/SKILL.md", "the flow:habits skill")
    for s in SKILLS:
        text = re.sub(r"/flow-" + s + r"\b", "/flow:" + s, text)
    text = re.sub(r"\bflow-habits\b", "flow:habits", text)
    for a in AGENTS:
        text = text.replace(f"`{a}`", f"`flow:{a}`")
    return text


def drop_hooks(text: str) -> str:
    """Remove a `hooks:` block from an agent's frontmatter: plugin agents ignore it."""
    lines, out, skipping, fences = text.split("\n"), [], False, 0
    for line in lines:
        if line == "---":
            fences += 1
            skipping = False
        if fences == 1 and line.startswith("hooks:"):
            skipping = True
            continue
        if skipping and (line[:1].isspace() or line == ""):
            continue
        skipping = False
        out.append(line)
    return "\n".join(out)


for path in sorted(root.rglob("*")):
    if path.suffix not in (".md", ".py", ".sh") or "_test.py" in path.name:
        continue
    text = path.read_text()
    new = rewrite(text, in_skill="skills" in path.parts)
    if path.parent.name == "agents":
        new = drop_hooks(new)
    if new != text:
        path.write_text(new)
        print("rewrote", path)
```

Then fix the frontmatter of the renamed skills by hand. In each `flow/plugin/skills/<s>/SKILL.md`, `name: flow-<s>` becomes `name: <s>`; in the habits skill, `name: flow:habits` becomes `name: habits`. The agents' `skills:` lists already read `flow:habits`.

- [ ] **Step 6: Review the leftovers by hand.** Run:

```bash
grep -rn -E '\.claude/flow|FLOW_HOME|install\.sh|home\.md|flow-(init|draft|supervise|retro|habits)|\.claude/agents|\$\(loom flow path docs/[a-z]+\.md\)' flow/plugin flow/lib --include='*.md' --include='*.py' --include='*.sh' | grep -v '_test.py'
```

Fix each hit so it says what is true under loom:
- **Records:** `~/.claude/flow/records/<key>/` becomes `~/.loom/flow/records/<key>/`.
- **Retros:** they live in `~/.loom/flow/records/retros/`. They no longer commit in a flow repo: §5.9 says roster changes land in loom's `flow/plugin/` and commit there.
- **Profiles:** `repos/<key>.toml` becomes the repo's own `.claude/flow.toml`.
- **Pin rule:** `PROVENANCE.md` becomes `flow/PROVENANCE.md in loom's repo`.
- **Agent file paths:** `~/.claude/agents/<x>.md` mentions become `flow/plugin/agents/<x>.md` in loom's repo.
- **Doc references in agents:** a sentence telling an agent to read a guide says "run `loom flow path docs/dispatch.md` and read the file it prints". Rewrite each `$(loom flow path docs/…)` hit as that kind of sentence.
- **The supervisor's dispatch line:** in `flow/plugin/agents/plan-execution-supervisor.md`, "Dispatch the story's **Developer**" (its step 1, line 66 in the source) gains: "as the agent `flow:<Developer>` (a plan names it bare: `intermediate-dev` is `flow:intermediate-dev`)".
- **Python comments and docstrings** naming `FLOW_HOME` change in step 7.

Expected after the pass: only hits in step 7's files.

- [ ] **Step 7: Change the Python for loom.**

`flow/lib/flowconf.py`:
- In the module docstring, the profile sentence becomes: "Its profile is `.claude/flow.toml` at the worktree's top level."
- The `roster` lines become: "`flowconf.py roster`: the roster ID loom gave this run (`FLOW_ROSTER`, `loom flow roster <12 hex>`)."
- Delete the paragraph that explains how `roster` hashes `git ls-tree`.
- "Every command works on the repo of the current directory (`roster` on the flow repo, FLOW_HOME)" becomes "Every command but `roster` works on the repo of the current directory".
- Replace `flow_home`, `find_profile` and `roster` with:

```python
def flow_root() -> Path:
    """The flow root loom runs this copy from: $FLOW_ROOT, else the directory above lib/."""
    return Path(os.environ.get("FLOW_ROOT") or Path(__file__).resolve().parent.parent)


def flow_data() -> Path:
    """Where run records and retros live: $FLOW_DATA, else ~/.loom/flow."""
    return Path(os.environ.get("FLOW_DATA") or Path.home() / ".loom" / "flow")


def find_profile(top: Path) -> Path:
    """The profile a worktree uses: its own .claude/flow.toml."""
    repo_file = top / ".claude" / "flow.toml"
    if repo_file.is_file():
        return repo_file
    raise FlowError(f"no flow profile: {repo_file} does not exist. Run /flow:init to draft one.")


def roster() -> str:
    """The roster ID loom gave this run, `loom flow roster <12 hex>` (FLOW_ROSTER)."""
    value = os.environ.get("FLOW_ROSTER", "").strip()
    if not value:
        raise FlowError("FLOW_ROSTER is not set: run flow's tools through `loom flow`")
    return value
```

- Delete `ROSTER_PATHS`.
- In `main`, `print(roster(flow_home()))` becomes `print(roster())`.
- Remove `hashlib` from the imports if nothing else uses it (`grep -n hashlib flow/lib/flowconf.py`).

`flow/lib/agent_costs.py`:
- The import becomes `from flowconf import flow_data`.
- `home_records` becomes:

```python
def home_records() -> list[str]:
    """Every run record loom keeps, $FLOW_DATA/records/<key>/*.md, the retros left out."""
    root = flow_data() / "records"
    return sorted(str(path) for path in root.glob("*/*.md") if path.parent.name != "retros")
```

- Its docstring's `($FLOW_HOME/records/<key>/*.md)` becomes `($FLOW_DATA/records/<key>/*.md)`.

`flow/lib/git_guard.py`:
- Delete `HOOK_COMMAND` and the two comment lines above it.
- In the docstring and in the message at line 245, `flow-habits` was already rewritten to `flow:habits` by step 5; check that the message reads `The flow:habits skill forbids it.`

`flow/lib/doctor.py`:
- The import line becomes `from flowconf import FlowError, Profile, drafts_dir, exclude_line, flow_root, load_profile, state_dir, toplevel`.
- Delete `from git_guard import HOOK_COMMAND`, `links_problem` and `shadowed`.
- Its docstring's install.sh line becomes "FAIL, and exit 1, when this is not a flow root loom wrote".
- Add:

```python
def root_problem() -> str | None:
    """None when FLOW_ROOT is a flow root loom wrote (plugin/, lib/ and ROSTER), else why not."""
    root = flow_root()
    missing = [part for part in ("plugin", "lib", "ROSTER") if not (root / part).exists()]
    if missing:
        return f"{root} is not a flow root (no {', '.join(missing)}): run flow's tools through `loom flow`"
    return None
```

- In `guard_problem`, the subprocess becomes `subprocess.run([sys.executable, "-I", str(flow_root() / "lib" / "git_guard.py")], input=json.dumps(payload), capture_output=True, text=True)`. Its messages name `git_guard.py` in place of `HOOK_COMMAND`.
- In `doctor()`, `report("links", links_problem())` becomes `report("flow root", root_problem())`.
- Delete the `for name in shadowed(top):` loop.

`flow/lib/check_plan.py`: at each place a story's Developer is read (the `developer` value compared with `DEVELOPERS` near line 275 and with `LAST_RESORT` near line 349), normalize it first with `developer = developer.removeprefix("flow:")`. Do it once, where the field is parsed, if one parse feeds both.

`flow/lib/check-agents.sh`:
- The header: "lint the flow's agents/*.md and skills/*/SKILL.md" becomes "lint the flow plugin's agents and skills (plugin/agents, plugin/skills)".
- Set `AGENTS_DIR="${AGENTS_DIR:-plugin/agents}"`, `SKILLS_DIR="${SKILLS_DIR:-plugin/skills}"`, `GUARDED="lib/guarded-agents.txt"` and `PRIVATE_BANNED="${FLOW_DATA:-$HOME/.loom/flow}/banned-tokens.txt"`.
- Delete `HOME_MD`, and delete the `HOOK_COMMAND` read and its check (the block from "# The developers' hook command is git_guard.HOOK_COMMAND" through `|| { echo "FAIL [guard] git_guard.HOOK_COMMAND is empty" >&2; exit 2; }`).
- The preload loop: after trimming `skill`, add:

```bash
    if [[ "$skill" != flow:* ]]; then
      fail skills "$file: preloads '$skill'; a plugin agent preloads a plugin skill by its namespaced name, flow:<name>"
      continue
    fi
    skill="${skill#flow:}"
```

- Replace the block from `if [[ "$tools" =~ (^|,)[[:space:]]*(Write|Edit)[[:space:]]*(,|$) ]]; then` through its closing `fi` (the drafter-mode, prose and awk hook checks plus maxTurns) with:

```bash
  if grep -qE '^hooks:' <<<"$fm"; then
    fail guard "$file: sets hooks:, which Claude ignores in a plugin agent; loom's git guard covers the agents $GUARDED lists"
  fi
  if [[ "$tools" =~ (^|,)[[:space:]]*(Write|Edit)[[:space:]]*(,|$) ]]; then
    mode="$(awk -v n="$base" '!/^#/ && $1 == n { print $2 }' "$GUARDED")"
    if [[ -z "$mode" ]]; then
      fail guard "$file: edits files but $GUARDED doesn't list it, so loom's git guard would not check its calls"
    elif [[ "$mode" == developer ]]; then
      for marker in 'diff-index' '--amend'; do
        grep -q -e "$marker" <<<"$text" \
          || fail git "$file: edits files but neither its body nor a preloaded skill mentions '$marker' — an implementer must read the shared index before a pathspec commit and must never rewrite history"
      done
    elif [[ "$mode" != drafter ]]; then
      fail guard "$GUARDED: '$base' has mode '$mode'; the modes are developer and drafter"
    fi
    [[ "$(field maxTurns)" =~ ^[1-9][0-9]*$ ]] \
      || fail guard "$file: edits files but sets no maxTurns — a runaway loop has no cap"
  fi
```

- After the agent loop, add:

```bash
if [[ ! -f "$GUARDED" ]]; then
  fail guard "$GUARDED does not exist"
else
  while read -r gname gmode; do
    [[ -z "$gname" || "$gname" == \#* ]] && continue
    [[ -f "$AGENTS_DIR/$gname.md" ]] || fail guard "$GUARDED lists '$gname', but $AGENTS_DIR/$gname.md does not exist"
  done < "$GUARDED"
fi
```

- The banned block:
  - Drop the `HOME_MD` checks.
  - Build the list from both files: `banned="$(cat "$BANNED" "$( [[ -f "$PRIVATE_BANNED" ]] && echo "$PRIVATE_BANNED" || echo /dev/null)" | grep -v -e '^#' -e '^[[:space:]]*$' || true)"`.
  - Scan `"$AGENTS_DIR" "$SKILLS_DIR" plugin/docs`.
  - The comment becomes "The flow serves every repo, so no agent, skill or doc may name one repo's tools or words."

`flow/lib/test.sh`:
- Add `export PYTHONDONTWRITEBYTECODE=1` after `set -uo pipefail`.
- The header comment becomes "loom's gate for flow's tools: every unit test in lib/, then the agent lint. CI runs it (.github/workflows/flow.yml); run it after changing flow/."

- [ ] **Step 8: Adapt the tests.** Each change follows step 7.

`flow/lib/flowconf_test.py`:
- Delete `test_the_home_store_profile_is_found_by_key`.
- In `test_the_repo_file_wins_whole_with_no_merge`, drop the home-store file and assert that the repo file is used.
- In `test_with_no_profile_the_cli_names_both_paths_and_flow_init`, rename it `test_with_no_profile_the_cli_names_the_repo_file_and_flow_init`; it asserts `.claude/flow.toml` and `/flow:init`.
- Replace the four `test_roster_*` tests (and any `FLOW_HOME` git-repo fixture only they use) with:

```python
    def test_roster_prints_the_id_loom_gave_the_run(self):
        result = run(["roster"], env={**os.environ, "FLOW_ROSTER": "loom flow roster 0123456789ab"})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "loom flow roster 0123456789ab")

    def test_roster_without_flow_roster_exits_2(self):
        env = {k: v for k, v in os.environ.items() if k != "FLOW_ROSTER"}
        result = run(["roster"], env=env)
        self.assertEqual(result.returncode, 2)
        self.assertIn("FLOW_ROSTER is not set", result.stderr)
```

  Use the file's own helper for running the CLI in place of `run`, and its own temp-repo setup, whichever names they have. Remove `FLOW_HOME` from every remaining test's environment.

`flow/lib/doctor_test.py`:
- Delete `test_a_shadowed_agent_is_a_warning_naming_it`.
- Replace `test_missing_links_fail` with `test_a_missing_flow_root_fails`: run doctor with `FLOW_ROOT` set to an empty temp dir, and assert a `FAIL flow root:` line naming `plugin, lib, ROSTER`.
- Where setUp builds a fake flow home with `install.sh` and `agents/`, build a fake `FLOW_ROOT` instead: `plugin/`, `ROSTER`, and `lib/` holding a copy of `git_guard.py`. Assert `PASS flow root` where the tests asserted `PASS links`.

`flow/lib/agent_costs_test.py`: in `test_records_without_files_read_the_home_store_and_leave_out_retros`, set `FLOW_DATA` (not `FLOW_HOME`) to the temp dir holding `records/`.

`flow/lib/git_guard_test.py`:
- Delete the `HookCommandTest` class.
- In `test_messages_name_the_flow_habits_skill_and_not_loom`, the expected text is `The flow:habits skill forbids it.`

`flow/lib/check_agents_test.py`:
- `setUp` builds `plugin/agents`, `plugin/skills/habits`, `lib/guarded-agents.txt` (content `dev developer\ndrafter drafter\n`) and copies `check-agents.sh`, `banned-tokens.txt` and `git_guard.py`. It writes no `home.md`, and sets `FLOW_DATA` in `CLEAN_ENV` to the temp dir.
- The fixtures `AGENT` and `DRAFTER` lose their `hooks:` blocks, and `AGENT`'s `skills:` reads `  - flow:habits`.
- Delete the tests that assert hook-command placement: every test whose name holds `guard_command`, or that edits `HOOK_COMMAND` or a hook's matcher (the ones at lines 173-205 of the source, among them `test_a_guard_command_under_post_tool_use_fails` and `test_a_guard_command_on_a_non_bash_matcher_fails`). Also delete `test_a_banned_word_in_home_md_fails`.
- Update every path from `agents/` and `skills/` to `plugin/agents/` and `plugin/skills/`.
- Add:

```python
    def test_an_agent_that_sets_hooks_fails(self):
        text = AGENT.replace("---\n\nYou build", "hooks:\n  PreToolUse: []\n---\n\nYou build")
        (self.root / "plugin" / "agents" / "dev.md").write_text(text)
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("sets hooks:", out)

    def test_an_editing_agent_the_guarded_list_misses_fails(self):
        (self.root / "lib" / "guarded-agents.txt").write_text("drafter drafter\n")
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("doesn't list it", out)

    def test_a_guarded_agent_without_its_file_fails(self):
        (self.root / "lib" / "guarded-agents.txt").write_text("dev developer\nghost developer\n")
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("lists 'ghost'", out)

    def test_a_preload_without_the_flow_prefix_fails(self):
        (self.root / "plugin" / "agents" / "dev.md").write_text(AGENT.replace("  - flow:habits", "  - habits"))
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("namespaced name", out)

    def test_a_word_from_the_private_list_fails(self):
        Path(self.tmp.name, "banned-tokens.txt").write_text("kumquat\n")
        (self.root / "plugin" / "skills" / "habits" / "SKILL.md").write_text(SKILL + "\nThe kumquat repo.\n")
        code, out = self.lint()
        self.assertEqual(code, 1, out)
        self.assertIn("kumquat", out)
```

  Here `CLEAN_ENV` has `FLOW_DATA=self.tmp.name`, set in `setUp` on a per-test copy. The test that writes `DRAFTER` must still pass: `drafter` is listed as a drafter, and its body needs no `diff-index`.

- [ ] **Step 9: Run flow's suite.** Run `bash flow/lib/test.sh`. Expected: unittest `OK`, then `OK: agent definitions passed (12 files)`. Then check `git status --short | grep pycache` prints nothing.

- [ ] **Step 10: CI.** Create `.github/workflows/flow.yml`, mirroring `build.yml`'s `actions/checkout` version:

```yaml
name: Flow

on:
  push:
    branches: [ main ]
    paths:
      - 'flow/**'
      - '.github/workflows/flow.yml'
  pull_request:
    branches: [ main ]
    paths:
      - 'flow/**'
      - '.github/workflows/flow.yml'

jobs:
  flow:
    name: Flow's tools and agent lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: '3.12'
      - name: flow/lib/test.sh
        run: bash flow/lib/test.sh
```

- [ ] **Step 11: Amend the spec.** Add a "Planning amendments (2026-11-05)" section to the spec after §2, listing amendments 1-9 above in one line each.

- [ ] **Step 12: Commit.**

```bash
git add -- flow .gitignore .github/workflows/flow.yml docs/superpowers/specs/2026-11-05-flow-in-loom-distribution-design.md
git commit -m "feat(flow): move the agent flow into loom as a plugin and its tools" -- flow .gitignore .github/workflows/flow.yml docs/superpowers/specs/2026-11-05-flow-in-loom-distribution-design.md
```

---

## P3 — The `flow` package and `loom flow`

**Files:**
- Create `flow/embed.go`, `flow/embed_test.go`, `flow/CLAUDE.md`, `flow/README.md`, `flow/PROVENANCE.md`, `internal/cli/flow.go`, `internal/cli/flow_test.go`.
- Modify `main.go`, `internal/cli/CLAUDE.md`.

- [ ] **Step 1: Write the package's tests.** `flow/embed_test.go`:

```go
package flow

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrite_WritesThePluginAndToolsButNoTests(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "flow")
	require.NoError(t, Write(dir))

	assert.True(t, IsRoot(dir))
	assert.FileExists(t, filepath.Join(dir, "plugin", ".claude-plugin", "plugin.json"), "all: keeps the dot-directory")
	assert.FileExists(t, filepath.Join(dir, "plugin", "agents", "verifier.md"))
	assert.FileExists(t, filepath.Join(dir, "plugin", "skills", "supervise", "SKILL.md"))
	assert.FileExists(t, filepath.Join(dir, "lib", "flowconf.py"))
	assert.FileExists(t, filepath.Join(dir, "lib", "guarded-agents.txt"))
	assert.NoFileExists(t, filepath.Join(dir, "lib", "flowconf_test.py"))
	roster, err := os.ReadFile(filepath.Join(dir, RosterFile))
	require.NoError(t, err)
	assert.Equal(t, RosterID()+"\n", string(roster))
	assert.NoDirExists(t, dir+".partial")
}

func TestWrite_RefusesADirThatExistsAndLeavesIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "flow")
	require.NoError(t, Write(dir))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mine"), []byte("x"), 0o600))

	assert.Error(t, Write(dir))
	assert.FileExists(t, filepath.Join(dir, "mine"), "a refused write changes nothing")
	assert.NoDirExists(t, dir+".partial")
}

func TestRosterID_ChangesWithAPluginFileAndNotWithLib(t *testing.T) {
	base := fstest.MapFS{
		"plugin/agents/a.md": {Data: []byte("one")},
		"lib/x.py":           {Data: []byte("x")},
	}
	edited := fstest.MapFS{
		"plugin/agents/a.md": {Data: []byte("two")},
		"lib/x.py":           {Data: []byte("x")},
	}
	libOnly := fstest.MapFS{
		"plugin/agents/a.md": {Data: []byte("one")},
		"lib/x.py":           {Data: []byte("y")},
	}
	assert.Regexp(t, `^loom flow roster [0-9a-f]{12}$`, rosterOf(base))
	assert.NotEqual(t, rosterOf(base), rosterOf(edited))
	assert.Equal(t, rosterOf(base), rosterOf(libOnly))
	assert.Equal(t, rosterOf(files), RosterID())
}

func TestGuarded_TheShippedListParsesAndNamesRealAgents(t *testing.T) {
	g, err := Guarded()
	require.NoError(t, err)
	assert.Equal(t, Developer, g["intermediate-dev"])
	assert.Equal(t, Drafter, g["drafter"])
	for name := range g {
		_, err := fs.Stat(files, "plugin/agents/"+name+".md")
		assert.NoError(t, err, name)
	}
}

func TestParseGuarded_RefusesABadLine(t *testing.T) {
	for _, line := range []string{"dev", "dev reviewer", "dev developer extra"} {
		_, err := parseGuarded(fstest.MapFS{"lib/guarded-agents.txt": {Data: []byte("# c\n" + line + "\n")}})
		assert.ErrorContains(t, err, "guarded-agents.txt:2", line)
	}
}

func TestMode_Guards(t *testing.T) {
	assert.True(t, Developer.Guards("Bash"))
	assert.False(t, Developer.Guards("Write"))
	for _, tool := range []string{"Bash", "Write", "Edit"} {
		assert.True(t, Drafter.Guards(tool), tool)
	}
	assert.False(t, Drafter.Guards("Read"))
}

func TestGuardedMode_MatchesTheAgentTypeTheProbeSaw(t *testing.T) {
	mode, ok := GuardedMode(probedPrefix + "junior-dev")
	assert.True(t, ok)
	assert.Equal(t, Developer, mode)
	_, ok = GuardedMode(probedPrefix + "verifier")
	assert.False(t, ok, "an unguarded flow agent")
	_, ok = GuardedMode("")
	assert.False(t, ok, "the main thread")
	if probedPrefix != "" {
		_, ok = GuardedMode("junior-dev")
		assert.False(t, ok, "a bare name is another plugin's or the home install's agent")
	}
	assert.True(t, strings.HasSuffix(probedPrefix, ":") || probedPrefix == "")
}
```

- [ ] **Step 2: Run them to see them fail.** Run `CGO_ENABLED=0 go test ./flow/`. Expected: a build failure (undefined `Write`, …).

- [ ] **Step 3: Write `flow/embed.go`.**

```go
// Package flow is the agent flow loom ships: the `flow` Claude plugin
// (plugin/: the pinned agents, the owner-started skills and the dispatch
// guide) and the tools behind `loom flow` (lib/). Each Claude launch gets
// its own copy in its hooks folder (Write), passed with --plugin-dir and
// named in the launch's environment (EnvRoot). Design:
// docs/superpowers/specs/2026-11-05-flow-in-loom-distribution-design.md.
package flow

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// files is the flow tree. all: keeps plugin/.claude-plugin, which a plain
// pattern skips; plain lib leaves out a __pycache__ a local test run left.
//
//go:embed all:plugin lib
var files embed.FS

// EnvRoot names a launch's flow root in its environment.
const EnvRoot = "LOOM_FLOW_ROOT"

// RosterFile is the file in a flow root that holds its roster ID.
const RosterFile = "ROSTER"

// probedPrefix is what the hook input's agent_type puts before a flow
// agent's name, as TestRealClaude_FlowPluginContract saw it (P1).
const probedPrefix = "flow:"

// Write writes the flow tree into dir: plugin/, lib/ without its tests, and
// RosterFile. It refuses a dir that exists, and writes a sibling first and
// renames it into place, so dir never holds part of a copy.
func Write(dir string) error {
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("flow: write %s: it exists", dir)
	}
	partial := dir + ".partial"
	if err := os.RemoveAll(partial); err != nil {
		return fmt.Errorf("flow: clear %s: %w", partial, err)
	}
	if err := writeTree(partial); err != nil {
		_ = os.RemoveAll(partial)
		return fmt.Errorf("flow: write %s: %w", dir, err)
	}
	if err := os.Rename(partial, dir); err != nil {
		_ = os.RemoveAll(partial)
		return fmt.Errorf("flow: write %s: %w", dir, err)
	}
	return nil
}

func writeTree(dst string) error {
	err := fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), "_test.py") {
			return nil
		}
		target := filepath.Join(dst, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, RosterFile), []byte(RosterID()+"\n"), 0o600)
}

// IsRoot reports whether dir is a flow root Write made.
func IsRoot(dir string) bool {
	for _, part := range []string{"plugin", "lib", RosterFile} {
		if _, err := os.Stat(filepath.Join(dir, part)); err != nil {
			return false
		}
	}
	return true
}

// RosterID names the roster this build ships, as run records carry it:
// "loom flow roster " and the first 12 hex digits of the SHA-256 over
// plugin/'s files, each as its path, its length and its bytes, in path
// order. lib/ is left out: a tool's fix doesn't change the roster.
func RosterID() string { return rosterOf(files) }

func rosterOf(fsys fs.FS) string {
	h := sha256.New()
	// An embedded or in-memory tree can't fail to read.
	_ = fs.WalkDir(fsys, "plugin", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(b))
		h.Write(b)
		return nil
	})
	return "loom flow roster " + hex.EncodeToString(h.Sum(nil))[:12]
}

// Mode is how loom's git guard checks a guarded agent's calls.
type Mode string

const (
	// Developer checks every Bash call.
	Developer Mode = "developer"
	// Drafter checks every Bash, Write and Edit call, with
	// git_guard.py --drafter.
	Drafter Mode = "drafter"
)

// Guards reports whether m checks a call to tool.
func (m Mode) Guards(tool string) bool {
	switch m {
	case Developer:
		return tool == "Bash"
	case Drafter:
		return tool == "Bash" || tool == "Write" || tool == "Edit"
	}
	return false
}

// Guarded is lib/guarded-agents.txt: each agent loom's git guard checks,
// by its name in the plugin, and its mode.
func Guarded() (map[string]Mode, error) { return guarded() }

var guarded = sync.OnceValues(func() (map[string]Mode, error) { return parseGuarded(files) })

func parseGuarded(fsys fs.FS) (map[string]Mode, error) {
	b, err := fs.ReadFile(fsys, "lib/guarded-agents.txt")
	if err != nil {
		return nil, fmt.Errorf("flow: %w", err)
	}
	out := map[string]Mode{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 || (Mode(f[1]) != Developer && Mode(f[1]) != Drafter) {
			return nil, fmt.Errorf("flow: guarded-agents.txt:%d: want `<agent> developer|drafter`, got %q", n, line)
		}
		out[f[0]] = Mode(f[1])
	}
	return out, sc.Err()
}

// GuardedMode is the mode the hook input's agent_type gets, and whether
// the guard checks it at all. A type without probedPrefix (the main
// thread's "", another plugin's agent, an agent of the home install) is
// not flow's and passes.
func GuardedMode(agentType string) (Mode, bool) {
	name, ok := strings.CutPrefix(agentType, probedPrefix)
	if !ok || name == "" {
		return "", false
	}
	g, err := Guarded()
	if err != nil {
		return Drafter, true // a list that can't be read guards every flow agent: fail closed
	}
	mode, ok := g[name]
	return mode, ok
}
```

If P1 recorded a bare agent type, set `probedPrefix = ""`. `GuardedMode` then matches bare names, and its test's namespaced-only assertion is skipped by its own `if`.

- [ ] **Step 4: Run the package's tests.** Run `CGO_ENABLED=0 go test ./flow/`. Expected: PASS.

- [ ] **Step 5: Write the CLI's tests.** `internal/cli/flow_test.go`:

```go
package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aidan-bailey/loom/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFlowRoot is a flow root whose lib holds the given scripts.
func fakeFlowRoot(t *testing.T, scripts map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "flow")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "plugin", "docs"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "lib"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, flow.RosterFile), []byte("loom flow roster 0123456789ab\n"), 0o600))
	for name, body := range scripts {
		require.NoError(t, os.WriteFile(filepath.Join(root, "lib", name), []byte(body), 0o600))
	}
	return root
}

func needPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
}

func runFlow(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := newFlowCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func exitCode(err error) int {
	var exit *ExitError
	if errors.As(err, &exit) {
		return exit.Code
	}
	if err != nil {
		return 1
	}
	return 0
}

func TestFlowTool_PassesArgsEnvAndTheExitCodeThrough(t *testing.T) {
	needPython(t)
	root := fakeFlowRoot(t, map[string]string{"check_plan.py": "import os, sys\n" +
		"print(' '.join(sys.argv[1:]), os.environ['FLOW_ROSTER'], os.environ['FLOW_ROOT'] == os.environ['LOOM_FLOW_ROOT'])\n" +
		"sys.exit(7)\n"})
	t.Setenv(flow.EnvRoot, root)

	out, _, err := runFlow(t, "check-plan", "--x", "plan.md")

	assert.Equal(t, 7, exitCode(err))
	assert.Equal(t, "--x plan.md loom flow roster 0123456789ab True\n", out)
}

func TestFlowTool_DoctorRunsFlowconfDoctor(t *testing.T) {
	needPython(t)
	t.Setenv(flow.EnvRoot, fakeFlowRoot(t, map[string]string{"flowconf.py": "import sys\nprint(sys.argv[1:])\n"}))
	out, _, err := runFlow(t, "doctor")
	require.NoError(t, err)
	assert.Equal(t, "['doctor']\n", out)
}

func TestFlowTool_RefusesARootThatIsNotOne(t *testing.T) {
	t.Setenv(flow.EnvRoot, t.TempDir())
	_, _, err := runFlow(t, "conf", "status")
	assert.Equal(t, exitNotRun, exitCode(err))
	assert.ErrorContains(t, err, "is not a flow root")
}

func TestFlowTool_RefusesWithoutPython(t *testing.T) {
	t.Setenv(flow.EnvRoot, fakeFlowRoot(t, nil))
	t.Setenv("PATH", t.TempDir())
	_, _, err := runFlow(t, "conf", "status")
	assert.Equal(t, exitNotRun, exitCode(err))
	assert.ErrorContains(t, err, "python3 is not on PATH")
}

func TestFlowTool_OutsideASessionRunsThisBuildsCopy(t *testing.T) {
	needPython(t)
	t.Setenv(flow.EnvRoot, "")
	out, _, err := runFlow(t, "conf", "roster")
	require.NoError(t, err)
	assert.Equal(t, flow.RosterID()+"\n", out)
}

func TestFlowTool_EveryToolStartsUnderIsolatedPython(t *testing.T) {
	needPython(t)
	t.Setenv(flow.EnvRoot, "")
	for _, tool := range flowTools {
		if strings.HasSuffix(tool.script, ".sh") {
			continue
		}
		// A script without argparse may refuse --help; what this pins is that
		// every one imports its siblings under -I, which adds no script dir.
		_, errOut, _ := runFlow(t, tool.name, "--help")
		assert.NotContains(t, errOut, "ModuleNotFoundError", tool.name)
		assert.NotContains(t, errOut, "ImportError", tool.name)
	}
}

func TestFlowPath_PrintsAPluginFileOnlyInsideASession(t *testing.T) {
	root := fakeFlowRoot(t, nil)
	require.NoError(t, os.WriteFile(filepath.Join(root, "plugin", "docs", "dispatch.md"), []byte("d"), 0o600))
	t.Setenv(flow.EnvRoot, root)

	out, _, err := runFlow(t, "path", "docs/dispatch.md")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "plugin", "docs", "dispatch.md")+"\n", out)

	_, _, err = runFlow(t, "path", "docs/missing.md")
	assert.Equal(t, 1, exitCode(err))
	_, _, err = runFlow(t, "path", "../ROSTER")
	assert.Equal(t, 1, exitCode(err), "a path out of the plugin")

	t.Setenv(flow.EnvRoot, "")
	_, _, err = runFlow(t, "path", "docs/dispatch.md")
	assert.Equal(t, 1, exitCode(err))
	assert.ErrorContains(t, err, "inside a session")
}

// guardScript is a git_guard.py that prints its arguments and exits code.
func guardScript(code int) string {
	return "import sys\nsys.stdin.read()\nprint(sys.argv[1:])\nsys.exit(" + string(rune('0'+code)) + ")\n"
}

func guard(t *testing.T, root, input string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runFlowGuard(strings.NewReader(input), &out, &errOut, root, exec.LookPath)
	return code, out.String(), errOut.String()
}

func TestFlowGuard_ChecksOnlyTheCallsAGuardedAgentsModeCovers(t *testing.T) {
	needPython(t)
	root := fakeFlowRoot(t, map[string]string{"git_guard.py": guardScript(2)})
	dev, drafter := `"agent_type":"flow:junior-dev"`, `"agent_type":"flow:drafter"`

	code, _, _ := guard(t, root, `{`+dev+`,"tool_name":"Bash"}`)
	assert.Equal(t, 2, code, "a developer's Bash call gets the guard's verdict")
	code, out, _ := guard(t, root, `{`+drafter+`,"tool_name":"Write"}`)
	assert.Equal(t, 2, code)
	assert.Contains(t, out, "--drafter")

	for _, input := range []string{
		`{` + dev + `,"tool_name":"Write"}`,                 // a developer's Write: not its mode's
		`{"agent_type":"flow:verifier","tool_name":"Bash"}`, // not guarded
		`{"tool_name":"Bash"}`,                              // the main thread
		`not json`,
	} {
		code, out, _ := guard(t, root, input)
		assert.Equal(t, 0, code, input)
		assert.Empty(t, out, "the guard did not run: %s", input)
	}
}

func TestFlowGuard_PassesWhatTheGuardAllows(t *testing.T) {
	needPython(t)
	code, _, _ := guard(t, fakeFlowRoot(t, map[string]string{"git_guard.py": guardScript(0)}), `{"agent_type":"flow:junior-dev","tool_name":"Bash"}`)
	assert.Equal(t, 0, code)
}

func TestFlowGuard_FailsClosedForAGuardedAgent(t *testing.T) {
	needPython(t)
	in := `{"agent_type":"flow:junior-dev","tool_name":"Bash"}`

	code, _, errOut := guard(t, "", in)
	assert.Equal(t, 2, code, "no root")
	assert.Contains(t, errOut, flow.EnvRoot)

	code, _, _ = guard(t, fakeFlowRoot(t, map[string]string{"git_guard.py": guardScript(1)}), in)
	assert.Equal(t, 2, code, "a guard that crashed blocks, since Claude reads exit 1 as allow")

	var out, errBuf bytes.Buffer
	noPython := func(string) (string, error) { return "", exec.ErrNotFound }
	assert.Equal(t, 2, runFlowGuard(strings.NewReader(in), &out, &errBuf, fakeFlowRoot(t, nil), noPython))
	assert.Contains(t, errBuf.String(), "python3")

	code, _, _ = guard(t, "", `{"agent_type":"flow:verifier","tool_name":"Bash"}`)
	assert.Equal(t, 0, code, "an unguarded agent passes whatever the guard's state")
}

func TestFlowImport_CopiesSkipsAndRefusesRecords(t *testing.T) {
	checkout, data := t.TempDir(), t.TempDir()
	write := func(base, rel, body string) {
		p := filepath.Join(base, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o700))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	}
	write(checkout, "records/-r/new.md", "new")
	write(checkout, "records/-r/same.md", "same")
	write(checkout, "records/-r/other.md", "theirs")
	write(checkout, "records/retros/retro-2026-11-01.md", "retro")
	write(checkout, "repos/-nowhere-at-all.toml", "schema = 1\n")
	write(data, "records/-r/same.md", "same")
	write(data, "records/-r/other.md", "mine")

	var out bytes.Buffer
	refused, err := importRecords(checkout, data, &out)
	require.NoError(t, err)

	assert.Equal(t, 1, refused)
	got, _ := os.ReadFile(filepath.Join(data, "records", "-r", "new.md"))
	assert.Equal(t, "new", string(got))
	got, _ = os.ReadFile(filepath.Join(data, "records", "-r", "other.md"))
	assert.Equal(t, "mine", string(got), "a differing record is never overwritten")
	assert.FileExists(t, filepath.Join(data, "records", "retros", "retro-2026-11-01.md"))
	assert.Contains(t, out.String(), "refused records/-r/other.md")
	assert.Contains(t, out.String(), "repos/-nowhere-at-all.toml: no checkout at the path its key names")
	assert.Contains(t, out.String(), "copied 2, skipped 1, refused 1")
}

func TestCheckoutOf_FindsTheDirectoryAKeyNames(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	dir := filepath.Join(base, "Dilettante Trading", "prader-rs.x")
	require.NoError(t, os.MkdirAll(dir, 0o700))

	got, ok := checkoutOf(keyOf(dir))
	assert.True(t, ok)
	assert.Equal(t, dir, got)
	_, ok = checkoutOf(keyOf(filepath.Join(base, "absent")))
	assert.False(t, ok)
}
```

- [ ] **Step 6: Run them to see them fail.** Run `CGO_ENABLED=0 go test ./internal/cli/ -run 'Flow|CheckoutOf'`. Expected: a build failure.

- [ ] **Step 7: Write `internal/cli/flow.go`.**

```go
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aidan-bailey/loom/config"
	"github.com/aidan-bailey/loom/flow"
	"github.com/spf13/cobra"
)

// FlowCmd is `loom flow`: the agent flow's tools (flow in loom spec §5.5).
// main attaches it.
var FlowCmd = newFlowCmd()

// flowTool is a `loom flow` subcommand that runs a script in a flow
// root's lib/, with args before the caller's.
type flowTool struct {
	name, script, short string
	args                []string
}

var flowTools = []flowTool{
	{name: "conf", script: "flowconf.py", short: "The repo's flow key, profile, role blocks, state, drafts and gates"},
	{name: "check-plan", script: "check_plan.py", short: "Check that a plan is ready for the supervised option"},
	{name: "packet", script: "plan_packet.py", short: "Write a plan's seams packet"},
	{name: "join", script: "plan_join.py", short: "Rebuild a plan's stories from their drafts"},
	{name: "premises", script: "plan_premises.py", short: "List a plan's premises the tree doesn't bear out"},
	{name: "conformance", script: "plan_conformance.py", short: "Show which of a story's plan code landed as written"},
	{name: "costs", script: "agent_costs.py", short: "Token usage and API-price cost of a session's agents, or of run records"},
	{name: "doctor", script: "flowconf.py", args: []string{"doctor"}, short: "Check that a supervised run can start here"},
	{name: "check-agents", script: "check-agents.sh", short: "Lint the flow plugin's agents and skills"},
	{name: "wait", script: "agent-wait.sh", short: "Start a long step, or wait for one inside a turn"},
}

func newFlowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "flow",
		Short: "The agent flow's tools: plans, profiles, gates, the git guard",
	}
	for _, tool := range flowTools {
		cmd.AddCommand(&cobra.Command{
			Use:                tool.name + " [ARGS…]",
			Short:              tool.short,
			DisableFlagParsing: true, // the script parses its own flags, --help among them
			RunE:               func(c *cobra.Command, args []string) error { return runFlowTool(c, tool, args) },
		})
	}
	cmd.AddCommand(newFlowGuardCmd(), newFlowPathCmd(), newFlowImportCmd())
	return cmd
}

// flowRoot is the flow root a tool runs from, and its cleanup: the
// launch's (flow.EnvRoot) inside a session, else this binary's copy,
// written to a temp dir for the call. A set EnvRoot that is not a flow
// root is refused, so a tool never runs another copy than its prompts'.
func flowRoot() (string, func(), error) {
	if dir := os.Getenv(flow.EnvRoot); dir != "" {
		if !flow.IsRoot(dir) {
			return "", nil, &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow: %s=%s is not a flow root", flow.EnvRoot, dir)}
		}
		return dir, func() {}, nil
	}
	tmp, err := os.MkdirTemp("", "loom-flow-")
	if err != nil {
		return "", nil, fmt.Errorf("loom flow: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	root := filepath.Join(tmp, "flow")
	if err := flow.Write(root); err != nil {
		cleanup()
		return "", nil, err
	}
	return root, cleanup, nil
}

// flowDataDir is where run records and retros live: <global dir>/flow.
func flowDataDir() string { return filepath.Join(config.GetGlobalConfigDir(), "flow") }

// runFlowTool runs tool's script from a flow root, its stdio the
// command's, and ends with the script's exit code (an *ExitError for a
// non-zero one).
func runFlowTool(c *cobra.Command, tool flowTool, args []string) error {
	root, cleanup, err := flowRoot()
	if err != nil {
		return err
	}
	defer cleanup()
	interp, argv := "python3", []string{"-I", filepath.Join(root, "lib", tool.script)}
	if strings.HasSuffix(tool.script, ".sh") {
		interp, argv = "bash", argv[1:]
	}
	bin, err := exec.LookPath(interp)
	if err != nil {
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow %s: %s is not on PATH; flow's tools need it", tool.name, interp)}
	}
	roster, err := os.ReadFile(filepath.Join(root, flow.RosterFile))
	if err != nil {
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow %s: %v", tool.name, err)}
	}
	argv = append(append(argv, tool.args...), args...)
	runner := exec.Command(bin, argv...)
	runner.Stdin, runner.Stdout, runner.Stderr = c.InOrStdin(), c.OutOrStdout(), c.ErrOrStderr()
	runner.Env = append(os.Environ(),
		flow.EnvRoot+"="+root, "FLOW_ROOT="+root, "FLOW_DATA="+flowDataDir(),
		"FLOW_ROSTER="+strings.TrimSpace(string(roster)))
	if err := runner.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			return &ExitError{Code: exit.ExitCode()}
		}
		return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow %s: %v", tool.name, err)}
	}
	return nil
}

func newFlowGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guard",
		Short: "Loom's PreToolUse hook: the git guard for flow's developers and drafter",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if code := runFlowGuard(c.InOrStdin(), c.OutOrStdout(), c.ErrOrStderr(), os.Getenv(flow.EnvRoot), exec.LookPath); code != 0 {
				return &ExitError{Code: code}
			}
			return nil
		},
	}
}

// runFlowGuard is `loom flow guard` (spec §5.3). It reads the hook input,
// and for a call a guarded agent's mode covers it runs git_guard.py from
// the flow root on the same input and returns its verdict. A covered call
// it can't check is blocked (2), since Claude reads any other non-zero
// exit as allow; every other call passes (0), whatever the guard's state.
func runFlowGuard(in io.Reader, stdout, stderr io.Writer, root string, lookPath func(string) (string, error)) int {
	input, err := io.ReadAll(in)
	if err != nil {
		return 0 // no input names an agent, so nothing says the call is a guarded one
	}
	var call struct {
		AgentType string `json:"agent_type"`
		ToolName  string `json:"tool_name"`
	}
	if json.Unmarshal(input, &call) != nil {
		return 0
	}
	mode, ok := flow.GuardedMode(call.AgentType)
	if !ok || !mode.Guards(call.ToolName) {
		return 0
	}
	block := func(why string) int {
		fmt.Fprintf(stderr, "loom flow guard: %s's %s call is blocked: %s\n", call.AgentType, call.ToolName, why)
		return 2
	}
	if root == "" || !flow.IsRoot(root) {
		return block(flow.EnvRoot + " names no flow root, so the git guard can't run")
	}
	python, err := lookPath("python3")
	if err != nil {
		return block("python3 is not on PATH, so the git guard can't run")
	}
	args := []string{"-I", filepath.Join(root, "lib", "git_guard.py")}
	if mode == flow.Drafter {
		args = append(args, "--drafter")
	}
	runner := exec.Command(python, args...)
	runner.Stdin, runner.Stdout, runner.Stderr = bytes.NewReader(input), stdout, stderr
	err = runner.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit) && exit.ExitCode() == 2:
		return 2
	default:
		return block(fmt.Sprintf("the git guard failed: %v", err))
	}
}

func newFlowPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path FILE",
		Short: "Print the absolute path of a file in this session's flow plugin (docs/dispatch.md, …)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			root := os.Getenv(flow.EnvRoot)
			if root == "" {
				return fmt.Errorf("loom flow path: %s is unset; it names a launch's flow root, so run this inside a session loom launched", flow.EnvRoot)
			}
			if !flow.IsRoot(root) {
				return &ExitError{Code: exitNotRun, Msg: fmt.Sprintf("loom flow path: %s=%s is not a flow root", flow.EnvRoot, root)}
			}
			plugin := filepath.Join(root, "plugin")
			p := filepath.Join(plugin, filepath.FromSlash(args[0]))
			if rel, err := filepath.Rel(plugin, p); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("loom flow path: %s is outside the flow plugin", args[0])
			}
			if _, err := os.Stat(p); err != nil {
				return fmt.Errorf("loom flow path: the flow plugin has no %s", args[0])
			}
			fmt.Fprintln(c.OutOrStdout(), p)
			return nil
		},
	}
}

func newFlowImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import CHECKOUT",
		Short: "Copy a claude-flow checkout's run records into loom, and say where its profiles go",
		Long: "Copies CHECKOUT's records/ into <global dir>/flow/records: a record already there with the same bytes is " +
			"skipped, one with other bytes is refused and listed. Then prints where each repos/<key>.toml belongs: " +
			"that repo's .claude/flow.toml, for you or that repo's session to commit. Exits 1 when it refused any record.",
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			refused, err := importRecords(args[0], flowDataDir(), c.OutOrStdout())
			if err != nil {
				return err
			}
			if refused > 0 {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
}

// importRecords copies checkout's records/ into data/records, never over a
// file that differs, then lists where each of checkout's profiles belongs.
// It returns how many records it refused.
func importRecords(checkout, data string, out io.Writer) (int, error) {
	var copied, skipped, refused int
	src := filepath.Join(checkout, "records")
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(checkout, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		target := filepath.Join(data, rel)
		switch have, err := os.ReadFile(target); {
		case err == nil && bytes.Equal(have, b):
			skipped++
		case err == nil:
			refused++
			fmt.Fprintf(out, "refused %s: %s differs\n", filepath.ToSlash(rel), target)
		case errors.Is(err, fs.ErrNotExist):
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			if err := config.AtomicWriteFile(target, b, 0o600); err != nil {
				return err
			}
			copied++
		default:
			return err
		}
		return nil
	})
	if err != nil {
		return refused, fmt.Errorf("loom flow import: %w", err)
	}
	profiles, err := filepath.Glob(filepath.Join(checkout, "repos", "*.toml"))
	if err != nil {
		return refused, fmt.Errorf("loom flow import: %w", err)
	}
	for _, p := range profiles {
		key := strings.TrimSuffix(filepath.Base(p), ".toml")
		if dir, ok := checkoutOf(key); ok {
			fmt.Fprintf(out, "repos/%s.toml: copy it to %s and commit it there\n", key, filepath.Join(dir, ".claude", "flow.toml"))
		} else {
			fmt.Fprintf(out, "repos/%s.toml: no checkout at the path its key names\n", key)
		}
	}
	fmt.Fprintf(out, "copied %d, skipped %d, refused %d\n", copied, skipped, refused)
	return refused, nil
}

var nonKey = regexp.MustCompile(`[^A-Za-z0-9]`)

// keyOf is flowconf's key of a path: every character outside [A-Za-z0-9]
// made "-".
func keyOf(path string) string { return nonKey.ReplaceAllString(path, "-") }

// checkoutOf is the directory whose resolved path key names, found by
// walking down from / one entry at a time, or false when none does.
func checkoutOf(key string) (string, bool) {
	return walkKey(string(filepath.Separator), strings.TrimPrefix(key, "-"))
}

func walkKey(dir, rest string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, next := keyOf(e.Name()), filepath.Join(dir, e.Name())
		if rest == s {
			return next, true
		}
		if strings.HasPrefix(rest, s+"-") {
			if found, ok := walkKey(next, rest[len(s)+1:]); ok {
				return found, true
			}
		}
	}
	return "", false
}
```

`config.GetGlobalConfigDir()` and `config.AtomicWriteFile(path, data, perm)`: check each signature with `grep -n 'func GetGlobalConfigDir\|func AtomicWriteFile' config/*.go`. If `GetGlobalConfigDir` also returns an error, `flowDataDir` returns `(string, error)`, and both callers pass the error up.

- [ ] **Step 8: Attach it.** In `main.go`, after `rootCmd.AddCommand(cli.FeedbackCmd)`, add `rootCmd.AddCommand(cli.FlowCmd)`.

- [ ] **Step 9: Run the tests.** Run `CGO_ENABLED=0 go test ./flow/ ./internal/cli/ -run 'Flow|CheckoutOf|Write|Roster|Guard|Mode'`. Expected: PASS. If `TestFlowTool_EveryToolStartsUnderIsolatedPython` reports an import error, that script lacks the `sys.path.insert(0, …parent)` the others have. Add it, in `flow/lib`, before the script's first import of a sibling.

- [ ] **Step 10: Write the package docs.**

`flow/CLAUDE.md`:

```markdown
# flow

The agent flow loom ships: the `flow` Claude plugin (`plugin/`: 12 pinned agents, the owner-started skills `/flow:init`, `/flow:draft`, `/flow:supervise` and `/flow:retro`, the preloaded `flow:habits`, and the dispatch guide) and the tools behind `loom flow` (`lib/`, Python and bash), embedded in the binary. `session` writes a copy into each Claude launch's hooks folder (`Write`). Full reference in [`README.md`](README.md).

## Rules when modifying this package

- **Change an agent's model, effort, maxTurns, tools or cache lifetime only on the owner's instruction, and record it with its date in `flow/PROVENANCE.md`.** The roster is tuned on run evidence by an owner-started retro; a pin changed on a hunch costs every run. **Convention** — a roster that drifts from its evidence.
- **Name the agents loom's git guard checks in `lib/guarded-agents.txt` only, and give no agent a `hooks:` block.** Claude ignores `hooks` in a plugin agent, so loom's PreToolUse hook (`loom flow guard`) is the only guard, and it reads this list. **Enforced** by `check-agents.sh` (an editing agent missing from the list, a listed agent without a file, an agent with `hooks:`) through `.github/workflows/flow.yml`, and by `TestGuarded_TheShippedListParsesAndNamesRealAgents`.
- **Embed with `all:plugin`, and keep `lib` plain.** A plain `plugin` pattern skips `.claude-plugin`, and Claude then loads no plugin; `all:lib` would embed a `__pycache__` a local run left. **Enforced** by `TestWrite_WritesThePluginAndToolsButNoTests`.
- **Run `bash flow/lib/test.sh` after changing anything under `flow/`.** It runs the tools' unit tests and the agent lint; the Go suite runs neither. **Enforced** in CI by `.github/workflows/flow.yml`, on changes under `flow/**`.
- **Keep client repos' names out of `flow/`: a word that names one goes in the owner's private `~/.loom/flow/banned-tokens.txt`, which `check-agents.sh` reads when it exists.** Loom's repo is public, and the flow serves every repo. **Convention** for what goes in the private list; **Enforced** for the shipped list's words by `check-agents.sh`.
- **Write a copy only into a dir loom owns that doesn't exist yet (`Write` refuses one that does), and remove only its own `<dir>.partial`.** **Enforced** by `TestWrite_RefusesADirThatExistsAndLeavesIt`.

## Pointers

- [`../session/CLAUDE.md`](../session/CLAUDE.md) — the launch that writes the copy (`prepareFlow`).
- [`../internal/cli/CLAUDE.md`](../internal/cli/CLAUDE.md) — `loom flow` and the guard.
- [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) — where this package sits.
```

`flow/README.md` (facts):
- what flow is (one paragraph);
- the layout (`plugin/`, `lib/`, the files `Write` makes: `plugin/`, `lib/` without tests, `ROSTER`);
- the names (`/flow:*`, `flow:<agent>`, `flow:habits`);
- the tools table from spec §5.5 with amendment 6;
- the environment (`LOOM_FLOW_ROOT`, `FLOW_ROOT`, `FLOW_DATA`, `FLOW_ROSTER`);
- where data lives (`.claude/flow.toml` per repo, `~/.loom/flow/records/`, the private banned list);
- the provenance line: "Moved from the private claude-flow repo at d937675 on 2026-11-05; its history, evidence and pin provenance before the move stay there."

`flow/PROVENANCE.md`: a title, then "Pin changes from the move into loom on (2026-11-05). Each entry: the date, the agent, the field, old → new, and the owner's instruction. Earlier history: claude-flow's `docs/PROVENANCE.md` (archived).", then an empty list.

`internal/cli/CLAUDE.md`:
- Add `loom flow` (`FlowCmd` in `flow.go`) to the header's command list.
- Add one rule: "**Run flow's tools from the launch's flow root (`LOOM_FLOW_ROOT`) and refuse one that isn't (125); outside a session, from a temp copy of this build's. Let `loom flow guard` block (2) a call it covers but can't check, and pass every other call.** A tool from another copy than its prompts' misreads them, and Claude reads a hook's exit 1 as allow, so a crashed guard would let a developer's `git stash` through. **Enforced** by `TestFlowTool_RefusesARootThatIsNotOne`, `TestFlowGuard_FailsClosedForAGuardedAgent` and `TestFlowGuard_ChecksOnlyTheCallsAGuardedAgentsModeCovers`."

- [ ] **Step 11: Run the CLAUDE.md lint.** Run `go run ./tools/claudemd -v 2>&1 | tail -5`. Expected: no failures, and every budget under its cap.

- [ ] **Step 12: Commit.**

```bash
git add -- flow/embed.go flow/embed_test.go flow/CLAUDE.md flow/README.md flow/PROVENANCE.md internal/cli/flow.go internal/cli/flow_test.go internal/cli/CLAUDE.md main.go
git commit -m "feat(flow,cli): embed the flow tree and add loom flow, its guard, path and import" -- flow/embed.go flow/embed_test.go flow/CLAUDE.md flow/README.md flow/PROVENANCE.md internal/cli/flow.go internal/cli/flow_test.go internal/cli/CLAUDE.md main.go
```

---

## P4 — Each launch gets flow

**Files:**
- Create `session/claude_flow.go`, `session/claude_flow_test.go`, `session/loom_context_flow.md`.
- Modify:
  - `session/hooks/hooks.go`, `session/hooks/hooks_test.go`;
  - `session/subagent_hooks.go`, `session/instance.go`;
  - `session/loom_context.go`, `session/loom_context_test.go` and the `WriteLoomContextFiles` callers;
  - `config/settings.go`;
  - `core/load.go`, `core/settings.go`;
  - `.github/workflows/build.yml`;
  - `docs/ARCHITECTURE.md`, `USAGE.md`;
  - `session/CLAUDE.md`, `session/hooks/CLAUDE.md`.

- [ ] **Step 1: The setting.** In `config/settings.go`, after `ClaudeSubagentTracking`, add:

```go
	// Flow controls whether new Claude sessions get the agent flow: the
	// `flow` plugin, its git guard hook and its context fragment (see
	// session.SetFlowEnabled). nil is treated as enabled (read via
	// FlowEnabled), matching ClaudeLoomContext.
	Flow *bool `json:"flow,omitempty"`
```

  In `Clone`, beside `out.ClaudeSubagentTracking = clonePtr(…)`, add `out.Flow = clonePtr(s.Flow)`. Beside `SubagentTrackingEnabled`, add:

```go
// FlowEnabled reports whether new Claude sessions get the agent flow. nil
// (unset) is treated as enabled, mirroring LoomContextEnabled.
func (s Settings) FlowEnabled() bool { return s.Flow == nil || *s.Flow }
```

  Test, in `config/settings_test.go` beside the other toggles' tests:

```go
func TestSettings_FlowEnabledDefaultsOnAndCloneCopiesIt(t *testing.T) {
	assert.True(t, Settings{}.FlowEnabled())
	off := false
	s := Settings{Flow: &off}
	assert.False(t, s.FlowEnabled())
	c := s.Clone()
	*s.Flow = true
	assert.False(t, c.FlowEnabled(), "Clone deep-copies the pointer")
}
```

- [ ] **Step 2: The guard entry.** In `session/hooks/hooks.go`:
  - Add `Timeout int `json:"timeout,omitempty"`` to `hookCommand`.
  - Add `const eventPreToolUse = "PreToolUse"`, `const guardMatcher = "Bash|Write|Edit"` and `const guardTimeout = 10`.
  - Change `SettingsJSON(eventsDir, autoMemoryDir, briefPath string)` to `SettingsJSON(eventsDir, autoMemoryDir, briefPath, guardCommand string)`, adding before the encode:

```go
	if guardCommand != "" {
		doc.Hooks[eventPreToolUse] = append(doc.Hooks[eventPreToolUse], hookMatcher{
			Matcher: guardMatcher,
			Hooks:   []hookCommand{{Type: "command", Command: guardCommand, Timeout: guardTimeout}},
		})
	}
```

  - Extend its comment: "A non-empty guardCommand adds a PreToolUse entry on Bash, Write and Edit running it: loom's git guard for flow's agents (`loom flow guard`), the one hook here that may exit non-zero, to block a call."
  - Rename `Prepare`'s body into `PrepareGuarded(dir, autoMemoryDir, brief, guardCommand string) (string, error)`, which passes `guardCommand` to `SettingsJSON`. Keep `func Prepare(dir, autoMemoryDir, brief string) (string, error) { return PrepareGuarded(dir, autoMemoryDir, brief, "") }`.
  - In `hooks_test.go`, every `SettingsJSON(a, b, c)` call becomes `SettingsJSON(a, b, c, "")`, and add:

```go
func TestSettingsJSON_GuardsFlowsAgentsOnlyWhenAsked(t *testing.T) {
	without, err := SettingsJSON("/e", "", "", "")
	require.NoError(t, err)
	assert.NotContains(t, string(without), "PreToolUse")

	with, err := SettingsJSON("/e", "", "", "'/bin/loom' flow guard")
	require.NoError(t, err)
	var doc settingsDoc
	require.NoError(t, json.Unmarshal(with, &doc))
	require.Len(t, doc.Hooks["PreToolUse"], 1)
	entry := doc.Hooks["PreToolUse"][0]
	assert.Equal(t, "Bash|Write|Edit", entry.Matcher)
	assert.Equal(t, []hookCommand{{Type: "command", Command: "'/bin/loom' flow guard", Timeout: 10}}, entry.Hooks)
	for _, name := range HookEvents {
		assert.Empty(t, doc.Hooks[name][0].Matcher, "the recorder's entries keep no matcher: %s", name)
	}
}
```

  Use the file's own imports. Add `encoding/json` if it lacks it.

- [ ] **Step 3: The session's tests.** `session/claude_flow_test.go`:

```go
package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aidan-bailey/loom/flow"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withFlow(t *testing.T, inst *Instance, exe string) {
	t.Helper()
	SetFlowEnabled(inst.ConfigDir, true)
	old := flowExecutable
	flowExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { SetFlowEnabled(inst.ConfigDir, false); flowExecutable = old })
}

func TestLaunchProgram_GivesAClaudeLaunchFlowBesideTheMod(t *testing.T) {
	withModSocket(t, "/run/loom/x.mod.sock")
	inst := hooksInstance(t, "claude")
	withFlow(t, inst, "/opt/loom/bin/loom")

	got, env := inst.launchCommand(LaunchEnv{Program: "claude"}, true)

	dir := SubagentHooksDir(inst.ConfigDir, inst.Title)
	root := filepath.Join(dir, "flow")
	assert.Contains(t, got, "--plugin-dir '"+filepath.Join(root, "plugin")+"'")
	assert.Contains(t, got, "--plugin-dir '"+filepath.Join(dir, "mod")+"'", "the mod's plugin dir stays")
	assert.True(t, flow.IsRoot(root))
	assert.Contains(t, env, flow.EnvRoot+"="+root)
	settings, err := os.ReadFile(hooks.SettingsPath(dir))
	require.NoError(t, err)
	assert.Contains(t, string(settings), `"command": "'/opt/loom/bin/loom' flow guard"`)
}

func TestLaunchProgram_NoFlowWhenTheSettingIsOff(t *testing.T) {
	inst := hooksInstance(t, "claude")

	got, env := inst.launchCommand(LaunchEnv{Program: "claude"}, true)

	dir := SubagentHooksDir(inst.ConfigDir, inst.Title)
	assert.NotContains(t, got, filepath.Join(dir, "flow"))
	assert.NoDirExists(t, filepath.Join(dir, "flow"))
	for _, kv := range env {
		assert.NotContains(t, kv, flow.EnvRoot)
	}
	settings, err := os.ReadFile(hooks.SettingsPath(dir))
	require.NoError(t, err)
	assert.NotContains(t, string(settings), "flow guard")
}

func TestLaunchProgram_NoFlowWhenLoomCantNameItsBinary(t *testing.T) {
	inst := hooksInstance(t, "claude")
	withFlow(t, inst, "/opt/it's/loom")

	got, _ := inst.launchCommand(LaunchEnv{Program: "claude"}, true)

	assert.NotContains(t, got, "flow/plugin", "no guard, so no flow agents")
	assert.Nil(t, inst.flowEnv())
}

func TestLaunchProgram_NoFlowWithoutHooks(t *testing.T) {
	for _, program := range []string{"aider", "claude --settings /mine.json"} {
		inst := hooksInstance(t, program)
		withFlow(t, inst, "/opt/loom/bin/loom")

		got, _ := inst.launchCommand(LaunchEnv{Program: program}, true)

		assert.NotContains(t, got, "flow/plugin", program)
		assert.Nil(t, inst.flowEnv(), program)
	}
}

func TestLaunchProgram_ARelaunchWithFlowOffDropsTheFlowEnv(t *testing.T) {
	inst := hooksInstance(t, "claude")
	withFlow(t, inst, "/opt/loom/bin/loom")
	inst.launchProgram(LaunchEnv{Program: "claude"}, true)
	require.NotNil(t, inst.flowEnv())

	SetFlowEnabled(inst.ConfigDir, false)
	inst.launchProgram(LaunchEnv{Program: "claude"}, true)

	assert.Nil(t, inst.flowEnv())
}
```

- [ ] **Step 4: Run them to see them fail.** Run `CGO_ENABLED=0 go test ./session/ -run 'Flow'`. Expected: a build failure.

- [ ] **Step 5: Write `session/claude_flow.go`.**

```go
package session

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/aidan-bailey/loom/flow"
	"github.com/aidan-bailey/loom/session/hooks"
)

// flowOn mirrors config.FlowEnabled() per config dir; the model sets it
// beside loomContextOn (core's syncSessionFlags). A dir never set reads as
// off, so a test's launch gets flow only when it asks.
var flowOn dirFlags

// SetFlowEnabled sets the flow toggle of the sessions whose config dir is
// configDir.
func SetFlowEnabled(configDir string, enabled bool) { flowOn.set(configDir, enabled) }

// FlowEnabled reports configDir's flow toggle.
func FlowEnabled(configDir string) bool { return flowOn.get(configDir, false) }

// flowExecutable is the loom binary the guard hook runs; tests replace it.
var flowExecutable = os.Executable

// flowDirOf is where a launch's flow root lives in its hooks folder.
func flowDirOf(hooksDir string) string { return filepath.Join(hooksDir, "flow") }

// flowGuardCommand is the PreToolUse command that runs loom's git guard
// for flow's guarded agents, or "" when this launch gets no flow: the
// setting is off for its config dir, or loom can't name its own binary in
// a command (unknown, or holding a single quote). Without the guard no
// launch gets flow's plugin, so its agents never run unguarded.
func (i *Instance) flowGuardCommand() string {
	if !FlowEnabled(i.ConfigDir) {
		return ""
	}
	exe, err := flowExecutable()
	if err != nil || !hooks.SafePath(exe) {
		i.getLogger().Warn("flow.skipped", "reason", "loom's own binary can't be named in a hook command")
		return ""
	}
	return "'" + exe + "' flow guard"
}

// prepareFlow writes flow into the launch's hooks folder (dir, which
// hooks.Prepare just emptied) and returns the environment naming it, or
// nil when the write failed: the launch then goes without flow's plugin.
func (i *Instance) prepareFlow(dir string) []string {
	root := flowDirOf(dir)
	if err := flow.Write(root); err != nil {
		i.getLogger().Warn("flow.prepare_failed", "err", err.Error())
		return nil
	}
	return []string{flow.EnvRoot + "=" + root}
}

// flowEnv is the environment naming the current launch's flow root, nil
// when the launch got no flow. launchCommand appends it after modEnv.
func (i *Instance) flowEnv() []string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return slices.Clone(i.flowEnvVars)
}
```

  In `session/instance.go`, beside `modEnvVars []string`, add:

```go
	// flowEnvVars is the current launch's flow environment (prepareFlow),
	// nil when it got no flow.
	flowEnvVars []string
```

  In `session/subagent_hooks.go`:
  - `resetHookLaunch` sets `i.flowEnvVars = nil` beside `i.modEnvVars = nil`.
  - `launchCommand` returns `program, append(append(InstanceEnv(env), i.modEnv()...), i.flowEnv()...)`.
  - `prepareHooks`:
    - Before `hooks.Prepare`, add `guard := i.flowGuardCommand()`, and the call becomes `hooks.PrepareGuarded(dir, memoryDir, brief, guard)`.
    - After `modEnv := i.prepareMod(dir, launchID)`, add:

```go
	var flowEnv []string
	if guard != "" {
		flowEnv = i.prepareFlow(dir)
	}
```

    - Under the lock, add `i.flowEnvVars = flowEnv`.
    - After the mod's `ApplyClaudePluginDir`, add:

```go
	if flowEnv != nil {
		program = agent.ApplyClaudePluginDir(program, filepath.Join(flowDirOf(dir), "plugin"))
	}
```

    - Its comment gains: "When flow is on for the config dir, it also writes flow into the folder, adds its plugin dir and the guard hook (prepareFlow, flowGuardCommand)."

- [ ] **Step 6: Run the session's flow tests.** Run `CGO_ENABLED=0 go test ./session/ -run 'Flow|LaunchProgram|Mod'`. Expected: PASS, the mod's tests included (`modEnv` is unchanged).

- [ ] **Step 7: The context fragment.** Create `session/loom_context_flow.md`:

```markdown
## The agent flow

Loom ships the agent flow as the `flow` plugin: the skills `/flow:init`, `/flow:draft`, `/flow:supervise` and `/flow:retro`, and its pinned agents, dispatched as `flow:<name>` (`flow:verifier`, `flow:intermediate-dev`, …). In a repo where `loom flow conf status` names a profile and whose own CLAUDE.md defines no supervised option, `superpowers:writing-plans` asks for the execution option before it writes the plan, and adds a third: **3. Supervised**, "a skeleton now; `/clear`, then `/flow:draft <plan>`; `/clear`, then `/flow:supervise <plan>`: drafters write the stories, then a supervisor runs them story by story with the pinned roster, and escalates what the spec doesn't settle." For option 3 the design session moves every decision into the spec, writes only the skeleton, in the format of the skeleton section of flow's dispatch guide, and commits both by pathspec; it drafts nothing. A plan is drafted from its spec, never generated from a prototype of the code. `/flow:supervise` also runs a plan written by hand that `loom flow check-plan` passes. `/flow:init`, `/flow:draft`, `/flow:supervise` and `/flow:retro` are the owner's to type: asked to run one, say so, and never read its SKILL.md to follow it yourself. Flow's guides are files in this session's copy of the plugin: `loom flow path docs/dispatch.md` prints the dispatch guide's path.
```

  In `session/loom_context.go`:
  - Add `//go:embed loom_context_flow.md` with `loomContextFlow []byte` to the embedded block.
  - `loomContextFile(head []byte, withFlow bool)` appends `loomContextFlow` after the sessions fragment when `withFlow`.
  - `subagentContextFile(head []byte, withFlow bool)` appends it after the shared fragment when `withFlow`.
  - `WriteLoomContextFiles(configDir string, withFlow bool)` passes `withFlow` to both files.
  - `subagentBrief` passes `FlowEnabled(configDir)`.
  - Build each with `parts := [][]byte{…}; if withFlow { parts = append(parts, loomContextFlow) }; return bytes.Join(parts, []byte("\n"))`, and update their comments.

  Every `WriteLoomContextFiles(x)` call site from `grep -rn 'WriteLoomContextFiles(' --include='*.go' .` gains `, false`, except `core/load.go` (step 8). Add to `session/loom_context_test.go`:

```go
func TestLoomContext_TheFlowFragmentFollowsTheSetting(t *testing.T) {
	on, off := t.TempDir(), t.TempDir()
	require.NoError(t, WriteLoomContextFiles(on, true))
	require.NoError(t, WriteLoomContextFiles(off, false))
	for _, name := range []string{loomContextFileWorktree, loomContextFileWorkspace} {
		withFlow, err := os.ReadFile(filepath.Join(on, name))
		require.NoError(t, err)
		without, err := os.ReadFile(filepath.Join(off, name))
		require.NoError(t, err)
		assert.Contains(t, string(withFlow), "## The agent flow", name)
		assert.NotContains(t, string(without), "## The agent flow", name)
	}
	SetLoomContextEnabled(on, true)
	SetFlowEnabled(on, true)
	t.Cleanup(func() { SetLoomContextEnabled(on, false); SetFlowEnabled(on, false) })
	assert.Contains(t, subagentBrief("claude", on, false), "## The agent flow")
	SetFlowEnabled(on, false)
	assert.NotContains(t, subagentBrief("claude", on, false), "## The agent flow")
}
```

  Run `CGO_ENABLED=0 go test ./session/ -run LoomContext`. If `TestLoomContext_NamesRealSessionFlags` checks every `` `loom <cmd> …` `` span against the CLI's commands, add `cli.FlowCmd`, or the tree it builds, so `loom flow conf status`, `loom flow check-plan` and `loom flow path` resolve. It lives in `internal/cli/loom_context_test.go`. Its failure names the span it can't find.

- [ ] **Step 8: The model sets the flag and writes the variant.**
  - In `core/load.go`'s `syncSessionFlags`, add `session.SetFlowEnabled(cfgDir, cfg.FlowEnabled())`.
  - At the `session.WriteLoomContextFiles(cfgDir)` call (line 188), pass the workspace config's `FlowEnabled()`. Read the enclosing function for the `*config.Config` in scope, and keep the existing warning.
  - In `core/settings.go`'s `SaveSettings`, after `syncSessionFlags(ws.cfg, flagsDir)`, add:

```go
	// The context files carry the flow fragment only while flow is on, and
	// a launch reads them as it starts.
	if flagsDir != "" {
		if err := session.WriteLoomContextFiles(flagsDir, ws.cfg.FlowEnabled()); err != nil {
			log.For("core").Warn("loom_context.write_failed", "err", err.Error())
		}
	}
```

  Add `log` to the imports if `core/settings.go` lacks it. In `core/settings_test.go`, or the file holding `SaveSettings`' tests, add a test that saving `Flow: &off` turns `session.FlowEnabled(<ctx config dir>)` false and rewrites the worktree context file without `## The agent flow`. Build its workspace the way the neighbouring `SaveSettings` tests do.

- [ ] **Step 9: CI and docs.**
  - `.github/workflows/build.yml`: add `- 'flow/**'` to both `paths:` lists.
  - `docs/ARCHITECTURE.md`: a paragraph after the loom mod's, on the agent flow:
    - the `flow/` tree;
    - the per-launch copy at `<hooks folder>/flow` (`--plugin-dir <…>/flow/plugin`, `LOOM_FLOW_ROOT`);
    - the guard hook (`loom flow guard`, `lib/guarded-agents.txt`);
    - the context fragment and its `flow` setting;
    - data in `.claude/flow.toml` and `<global dir>/flow/records`.
  - `USAGE.md`:
    - a `loom flow` section listing the tools (amendment 6) and `import`;
    - the `flow` setting in the config reference beside `claude_loom_context`.
  - `session/CLAUDE.md`: amend the mod rule's sentence to "give a launch the loom mod and flow only with its hooks (`prepareMod`, `prepareFlow` inside `prepareHooks`), and flow only with its guard (`flowGuardCommand`)". Add `TestLaunchProgram_GivesAClaudeLaunchFlowBesideTheMod` and `TestLaunchProgram_NoFlowWhenLoomCantNameItsBinary` to its Enforced list. Check the budget: `go run ./tools/claudemd -v | grep '^session/CLAUDE'` must stay at or under 20000 bytes.
  - `session/hooks/CLAUDE.md`: the exit-0 rule gains "— except the git guard (`SettingsJSON`'s guardCommand, `loom flow guard`), which exits 2 to block a guarded agent's call, and still reads its whole input". Its Enforced list gains `TestSettingsJSON_GuardsFlowsAgentsOnlyWhenAsked`.

- [ ] **Step 10: Run the touched packages.** Run `CGO_ENABLED=0 go test ./config/ ./session/... ./core/ ./internal/cli/ ./flow/`. Expected: PASS. Run `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/') session/claude_flow.go session/claude_flow_test.go flow/*.go internal/cli/flow*.go`. Expected: no output.

- [ ] **Step 11: Commit.**

```bash
git add -- config session core .github/workflows/build.yml docs/ARCHITECTURE.md USAGE.md
git commit -m "feat(session,config,core): each Claude launch gets the flow plugin, its guard and its context" -- config session core .github/workflows/build.yml docs/ARCHITECTURE.md USAGE.md
```

---

## P5 — Gates and the handoff

- [ ] **Step 1: The full suite and race, as queued jobs.**

```bash
loom job submit -d "flow-in-loom: full suite" -- env CGO_ENABLED=0 go test ./...
loom job submit -d "flow-in-loom: race" -- env CC=clang CGO_ENABLED=1 go test -race -count=1 ./...
```

  Expected: both exit 0. A failure only in `core`'s log-counting tests while another session's suite runs is the shared `/tmp/loom.log` trap (`docs/claude/testing-pitfalls.md`): rerun `core` alone.

- [ ] **Step 2: Quick checks.**
  - `CGO_ENABLED=0 go vet ./...`
  - `GOOS=windows CGO_ENABLED=0 go build ./...`
  - `gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')`
  - `go run ./tools/claudemd`
  - `bash flow/lib/test.sh`

  Expected: all clean. golangci-lint is skipped (the local v2 rejects the config): say so in the report.

- [ ] **Step 3: e2e** (the launch changed). Run `loom job submit -d "flow-in-loom: e2e" -- env CGO_ENABLED=0 go test -tags e2e ./e2e/...`. Expected: PASS.

- [ ] **Step 4: The real plugin against real Claude.** Rerun P1's probe (`LOOM_TEST_REAL_CLAUDE=1 … -run TestRealClaude_FlowPluginContract`), then run a dev build through the `loom-dev` skill.
  - In a sandbox session, check that `/flow:` lists the four skills.
  - Check that `loom flow conf status` inside its pane names the sandbox repo's key.
  - Check that `loom flow path docs/dispatch.md` prints a file under the session's hooks folder.

  Record what was seen in Execution notes.

- [ ] **Step 5: Hand off.** Report to the owner:
  - the commits;
  - the gates;
  - the probe's agent type;
  - the cutover steps that are theirs (spec §5.8):
    1. When no supervised run uses the home copy, run `install.sh --uninstall`, then once more with `CLAUDE_CONFIG_DIR` set to each account's config dir.
    2. Run `loom flow import ~/.claude/flow`.
    3. Copy each profile to its repo's `.claude/flow.toml` and commit it there.
    4. Write `~/.loom/flow/banned-tokens.txt` with the client names (`prader`, `polymarket`, `polyoxide`).
    5. Archive claude-flow.
  - Merging and pushing are the owner's call.

---

## Execution notes

(Filled in as packages land: P1's agent type, deviations, the gates' results.)
