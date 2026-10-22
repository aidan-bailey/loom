# Slash commands from the phone and the CLI

**Date:** 2026-10-22
**Status:** Approved design. Brainstormed with the user.

## Problem

The PWA's reply box and `loom session send` hand their text to Claude
through the loom mod, which submits it with `$.prompt.submit`. That call
submits a prompt "the model reads as a user turn": a slash command runs
only when a person types it or a plugin calls `$.command.run`. So
`/compact` sent from the phone reaches Claude as the text "/compact", and
nothing away from the desk can compact, clear, switch the model or start a
skill.

## Findings

From Claude Code 2.1.306's mod API (the plugin-authoring skill's
`claude-code.d.ts`), and the mod spec (`2026-10-14-loom-claude-mod-design.md`):

- `$.command.run({ command, args })` runs a slash command "as if the person
  typed `/command args`", queued and run once the session is idle, and
  resolves to `{ text?, context? }`: the command's printed output, absent
  for a panel or a prompt for the model. It rejects an unknown name.
- `$.command.list()` returns the commands the person can run now, each with
  `source`: `builtin`, `plugin`, `user` or `mcp`. A skill is `plugin` or
  `user`; an MCP server's prompt is `mcp`.
- `$.prompt.submit` runs no command; `command.run` fires only for a typed
  command or a plugin's `$.command.run`.
- The mod socket decodes JSON leniently, so a field an older daemon or mod
  doesn't know is ignored. An older mod handed a command message would
  submit its text as a prompt.

## Decisions

| # | Decision |
|---|---|
| 1 | **What runs:** three built-ins that finish on their own (`/compact [instructions]`, `/clear`, `/model <name>`), and every command the session's Claude lists with a source other than `builtin`: skills, plugin, user and MCP commands, which run as a model turn. |
| 2 | **Callers:** the phone's reply box (`OriginWeb`) and `loom session send` (`OriginCLI`). Lua's `send_prompt` and every other origin keep sending text as a prompt. |
| 3 | **Policy in Go:** the mod reports Claude's command list in its hello; the model parses the reply and decides; the mod only runs what it is handed. A rule of `session/claudemod`: the module is a carrier. |
| 4 | **Fail closed:** a name the session doesn't list, a built-in off the allowlist, and a session whose mod sent no list are refused with the reason, never sent as text. |
| 5 | **No protocol bump:** the hello gains `commands` and an inbox message gains `command` and `args`; the model never hands a command to a mod whose hello had no `commands`. |
| 6 | **The result:** the request answers once the command is queued, as a reply does; its output then arrives as a notice of that request: on the phone's stream (kept among its recent notices), and printed by `loom session send --wait`. |

## Design

### What counts as a command

After trimming, a reply is a command when its first word is `/` and a name
of 1 to 128 characters from letters, digits, `_`, `-` and `:` (a plugin's
commands are `plugin:name`). The name is the word without its slash; the
arguments are the rest, trimmed. A first word holding a second `/`
(`/tmp/x is full`) is a message, so a reply starting with a path still
reaches Claude as text.

### The rules

The model checks a command, in this order, and refuses with the reason:
- A command from another origin than `OriginWeb` or `OriginCLI` is not one:
  the text goes as before.
- With `From` set (`loom session send --from`): "a slash command takes no
  --from".
- The session's mod sent no command list (a mod from before this change):
  "this session's loom mod predates slash commands: pause and resume the
  session to update it".
- `compact` takes any arguments; `clear` none ("/clear takes no
  arguments"); `model` needs one ("/model needs a model name: a bare /model
  opens a menu nobody can answer").
- Any other name the list gives as `builtin`: "/<name> is a built-in loom
  doesn't run from a reply: it may open a menu or end the session".
- A name the list doesn't have: "this session's Claude has no /<name>
  command". Aliases are refused here, since the list names none.
- Otherwise the name has a source other than `builtin`, and runs with its
  arguments.

The other rules of a reply stay: a live mod, Claude as the program, not
busy, at most 64 KiB.

### Protocol

- `ModHello.Commands`: `[{name, source}]`, from `$.command.list()`. Absent
  from an older mod. The model keeps the list with the mod's link and drops
  it with the link. At most 2,000 entries; a name that fails the pattern
  above, or a source outside the four, is dropped.
- `ModMessage.Command` and `ModMessage.Args`: set for a command, whose
  `Text` stays the whole reply for the log.
- The mod runs a message with `Command` set through `$.command.run({
  command, args })`, reports `queued` as it hands it over, then `complete`
  with the output as `text` (empty when the command printed none), or
  `failed` with the error. A command reports no `started`. One that fails
  is never pasted into the pane: the model raises the error as the
  request's notice.

### What the person sees

- The request answers as a reply does once the command is queued (200 on
  the phone; `loom session send` returns), or with the refusal (409 on the
  phone, an error from the CLI).
- On `complete` the model raises a notice for the request: `/<name>:
  <output>`, the output cut at 2,000 characters with "…", or `Ran /<name>.`
  when it printed nothing. On `failed`, an error notice: `/<name> failed:
  <error>`. A notice tied to a request reaches the phone that asked
  (`internal/web` keeps it among the recent notices) or the CLI that waits.
- `loom session send --wait` prints the command's output. When a command
  starts a model turn (a skill), the turn's answer shows as the session's
  last message, as any turn's does; whether `--wait` waits for it is what
  the spike settles.
- The reply box's placeholder names the commands: "Reply, or /compact,
  /clear, /model <name>, /<skill>".

### Changes

- **core:** the parser and the rules (beside `OriginWeb`'s in
  `core/mod.go`), the hello's list on the mod link, the command fields of
  the outbox, the result notices, no paste for a command.
- **session/claudemod:** the module's hello lists the commands, and it runs
  a command message through `$.command.run`. Its allowed reach gains
  `$.command`; the package rule and the opt-in validator test's pinned call
  list change with it.
- **internal/web:** the reply box's placeholder; the rest is the model's.
- **Docs:** `USAGE.md` (the phone's reply box and `session send`), the
  `session/claudemod` and `core` READMEs, and the PWA spec's pointer here.

## Limits

- A command name: 1 to 128 characters. The hello's list: at most 2,000
  entries.
- A command the session gains after its mod's hello (an MCP server that
  connects late, a plugin installed mid-session) is refused until the mod
  says hello again (a pause and resume).

## Testing

- Go, over the production path: the parser (commands, messages, paths);
  each rule's refusal and the case it allows; an older mod refused; another
  origin's `/text` sent as a prompt; the outbox carrying `Command` and
  `Args`; `complete` raising the output notice and `failed` the error
  notice, with nothing pasted; the hello's list bounded; the phone's send
  answering 409 with the rule's text.
- The opt-in real-Claude test (`LOOM_TEST_REAL_CLAUDE=1`): the hello lists
  commands with their sources; `/model haiku` completes with output; an
  unknown name fails.

## Spike, as a gate

Before the code, a throwaway mod on a private tmux server, against real
Claude (haiku):
1. `$.command.list()`: the sources it gives, and whether aliases (`quit`,
   `reset`, `new`) appear.
2. `/model haiku`, `/compact` and `/clear` through `$.command.run`: what
   each resolves to, and how long it takes.
3. A project command (`.claude/commands/hello.md`): whether its run
   resolves before or after the turn it starts ends.
4. An unknown name: that the run rejects, and with what.

If aliases are listed as non-built-ins, the rules match on the list's
source only after resolving them, or refuse them by name. If a project
command's run resolves before its turn ends, `--wait` waits for the command
only, and the docs say so.

## Out of scope

- Commands that need a person: pickers, `/exit`, `/login`, `/resume`.
- Running a command typed into a pane-paste path (the prompt overlay, the
  quick bar): those already type into the pane.
- Refreshing the command list without a new hello.
