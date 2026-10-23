# Loom PWA: the conversation and the diff

**Date:** 2026-10-23
**Status:** Approved design. Brainstormed with the user. Sub-project 2 of
the PWA upgrade (`2026-10-23-pwa-skin-glance-design.md` lists all three).
The mockups are on a private page, in its third row:
https://claude.ai/artifact/Dv1GwHHRw8cJefivtMGvyh

## Problem

The phone shows a session's last message and its diff counts, but not what
Claude did on the way there or what it changed. Away from the desk, a reply
or a merge goes out on the strength of one message: there is no way to see
the prompts that led to it, the commands Claude ran, or the lines it wrote.

This spec adds two screens to the app, the conversation and the changes,
and two ways into them from the session screen.

## Where this sits

| # | Sub-project | Holds |
|---|---|---|
| 1 | Skin and glance (built, local main `9991f303`) | The skin, usage, subagents, PR links, the last message as markdown, quick replies, the workspace chips |
| 2 | **The conversation and the diff** (this spec) | The session's recent turns from Claude's transcript; the worktree's diff, file by file |
| 3 | Act more and notifications | Recover, resume with another model or account, a session from an issue, quiet at the desk, mute, an away digest |

Two departures from sub-project 1's outline: the line under a session's
title stays empty (no current tool; Decision 2), and the conversation opens
from the LAST MESSAGE rule rather than an "earlier turns" link above it.

## Findings

- **The transcript path is known.** A parent `SessionStart` stores Claude's
  session ID and `transcript_path` on the instance (`claudeState`, persisted
  since schema v7), and `Instance.ClaudeSession()` returns both. The file is
  `<config dir>/projects/<encoded cwd>/<session ID>.jsonl`, and Claude only
  appends to it, so a byte offset names a record for good. This session's
  is 14.5 MB in 4,546 lines, the longest line 428 KB.
- **Record shapes**, from a census of the 40 newest transcripts of this
  repository's sessions (Claude 2.1.288 to 2.1.306), counting shapes only:
  - Most lines are not conversation: `attachment` (23,206), `last-prompt`,
    `mode`, `ai-title`, `bridge-session`, `api-request`, `queue-operation`
    and more. Only `user`, `assistant` and `system` records matter.
  - An `assistant` record holds one content block (12,134 of 12,137), and
    the blocks of one message share its `message.id`. Of 4,432 `thinking`
    blocks, 3,810 are empty (a signature only). An API error is an
    assistant text record marked `isApiErrorMessage` (38).
  - Claude writes a `tool_use` record before the tool runs and its
    `tool_result` (a `user` record, `is_error` on failure) after.
  - A human prompt carries `promptSource` `typed`, `suggestion_accepted`,
    `queued` or `sdk` and `origin.kind` `human`; older records carry
    neither.
  - A slash command is a `user` record whose text starts `<command-name>`
    or `<command-message>` (a skill), with `<command-args>`. A manual
    `/compact` is written twice: a plain `/compact` record before the
    `compact_boundary`, then a `<command-name>/compact` record after the
    compact summary. A transcript after `/clear` opens with a meta caveat, a
    `<command-name>/clear` record and a `system` `local_command` record. A
    command run at launch (`/remote-control` in the census) leaves only
    `system` `local_command` records.
  - A message from another Claude session (`SendMessage`, mostly a
    subagent's handback) is a meta `user` record with `origin.kind` `peer`,
    `origin.name` the sender's agent name (`drafter`, `recon`, `Explore`,
    or empty) and `origin.body` the message; its text frames the body as
    "Another Claude session sent a message: …". Older Claude wrote the
    framed text as a plain `user` record with no origin.
  - A background task's end is a `user` record with `origin.kind`
    `task-notification`, its text `<task-notification>` holding `<status>`
    and `<summary>`.
  - `system` subtypes: `stop_hook_summary`, `turn_duration`,
    `local_command`, `bridge_status`, `compact_boundary`, `away_summary`,
    `informational`. Claude also compacts on its own while idle: a
    boundary with no command before it.
  - Meta records (`isMeta`: skill bodies, caveats, system text) and compact
    summaries (`isCompactSummary`, `isVisibleInTranscriptOnly`) are not
    the conversation.
- **Loom's own messages are plugin prompts.** The mod delivers a message
  with `$.prompt.submit`, `asUser` for the phone, scripts and the first
  prompt and framed for a CLI send (`core/mod.go`). Claude's types say the
  record's origin is `{kind: 'plugin', name, asUser?}` and that the
  transcript still names the plugin. No census transcript holds one yet,
  so a real-Claude test pins it (Testing).
- **The diff the TUI shows is not parseable as it stands.** `GitWorktree.Diff()`
  runs `git diff <base>` with stderr folded into stdout and under the
  user's diff config, so a warning, `color.diff=always`, `diff.noprefix` or
  `diff.external` changes what comes back. `Instance.UpdateDiffStats` caches
  the content on the instance, and `InstanceView.Diff.Content` carries it
  to every client in `ViewsChanged`.
- **A request's reply reaches only its client.** `Reply` goes to the
  connection that asked (`forConn`), FetchIssue already answers a request
  with data (`Reply.Issue`, from a tracked job), and the wire's frames are
  newline-delimited JSON with no size cap.
- **`InstanceView.Program` tells a Claude session apart**, so the app's
  link to the conversation needs no new wire field.
- **The hooks pipeline left `PreToolUse` and `PostToolUse` out on purpose**
  (`2026-09-23-claude-hook-events-design.md`): they fire on every tool call,
  about 5 ms each, and `PostToolUse` writes the tool's whole output to disk.
- **The hook's tool summary is deliberately narrow:** `toolSummaryKeys` in
  `session/hooks/event.go` names six tools, cut to one line of 200
  characters by `capSummary`.

## Decisions

| # | Decision |
|---|---|
| 1 | **Conversation:** chat plus steps. Each turn shows its prompt, Claude's replies as markdown, and its tool calls as one-line steps folded under a count. No tool output. |
| 2 | **No current-tool line.** The newest turn's steps show what is running; no PreToolUse hook, and nothing new under the session's title. |
| 3 | **Through Core.** Two requests, `ReadTurns` and `ReadDiff`, each answered by a model job, the reply going to the requester alone. The web layer reads no file and runs no git. |
| 4 | **Turns are labelled by origin:** you (typed, a command, the phone's reply), loom (a framed CLI send), another session (a peer), a background task. |
| 5 | **Paging:** newest first, at most 10 turns or 256 KiB of text a page, cursors that are byte offsets. A refresh reads only from the newest turn the app holds. |
| 6 | **Diff:** the worktree against its base commit, file by file with hunks, at most 1 MiB of patch. Active worktree sessions only. |
| 7 | **Ways in:** `conversation ›` on the session screen's LAST MESSAGE rule, which now shows for every Claude session; the CHANGES tile. |
| 8 | **Refresh:** on open, on the stream's changes to the session, and every 5 s (conversation) or 10 s (changes) while the screen is open and the session is Running. |
| 9 | **Paused sessions:** the conversation reads (it is a file); the diff does not. |
| 10 | **Unchanged:** the stream's rules, the pushes, the actions, and sub-project 1's screens apart from the two ways in. |

## Architecture

Additive throughout: `Core` gains two methods, `Reply` two fields and the
app's JSON one session field and two routes. Adding methods and fields
needs no `rpc.Protocol` bump, but a regenerated `docs/specs/protocol.md`.
The methods follow `docs/claude/adding-a-core-method.md` and the routes
`docs/claude/web-layer.md`.

### Daemon side

- `session/transcript`, new: a pure reader (no tmux, UI or app imports).
  `Read(path, conversation string, q Query) (Page, error)` reads one page
  of the transcript at `path`, whose session ID is `conversation`.
- `session/hooks`: `capSummary` exported as `CapSummary`, the one cut both
  summaries use.
- `session/git`: `(*GitWorktree).Patch() (string, error)`;
  `SplitPatch(text string) []FilePatch`; the types `Patch`, `FilePatch`
  and `Hunk`.
- `session`: `(*Instance).ReadPatch() (git.Patch, error)`.
- `core`: `ReadTurns(id InstanceID, q transcript.Query, req ReqID)` and
  `ReadDiff(id InstanceID, req ReqID)`; the ops `opReadTurns` and
  `opReadDiff` in `precondition`; `Reply.Turns transcript.Page` and
  `Reply.Diff git.Patch`, values like `Reply.Issue`.

### Web layer (`internal/web`)

- Routes `GET /api/v1/sessions/{id}/turns` and
  `GET /api/v1/sessions/{id}/diff`, each a Core request waited on like
  the actions (`l.reqs.next`, `l.reqs.wait`).
- `sessionJSON` gains `claude`: `session.IsClaudeProgram(v.Program)`.
- `turnsJSON` and `patchJSON` (the name `diffJSON` is the counts').

### App (`internal/web/static`)

- `js/turns.mjs` (the conversation screen) and `js/changes.mjs` (the
  changes screen), new.
- `js/app.mjs`: the routes `#s/<id>/turns` and `#s/<id>/diff`.
- `js/api.mjs`: the two calls.
- `js/session.mjs`: the ways in; `actionsFor` gains `diff`.
- `app.css`: the screens' components, from sub-project 1's tokens.

## Reading the transcript

### Records

The reader decodes only `user`, `assistant` and `system` records, in this
order of rules; the first that matches decides.

| Record | Becomes |
|---|---|
| a line that is not JSON, or longer than 4 MiB | nothing; counted in `skipped` |
| a final line without its newline | nothing (Claude is writing it) |
| `isSidechain` | nothing |
| `system` `compact_boundary` | a `compacted` item on the current turn |
| any other `system` record, and every other type | nothing |
| an `assistant` text block | a `text` item, its first 64 KiB; with `isApiErrorMessage`, an error `note` of its first line instead |
| an `assistant` `tool_use` block | a step, added to the current `steps` item, or opening one when the last item is not a `steps` item |
| any other `assistant` block (`thinking` among them) | nothing |
| a `user` record with `tool_result` blocks | the state of each step it names (below) |
| a `user` record marked `isCompactSummary` or `isVisibleInTranscriptOnly` | nothing |
| a `user` text that is `[Request interrupted by user]` or `[Request interrupted by user for tool use]` | an `interrupted` note on the current turn |
| a `user` record with `origin.kind` `peer` | a turn from `peer`: `name` from `origin.name`, its text `origin.body` |
| a `user` record with `origin.kind` `plugin` | a turn from `you` when `origin.asUser`, else from `plugin` with `name` from `origin.name` |
| a `user` record with `origin.kind` `task-notification`, or text starting `<task-notification>` | a turn from `task`, its text `<status>: <summary>` |
| any other `user` record marked `isMeta` | nothing |
| a `user` text starting `<command-name>` or `<command-message>` | a turn from `you`, its text `/<name>` and the `<command-args>`, `command` set; or nothing when it repeats a `/compact` (below) |
| a `user` text starting `<bash-input>` | a turn from `you`, its text `! ` and the input, `command` set |
| a `user` text starting `<local-command-stdout>`, `<local-command-stderr>`, `<local-command-caveat>`, `<bash-stdout>` or `<bash-stderr>` | nothing |
| a `user` text starting `Another Claude session sent a message:` | a turn from `peer` with no name (older Claude's form) |
| any other `user` record | a turn from `you`: its text (a string, or its text blocks joined, an image block as `[image]`), `command` set when it is one line starting `/` and a letter |

The origin rules come before the meta rule because a peer's record is meta.
A turn's text is kept to its first 16 KiB. A turn holds every record from
its own to the next turn's.

**The `/compact` repeat.** When the current turn's text is `/compact` and
the turn holds a `compacted` item, a `<command-name>/compact` record that
comes before any other turn is the same command written again, and starts
no turn. A `/compact` record after a boundary in any other turn (an older
Claude that wrote it only once) starts one.

### Steps

- **Tool:** the block's `name`.
- **Summary:** the input field the table names, its first line cut to 200
  characters by `hooks.CapSummary`; a file path under the record's `cwd` is
  shown relative to it. Bash: `command`; Read, Edit, Write, MultiEdit:
  `file_path`; NotebookEdit: `notebook_path`; Grep, Glob: `pattern`;
  WebFetch: `url`; WebSearch, ToolSearch: `query`; Agent, Task:
  `description`; Skill: `skill`. Any other tool has none.
- **At:** the record's `timestamp`.
- **State:** `pending` until a result names the step's `id`; then `done`,
  `failed` when the result sets `is_error`, or `interrupted` when the
  result's whole text is `[Request interrupted by user for tool use]`.
- A `steps` item keeps its newest 200 steps and counts them all: `total`
  and `failed`.

### Pages

A page holds, oldest first, the turns the query asks for.

- **The newest page** (`before` and `after` both 0): reading backward from
  the end of the file as it was when the reader opened it.
- **An earlier page** (`before` set): reading backward from that offset.
- Reading stops once it holds 10 turns or 256 KiB of text (turn texts and
  text items), at the start of the file, or after 16 MiB read, whichever
  comes first. The page holds at least one turn, however large.
- **A refresh** (`after` set): the turns from the one at that offset to
  the end. It reads back from the end only as far as that offset, so a
  poll re-reads the turn in progress. Records at `after` that precede the
  first turn starting there (the offset was a partial turn's) form a
  partial turn again.
- A turn whose first record lies more than 16 MiB back is `partial`: no
  text, its `id` the first line read, and `earlier` continues it.
- `earlier` is the page's first turn's `id` when records lie before it,
  and 0 at the start of the file (no turn can start at 0 and have an
  earlier page). An `after` of 0 means none: a turn starting at offset 0
  refreshes as the newest page.
- **Reset:** the reply is the newest page with `reset` set when
  `q.Conversation` is set and is not the transcript's (a `/clear` or a
  `/resume`), when `before` or `after` lies past the end of the file, or
  when `after` is not reached within 16 MiB.

### The shapes

```go
type Query struct {
	Conversation string // the conversation the caller holds turns of; "" for none
	Before       int64  // > 0: the page before this offset
	After        int64  // > 0: the turns from this offset to the end
}

type Page struct {
	Conversation string // the transcript's session ID
	Turns        []Turn // oldest first
	Earlier      int64  // Before for the page before this one; 0 at the start
	Reset        bool   // the page replaces every turn the caller holds
	Skipped      int    // records that could not be read
}

type Turn struct {
	ID      int64     // the byte offset of its first record
	At      time.Time // its first record's timestamp
	From    string    // "you", "plugin", "peer" or "task"
	Name    string    // the plugin's or the peer's name
	Text    string    // the prompt, first 16 KiB
	Cut     bool      // Text was cut
	Command bool      // a slash command or a shell command
	Partial bool      // its start lies before what was read
	Items   []Item
}

type Item struct {
	Kind   string    // "text", "steps", "compacted" or "note"
	At     time.Time // its record's timestamp; steps: its first step's
	Text   string    // text: Claude's markdown, first 64 KiB; note: the note
	Cut    bool
	Error  bool      // note: an API error
	Steps  []Step    // steps: the newest 200
	Total  int       // steps: every step of the run
	Failed int       // steps: every failed step of the run
}

type Step struct {
	ID      string // the tool_use block's id
	Tool    string
	Summary string
	At      time.Time
	State   string // "pending", "done", "failed" or "interrupted"
}
```

### The file

`Read` refuses, with an error naming the path, a path that is not
absolute, whose base name is not `<conversation>.jsonl`, or that is not a
regular file once links are followed: a bad hook payload must not point it
at another file. It opens the file read-only, never writes it, and reads
nothing past the size it saw at open.

## The patch

- **`Patch()`** runs `ensureUntrackedStaged`, then
  `git --no-pager diff --no-color --no-ext-diff --src-prefix=a/ --dst-prefix=b/ <base>`,
  reading stdout alone (`runGitStdout`) within `gitTimeout`. A warning on
  stderr can't land in the patch, and a user's diff config can't change
  what the parser reads.
- **`SplitPatch`** splits it into files in git's order:
  - a file starts at each `diff --git` line;
  - status: `A` (`new file mode`), `D` (`deleted file mode`), `R`
    (`rename from`/`rename to`), `C` (`copy from`/`copy to`), else `M`;
    `Binary files … differ` sets `binary` and leaves no hunks;
  - paths come from the `---`/`+++`, rename and copy lines, with git's
    C-style quoting undone; from the `diff --git` line only when the file
    has none of those (binary, mode-only), where its two names are equal;
  - a hunk is its `@@` header and its lines, counted by the header's
    ranges, so an added line that reads `+++ x` stays a line; an empty line
    in a hunk is a blank context line (`diff.suppressBlankEmpty`), and
    `\ No newline at end of file` is kept;
  - `added` and `removed` are counted from the hunks, exactly.
- **Limits:** a line longer than 2,000 bytes is cut at a rune boundary and
  its index listed in its hunk's `cut`; a file of more than 5,000 lines
  keeps its counts and drops its hunks (`omitted: "lines"`); once the kept
  lines reach 1 MiB, every later file keeps its counts and drops its hunks
  (`omitted: "budget"`).
- **`Instance.ReadPatch()`** returns the patch with `Base` (the base
  commit's SHA), or an error when the instance has no worktree or no base
  commit. It never touches the cached diff stats (`setDiffStats`): the
  published view would carry the content to every TUI.

```go
type Patch struct {
	Base  string
	Files []FilePatch
}

type FilePatch struct {
	Path, OldPath  string
	Status         string // "A", "M", "D", "R" or "C"
	Binary         bool
	Added, Removed int
	Hunks          []Hunk
	Omitted        string // "", "lines" or "budget"
}

type Hunk struct {
	Header string   // the @@ line
	Lines  []string // each with its prefix: ' ', '+', '-' or '\'
	Cut    []int    // the indexes of the lines cut at 2,000 bytes
}
```

## The requests

- **`ReadTurns(id, q, req)`** answers `Reply.Turns`. `opReadTurns` refuses
  a workspace terminal and a session whose program is not Claude
  (`session.IsClaudeProgram(inst.Program())`). On the loop it reads
  `inst.ClaudeSession()`; with no transcript path the reply's error is
  "loom has not seen this session's transcript yet". Otherwise the job
  calls `transcript.Read`.
- **`ReadDiff(id, req)`** answers `Reply.Diff`. `opReadDiff` refuses a
  workspace terminal, a Loading or Deleting session, a Paused or
  Recoverable one, and one not started. The job calls `inst.ReadPatch()`.
- Both run `m.spawn(m.track(req, id, job))`, and `deliverTracked` copies
  the result into the `Reply`, as for `FetchIssue`. Each reply goes to the
  requesting connection alone.
- Neither logs turn text, prompts or patch content, here or in the web
  layer: only the request, its session and its result.

## API

| Change | Where |
|---|---|
| `GET /api/v1/sessions/{id}/turns?conversation=&before=&after=` | new route, behind `authed` |
| `GET /api/v1/sessions/{id}/diff` | new route, behind `authed` |
| `claude` on a session | snapshot; `sessions` event |

- `before` and `after` are non-negative integers and at most one is set;
  otherwise 400.
- An unknown session or a workspace terminal is 404; a refusal 409 with the
  model's text; a stopping daemon 503; no reply within `replyWait` (30 s)
  504.
- Each answer is logged like the message route's: device, action
  (`turns`, `diff`), session, result.
- A text item crosses as its tree (`buildTree`): `blocks`, and `truncated`
  when either the reader's 64 KiB cut or the tree's limits cut it. The
  text itself is not sent. Every other string is drawn as text.

```json
{
  "conversation": "2fba5511-…",
  "turns": [{
    "id": 81234, "at": "2026-10-23T12:20:04Z", "from": "you", "name": "",
    "text": "now make loom session send name the commands it accepts",
    "cut": false, "command": false, "partial": false,
    "items": [
      {"kind": "steps", "at": "…", "total": 5, "failed": 0,
       "steps": [{"tool": "Bash", "summary": "go test ./core/...", "at": "…", "state": "done"}]},
      {"kind": "text", "at": "…", "blocks": [], "truncated": false},
      {"kind": "compacted", "at": "…"},
      {"kind": "note", "at": "…", "text": "interrupted", "error": false}
    ]
  }],
  "earlier": 80112,
  "reset": false,
  "skipped": 0
}
```

`earlier` is `null` at the start of the file. A step's `id` stays in Go:
the reader needs it to settle states, and the app does not.

```json
{
  "base": "9e1f03a…",
  "files": [{
    "path": "core/mod_command.go", "old_path": "", "status": "M",
    "binary": false, "added": 64, "removed": 12, "omitted": "",
    "hunks": [{"header": "@@ -39,9 +39,16 @@ func commandRefusal(…", "lines": [" \tswitch {", "-\tif x {", "+\tcase y:"], "cut": []}]
  }]
}
```

`internal/web/README.md`'s API table, session JSON and limits list them.

## The conversation screen

`#s/<id>/turns`. Mockup: "Conversation, a turn in progress".

- **Header:** `‹ <title>` back to the session; "Conversation"; the session
  screen's sub-line (status glyph and age, workspace, branch).
- **Turns,** oldest first, opening scrolled to the bottom. `↑ earlier turns`
  above them while `earlier` is set: it loads the page before and keeps the
  scroll position.
- **A turn's opening:** a box whose label sits in its top border:
  `YOU · 12:20`, `LOOM · 12:20` (the plugin's name), `FROM DRAFTER · 12:20`
  (`FROM ANOTHER SESSION` with no name) or `BACKGROUND TASK · 12:20`. Its
  text is plain text, a command in the accent colour; past 12 lines it
  shows 12 and a `▸ N more lines` that opens the rest. A partial turn has
  no box but a dim "Its start is earlier."
- **Claude's side:** `CLAUDE · 12:09` (the time of the turn's last item),
  or `CLAUDE · ◐ WORKING` on the newest turn while the session is Running,
  then the items in order:
  - **text:** the tree, drawn by sub-project 1's `Markdown`;
  - **steps:** a fold, `▸ 14 steps` with `· 1 failed` in the error colour.
    Open, one row per step: a state glyph, the tool (a long name cut with
    an ellipsis), the summary on one line. `◐ running 1m` (accent) only
    for a pending step of the newest turn while the session is Running;
    any other pending step shows a dim `no result`. `×` marks a failed
    step (error colour) and an interrupted one (dim). With more than 200,
    the open fold starts with `N earlier steps not shown`. The newest
    turn's last fold starts open while the session is Running;
  - **compacted:** a rule, `COMPACTED 11:51`;
  - **note:** a dim line, `× interrupted`, or an API error's line in the
    error colour.
- **Skipped records:** a dim line at the top, "Some records could not be
  read.", once any reply has reported `skipped` above 0. (A refresh
  re-reads the same records, so the counts are not summed.)
- **The reply box** at the bottom: sub-project 1's component and rules.
- **Refresh** (one request in flight at a time): on open, the newest page;
  then with `after` set to the newest turn held, whenever the stream
  changes the session's `status` or `last_message`, and every 5 s while
  the screen is open and the session is Running. A refresh's turns replace
  every held turn from its first turn's `id` on. A `reset` page, from any
  request, replaces everything. At the bottom, the screen stays at the
  bottom as turns grow; scrolled up, it stays where it is.
- **Errors:** with nothing loaded, the error's text in place of the turns
  ("loom has not seen this session's transcript yet", a missing file).
  With turns loaded, they stay, under a dim "Could not refresh: <text> ·
  retry".

## The changes screen

`#s/<id>/diff`. Mockup: "Changes, one file open".

- **Header:** `‹ <title>`; "Changes"; the sub-line
  `8 files · +212 −40 · since 9e1f03a`: the reply's files, their summed
  counts, the base's first 7 characters.
- **One row per file,** at least 44 px tall: the status letter (`M` and `R`
  in `--accent`, `A` in `--ok`, `D` in `--error`, `C` in `--dim`), the
  file's name over its directory (a rename or copy: `from <old path>`),
  then `+N −N`, or `binary`.
- **Tapping a row** opens its hunks in a box that scrolls sideways on its
  own, lines unwrapped, tabs 4 wide: hunk headers on `--panel` with the
  `@@ … @@` in `--accent`; removed lines in `--error` on a new `--del`
  (`#3d2f2b`), added lines in `--ok` on a new `--add` (`#333a2b`); a cut
  line ends in a dim `…`. A file with no
  hunks says why: "Too long to show here (N lines)", "Not shown: the diff
  is over 1 MiB", "Binary file", or "No text changes" (a mode change, an
  empty file).
- Files start closed, a lone file open; a file stays open across refreshes
  (by path).
- **No changes:** "No changes since 9e1f03a."
- **Refresh** (one request in flight at a time): on open; when the stream
  changes the session's `diff` counts or `status`; every 10 s while the
  screen is open and the session is Running.
- **Errors:** with nothing loaded, the error's text and retry; with a diff
  loaded, it stays, under a dim "Could not refresh: <text> · retry".

## The session screen's ways in

Mockup: "Session, the ways in".

- **The LAST MESSAGE rule** shows for every session with `claude` set, the
  message under it or a dim "No message yet.", and ends in
  `conversation ›` (accent, 44 px tall), which opens `#s/<id>/turns`.
- **The CHANGES tile** opens `#s/<id>/diff`, with a chevron after its
  counts, when `actionsFor(s).diff`: not Loading or Deleting, and not in
  the paused group, the same as Send. Otherwise it is no link.

## Failure modes

- **No transcript known** (a launch without hooks, or before Claude's first
  `SessionStart`): the conversation screen says so. A program that is not
  Claude has no link.
- **A transcript path that fails the check, or a file gone** (Claude
  deletes a transcript a month after its last write): an error naming the
  path. The chat archive's copy is not read.
- **A line being written:** ignored until its newline lands.
- **An unreadable record:** skipped and counted. A result line skipped
  this way leaves its step pending, shown as `no result` once the turn is
  not the running one.
- **A Claude that crashed mid-tool:** its pending steps show `no result`.
- **A `/clear` or `/resume`:** the next reply carries `reset`, and the
  screen starts over on the new conversation.
- **A huge turn:** `partial`, and `earlier` reaches its start.
- **Git failing:** the error is git's own (`index.lock` held by the
  agent's own commit, as the TUI's probe meets today; the 8 s timeout; no
  base commit), with a retry.
- **A huge diff:** read into memory once, as the TUI's probe already does
  for a selected session, then cut to 1 MiB for the reply.
- **A slow daemon:** 504 after 30 s, shown like any other error.
- **Claude changing its transcript format:** records the reader no longer
  recognises drop out of the screen without an error. The real-Claude
  tests are the guard (below).

## Testing

- `session/transcript`, over synthetic JSONL written in the census's
  shapes (never a copied transcript):
  - each row of the records table, with both sides of the origin-before-
    meta order (a meta peer starts a turn; any other meta record does
    not);
  - the `/compact` repeat: skipped after a `/compact` turn's boundary, and
    a turn after a boundary in a turn that `/compact` did not start;
  - step summaries, the relative path, each state, and the 200-step keep
    with `total` and `failed`;
  - the 64 KiB and 16 KiB cuts at a rune boundary;
  - pages: 10 turns; 256 KiB with at least one turn; `earlier` at 0 with
    records before the first turn; `after` from a turn, from a partial
    turn's `id`, and not reached (reset); 16 MiB making a turn partial; a
    4 MiB line skipped and counted; a final line without its newline;
    `before` or `after` past the end (reset); another conversation (reset);
  - the file check: a relative path, another base name, a directory, a
    missing file.
- Opt-in, with `LOOM_TEST_REAL_CLAUDE=1`:
  - `TestRealClaude_TranscriptReader`, new: `claude -p` with a prompt that
    runs one Bash command, the transcript found by the session ID
    `--output-format json` reports, reads back as one `you` turn with the
    prompt, one `done` Bash step whose summary is the command, and a text
    item;
  - `TestRealClaude_TheModCarriesAMessage` (`internal/modserver`) sends
    its first message (`m1`) `asUser`, as the phone does, and keeps its
    second (`m2`) framed, as a CLI send is; then it reads its session's
    transcript, where `m1` is a turn from `you` and `m2` a turn from
    `plugin` named `loom`. No census transcript holds a plugin prompt, so
    this pins the shape the phone's replies take.
- `session/git`, with real git in a temp repo: each status, binary,
  mode-only, a rename, a copy (with `diff.renames=copies`), quoted paths (a
  space, a quote, non-ASCII), no newline at end of file, a blank context
  line under `diff.suppressBlankEmpty`, an added line reading `+++ x`, and
  each limit. `Patch()` in a repo whose config sets `color.diff=always`,
  `diff.noprefix=true` and a `diff.external` that fails still parses.
  `session`: `TestReadPatch_LeavesTheCachedDiffAlone`.
- `core`: the preconditions in `TestRequests_RefuseWhatTheTUIRefuses`, both
  sides of each; each reply reaching only its requester;
  `TestWire_EveryFieldSurvives` with the new fields set;
  `TestLoopForwardsEachMethodToItsNamesake`, `TestGenerated_IsFresh` and
  `TestProtocolReference` passing.
- `internal/web`, over the production path (a booted model, the real
  `rpc.Server`, a client over `net.Pipe`, `httptest`): both routes' JSON,
  400, 404, 409 and 504; `claude` on a session; a text item through
  `buildTree`; the action log holding no turn text or patch content.
  `node --check` on every module, and `TestStatic_ModulesSetNoStyleAttribute`
  passing.
- **Render:** headless Chromium at 390×844 against a canned server outside
  the worktree (`docs/claude/web-layer.md`): a turn in progress, a long
  fold, a compaction, an interruption, every turn label, a partial turn;
  a diff with each status, a cut line and both omissions.
- **On the iPhone,** through the `loom-dev` skill (never `./loom` or
  `loom serve stop` in a loom pane): a working session's conversation with
  a step finishing, earlier turns, a `/clear` starting over, the changes of
  a session with an untracked file, and a paused session (its
  conversation, no changes link).
- **Gates:** `CGO_ENABLED=0 go test ./...`; race on `session`, `core` and
  `internal/web`; `gofmt -l` and `go vet`; the CLAUDE.md lint;
  golangci-lint as the root `CLAUDE.md` says.

## Work packages

- **A. Transcript reader** (`session/transcript`, `session/hooks`): the
  reader and its tests, `TestRealClaude_TranscriptReader`,
  `hooks.CapSummary`; `session/README.md`'s facts; `session/CLAUDE.md`'s
  pure-reader rule naming `session/transcript`.
- **B. Patch** (`session/git`, `session`): `Patch`, `SplitPatch`, the
  types, `Instance.ReadPatch`, and their tests; `session/README.md`'s git
  facts; a `session/git/CLAUDE.md` rule to parse only `Patch`'s output,
  enforced by the config test.
- **C. Core requests** (`core`, `core/rpc`, `internal/modserver`): both
  methods by the checklist, the preconditions, the jobs, the `Reply`
  fields, the generated wire and `docs/specs/protocol.md`, their tests,
  and the mod test's transcript check.
- **D. Web and app** (`internal/web`): both routes, the JSON, `claude`, both
  screens, the ways in, the CSS; `internal/web/README.md`; the
  `internal/web/CLAUDE.md` rule on the last message's tree widened to the
  conversation's text items; the render check.

A and B in either order; C after both; D last. Check the CLAUDE.md budgets
(`go run ./tools/claudemd -v`) before adding a rule.

## Alternatives considered

- **The current tool:** a `PreToolUse` hook (about 5 ms on every tool call,
  a relaunch to take effect, and a published field that would re-send
  every workspace's views to every TUI on each call), the mod's `tool.call`
  (an early-access API), or the transcript's tail. Dropped at the user's
  suggestion: the newest turn's steps show what runs.
- **The conversation from the mod** (`$.session.messages()`): running
  sessions only, the newest 4,096 rows, an early-access API.
- **The web layer reading the transcript and running git itself:** the web
  would stop being only a Core client, which keeps the cloud federation
  open, and the model already knows the path and the worktree.
- **The diff through the selection** (`SetSelected`,
  `InstanceView.Diff.Content`): one selection per connection, and the
  content rides every client's views.
- **Turns pushed on the stream:** per-device subscriptions to a session;
  polling while a screen is open needs no new stream state.
- **Tool output in the steps:** the transcript's largest records, and the
  reason the hooks pipeline left `PostToolUse` out.

## Out of scope

- Tool output; a current-tool line; subagents' own transcripts.
- A transcript Claude has deleted, from the chat archive's copy.
- A Paused session's diff (its tree is kept since `ef20ae2`; it would need
  `InspectTree` first).
- A file past the 1 MiB budget on demand; syntax highlighting; word-level
  diffs; search.
- The TUI using either request.
- Sub-project 3.
