# feedback

The inbox loom's session agents write to ([spec](../docs/superpowers/specs/2026-11-05-session-feedback-design.md)). An agent that hits a loom problem (a failing or surprising `loom` command, a missing capability, a loom context sentence that isn't true) runs `loom feedback add`; the daemon stamps who filed it, a headless Haiku files it into a thread, and the owner reads the threads ranked by how many sessions hit each and starts a fix session from one. The model in `core` (`core/feedback*.go`) owns the inbox; this package is the logic it calls.

## Files

- `doc.go` — the package comment.
- `types.go` — `Kind` (`bug`, `friction`, `missing`, `docs`), `Note`, `Session` (who filed it, from the daemon's resolution of `$LOOM_LAUNCH`: instance ID, title, workspace, branch, subagent, Claude session and transcript), `Thread`, `Fix`, the `Grouping` and `Status` values, and `ValidateNote` (a kind of the four, a title of 1–120 characters on one line, a text of 1–20,000 bytes).
- `inbox.go` — `Inbox` (the file's shape) and its operations: `Add`, `Attach`, `OpenThread`, `Fail`, `Close`, `SetFix`, `Due` (the next note to group), `Offered` (threads not closed), `Sessions`, `Clone`.
- `rank.go` — `Row`, `Rank` (threads and loose notes by distinct sessions, then the latest note), `OpenThreads`.
- `store.go` — `Load` and `Save`.
- `group.go`, `prompt.md` — the grouper: `Request`/`BuildRequest`, `Schema`, `Answer`/`ParseAnswer`, `Apply`, `SystemPrompt`, and `Resolve` (the `feedback.grouper` block through `advisor.ResolveBlock`: on, `haiku`, `low`).
- `fix.go` — `FixTitle` (`fb-<T>-<slug>`, at most 32 cells) and `FixPrompt`.
- `calllog.go` — `CallRecord` and `AppendCall`.

## On disk

Under `<globalDir>/feedback/`:

- `feedback.json` — the inbox: version 1, the counters, every note and thread. Written atomically (0600, the dir 0700) by the model alone; a newer version, or a file that won't parse, is left untouched and the model refuses notes.
- `grouper.jsonl` — one line per grouper call: when, the note, the threads offered, the model and effort, the usage, the raw answer, where the note went or the error. Appended, never rewritten.
- `grouper/` — the empty folder the grouper's `claude -p` runs in, so its read-only tools reach nothing.

## A note's life

A filed note is `pending`. The model offers it to the grouper with every thread that isn't closed (each thread's last 20 notes, texts cut to 300 bytes); the answer names a thread, which the note joins (`grouped`), or 0 and a title, which opens a thread. A failed call, or an answer that doesn't parse, leaves the note `ungrouped`, retried 10 minutes later; three failures in a row pause the grouper until the daemon restarts or `feedback.grouper` changes. A disabled grouper leaves notes `pending`.

A thread is `open`, `fixing` once a fix session was started for it (`Fix` names the session), and `closed` when the owner closes it. A closed thread is never offered again, so a problem that recurs opens a new thread.

## The fix session

`FixPrompt` holds the thread's title and kind, every note's text, session, workspace, loom build and transcript, and opens with: check whether the base branch already fixes it (reporters may run an older daemon); if not, reproduce it and propose a fix before building it.
