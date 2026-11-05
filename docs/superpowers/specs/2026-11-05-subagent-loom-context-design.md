# Subagent loom context: brief every subagent through loom's `SubagentStart` hook

Status: implemented 2026-11-05 (plan: [`../plans/2026-11-05-subagent-loom-context.md`](../plans/2026-11-05-subagent-loom-context.md)).

Builds on [the loom-context revision](2026-11-04-loom-context-revision-design.md) and [subagent job results](2026-11-05-subagent-job-results-design.md) (branch `aidanb/loom-doesnt-know-subagents`, unmerged).

## 1. Problem

Loom hands each Claude session its context (worktree and branch rules, the shared tmux server, leaving the daemon alone, the work queue, other sessions) with `--append-system-prompt-file` (`loomContextProgram`, `session/loom_context.go`). Claude Code appends that file to the top-level agent's system prompt only. A subagent gets its own agent definition, plus the CLAUDE.md files for a general-purpose one. Forks are the one exception: they inherit the parent's whole context.

So a subagent doesn't know that heavy work goes through `loom job`, that it must commit by path, that scratch files stay out of the worktree, that the tmux server is shared, or that the daemon is off limits. None of the user's agent definitions (`~/.claude/agents`) mention any of it, and nothing tells the parent to pass it on.

Success:
- Every subagent of a session that got loom's context gets a subagent's cut of it before its first prompt: general-purpose, built-in, the user's own and plugin agents alike.
- A fork, which already has the parent's context, gets no second copy.
- A subagent resumed by `SendMessage` or after the parent's `--resume` holds one copy, not two.
- Nothing about the parent's prompt changes except the work-queue paragraph's wording (§5.2).

## 2. Decisions

- **Brief through a `SubagentStart` hook's `additionalContext`.** Claude Code puts it in a system reminder at the start of the subagent's conversation, the way CLAUDE.md reaches an agent, keeps it across resumes, and adds it again after auto-compaction drops it. Rejected: agent definitions, which are real system prompts but replace the whole prompt per type, so loom can't add a line to a built-in or to the user's agents; the mod's `agent.spawn` prompt rewrite, which mixes loom's text into the parent's brief; the mod's `prompt.compose`, whose input names no agent, so whether it fires for subagents is unknown.
- **A second command on loom's own hook settings, printing a file written at launch.** No daemon, no mod: it works whenever loom's hooks do. Rejected: the mod answering the classic event (depends on the daemon serving the mod, and the text needs no per-agent variation); folding the output into the recorder command (`HookCommand`), whose guarantee is that it always reads its input and exits 0.
- **A subagent cut composed from the parent's fragments** (the owner, 2026-11-05): a short subagent head, then the parent's head and "Loom itself". "Other sessions" stays the parent's: messaging and starting sessions is its job. Rejected: the parent's file verbatim (about 6.1 KB per spawn, with lines phrased for the parent); the work-queue paragraph alone (leaves out the guardrails).
- **Build on the job-routing branch** (the owner, 2026-11-05). This branch merges `aidanb/loom-doesnt-know-subagents` once its Package D (display, context and docs) lands, so the brief says from the start that a subagent's `loom job submit` reports back to it.
- **Skip forks with the matcher `^(?!fork$)`.** Claude Code evaluates a matcher holding any character beyond letters, digits, `_`, `-`, spaces, `,` and `|` as an unanchored JavaScript regex, so this matches every agent type but exactly `fork`.
- **Brief subagents exactly when the parent got loom's context flag.** The `claude_loom_context` toggle, a missing prompt file and a non-Claude program then decide both alike. (Loom adds its flag beside a user's own `--append-system-prompt-file`, so that one changes neither.)

## 3. Scope

In: the fragment split and the subagent head; the work-queue paragraph's wording; the subagent cut's composition; `hooks.Prepare` writing the brief and registering its entry; the launch gate; tests; the package rules that change.

Out: teammates and workflow agents (whether `SubagentStart` fires for them is unverified, §9); Windows and a user's own `--settings`, where loom installs no hooks at all; any change to the job-routing design.

## 4. What the probes established

Claude Code 2.1.318, 2026-11-05: `claude -p --settings` in a scratch dir outside the repo, with one `SubagentStart` hook that logged each payload and one that returned a numbered codeword as `additionalContext`, asked of each subagent with no tools allowed.

| Case | Hook fires | Text reaches the agent |
|---|---|---|
| New general-purpose subagent | yes | yes |
| New fork | yes (`agent_type: "fork"`) | yes |
| New fork, brief entry matched `^(?!fork$)` | the matcherless logger yes, the brief no | no |
| `SendMessage` to a finished subagent | yes, same `agent_id` | it keeps its first copy; the new output is not added |
| Same, after the parent's `claude --resume` | yes | same: its first copy, from its transcript, stands |
| New subagent after the parent's `--resume` | yes | yes, the new text |

The docs agree: on a later run for the same subagent, Claude Code injects the context only when the subagent's context doesn't already hold the copy from an earlier run, and injects again after auto-compaction discards it. A value over 10,000 characters is written to a file and replaced by its path and a 2,000-character preview.

## 5. Behaviour

### 5.1 The text

`session/loom_context_shared.md` splits in two, moving no words:
- `session/loom_context_shared.md`: "## Loom itself" (the shared tmux server, leaving the daemon alone, the work queue);
- `session/loom_context_sessions.md`: "## Other sessions".

The parent's prompt (`loomContextFile`) is its head, then the shared fragment, then the sessions fragment, which reads as today's file.

A new `session/loom_context_subagent.md` holds the subagent head, along these lines:

> # Loom context for a subagent
>
> You are a subagent of a Claude session that loom runs. That session's loom context follows, and its rules bind you as they bind it. Leave other loom sessions to your parent: don't message, start, pause or kill them; tell your parent when one should hear from it.

The subagent cut (`subagentContextFile`) is the subagent head, then the parent's head (the worktree or the workspace variant, chosen as the parent's is, by `IsWorkspaceTerminal`), then the shared fragment. It comes to about 4 KB and must stay under 10,000 bytes, so Claude passes it inline.

### 5.2 The work-queue paragraph

After the merge, the paragraph in `session/loom_context_shared.md` reads correctly to the parent and to a subagent alike. The sentence the routing branch's §9 adds is replaced by one along these lines:

> Submit from a foreground Bash call: a job's `[loom] job N` message goes back to whoever submitted it, a subagent included, which gets it even after it has finished and so can end its turn; a result loom can't place goes to the session's main conversation, naming the subagent.

`TestJobContext_NamesOnlyFlagsTheCommandsHave` keeps asserting that the paragraph names subagents.

### 5.3 Delivery

`hooks.Prepare` takes the brief as a third argument. With a non-empty brief it:
- writes `subagent-context.json` into the hooks folder, holding `{"hookSpecificOutput":{"hookEventName":"SubagentStart","additionalContext":<brief>}}`, before it writes `settings.json`;
- registers a second `SubagentStart` entry in the settings, matcher `^(?!fork$)`, whose command is

  ```sh
  cat >/dev/null; cat '<hooks folder>/subagent-context.json' 2>/dev/null; exit 0
  ```

  It reads its whole input, as `HookCommand` does, so Claude never fails writing the payload, and it exits 0 with no output when the file is gone.

The recorder's entry for `SubagentStart` is unchanged and has no matcher, so the subagent tracker still sees forks. An empty brief writes no file and registers no entry. The hooks folder is per launch (`Prepare` empties it), so each launch writes its own brief from the running binary's embedded fragments; nothing new goes in the config dir.

### 5.4 The gate

`loomContextPath` answers whether a launch gets loom's prompt file; `loomContextProgram` adds the flag from it and `subagentBrief` the subagent cut of the same variant, so the two can't disagree. `launchProgram` passes the brief, or an empty one, to `prepareHooks`. `prepareHooks`' own conditions (a config dir, not Windows, a Claude program, no user `--settings`) still decide whether hooks are prepared at all.

## 6. Components

| File | Change |
|---|---|
| `session/loom_context_shared.md` | keeps "Loom itself"; the work-queue sentence of §5.2 |
| `session/loom_context_sessions.md` | new: "Other sessions", moved from the shared fragment |
| `session/loom_context_subagent.md` | new: the subagent head |
| `session/loom_context.go` | embeds the new fragments; `loomContextFile` joins three; `subagentContextFile`; `loomContextPath`, shared by `loomContextProgram` and `subagentBrief` |
| `session/subagent_hooks.go` | `launchProgram` picks the brief; `prepareHooks` passes it to `hooks.Prepare` |
| `session/hooks/hooks.go` | `Prepare` and `SettingsJSON` take the brief; the brief's file, command and matcher |
| `session/hooks/CLAUDE.md` | new: the brief's rule (§8) |
| `session/CLAUDE.md` | the loom-context rule names the fragments by pattern and covers the subagent head |

## 7. Failure handling

- **The brief can't be written:** `Prepare` fails as it does for any write in the hooks folder, and the session launches untracked, as today.
- **The file is missing when the hook runs** (a relaunch emptied the folder): the command prints nothing and exits 0; the subagent goes unbriefed, with no error.
- **Claude doesn't support the matcher or the field** (an older build): the subagent gets the brief twice at worst (a fork) or not at all; the recorder entry is untouched, so status and tracking don't change.

## 8. Testing

- `session`: `TestSubagentContext_IsTheParentsWithoutOtherSessions`. For both variants, the cut holds the subagent head, the parent's head and the shared fragment, and not the sessions fragment, while the parent's prompt holds all three.
- `session`: `TestSubagentContext_FitsClaudesInlineCap`. Both cuts are under 10,000 bytes.
- `session/hooks`: `TestSettingsJSON_BriefsSubagentsButNotForks`. With a brief, the `SubagentStart` list holds the recorder entry unchanged and a brief entry with matcher `^(?!fork$)`; with none, only the recorder.
- `session/hooks`: `TestBriefCommand_ReadsItsInputAndExitsZero`. Under `sh`, with the file present, it prints the file's bytes; with the file gone, it prints nothing; both exit 0 having consumed a large stdin.
- `session/hooks`: `TestPrepare_WritesTheSubagentBrief`. The file decodes to `hookEventName: "SubagentStart"` and `additionalContext` equal to the brief; an empty brief writes no file.
- `session`: `TestLaunchProgram_BriefsSubagentsOnlyWithTheLoomContext`. The toggle on gives the worktree cut in the hooks folder, off gives none, and a workspace terminal gets the workspace cut.
- `session` (opt-in, `LOOM_TEST_REAL_CLAUDE=1`): `TestRealClaude_SubagentBrief`. A real `claude -p` under `hooks.Prepare`'s settings, with a marker brief: a general-purpose subagent reports the marker, and the brief entry doesn't fire for a fork.
- `internal/cli`: `TestLoomContext_NamesRealSessionFlags` reads `session/loom_context_shared.md` by path, so it moves to the sessions fragment, which now holds the `loom session` text. `TestJobContext_NamesOnlyFlagsTheCommandsHave` reads the composed prompt files, so the split leaves it alone; the subagent cuts carry the same paragraph through the shared fragment, which `TestSubagentContext_IsTheParentsWithoutOtherSessions` pins.

The package rule (`session/hooks/CLAUDE.md`): **give subagents the brief only through the hooks settings, with the fork-excluding matcher, and only when the parent got loom's context.** A fork already holds the parent's prompt, so it would read loom's rules twice; and a user who turned the context off has turned it off for the session's subagents too. Enforced by the tests above. `session/CLAUDE.md` is 8 bytes under its 20,000-byte budget, so the rule goes in the new file, the loom-context rule refers to the `loom_context_*.md` fragments by pattern, and the `session/buildsweep` rule moves, verbatim, into a new `session/buildsweep/CLAUDE.md`, which loads when that package is edited.

## 9. Known limits and later work

- No brief where loom installs no hooks: Windows, and a launch whose program already passes `--settings`.
- Teammates and workflow agents: unverified whether `SubagentStart` fires for them, and so whether they are briefed.
- A subagent resumed after a loom upgrade keeps the brief of the launch it started under, since Claude adds no second copy.
- A subagent the loom mod resumes with `$.session.send` (a job's result, [subagent job results](2026-11-05-subagent-job-results-design.md) §13) runs as the plugin's own work, for which Claude Code 2.1.318 runs no loom hook. It still holds the brief from its first start, which a parent's Agent call made; only a compaction during that run drops the brief until its next ordinary run.
- `USAGE.md`'s config table names the toggle; it now says the toggle covers subagents too.
