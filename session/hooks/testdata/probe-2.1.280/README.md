# Probe payloads, Claude Code 2.1.280

Raw hook payloads from the live probe of 2026-09-23 (see
`docs/superpowers/specs/2026-09-23-claude-hook-events-design.md`, Findings and
Probe results). One interactive haiku session in `manual` permission mode on
a private tmux server, hooks registered for `SessionStart`,
`UserPromptSubmit`, `PermissionRequest`, `Notification`, `Stop`,
`SessionEnd`, `SubagentStart`, `SubagentStop` and `TeammateIdle`.

File names are the hook's own `<unix-nanos>-<pid>.json`, so sorting by name
replays the session in order. Paths are scrubbed: the working directory is
`/probe/work`, the home directory `/home/user`, scratch paths `/tmp/scratch`.
Nothing else was edited.

| Scenario | Files (by nanos prefix) | What happened |
|---|---|---|
| Startup | `…9911178` | `SessionStart` `startup`, session `8c634184` |
| Plain turn | `…9925884`–`…9927419` | `UserPromptSubmit`, `Stop` ("PONG") |
| Permission prompt | `…9971083`–`…9993148` | `PermissionRequest` (Bash, no `agent_id`); `Notification` `permission_prompt` 6.0s later; approved; `Stop` ("DONE") |
| Internal helper | `…9998115` | `SubagentStop` with no matching start (one of Claude's helpers) |
| Background subagent with a prompt | `…0013312`–`…0040900` | `SubagentStart`; parent `Stop` while it runs (`background_tasks` lists it as a running `subagent`); the subagent's `PermissionRequest` (with `agent_id`); `Notification`; approved; `SubagentStop`; a `UserPromptSubmit` whose prompt is a `<task-notification>`; final `Stop` |
| `/clear` | `…0077106`–`…0077137` | `SessionEnd` `clear`, then `SessionStart` `clear` with new session `487f460e` |
| Turn, then `/exit` | `…0104851`–`…0108611` | `UserPromptSubmit`, `Stop`, `SessionEnd` `prompt_input_exit` |
| `--resume 487f460e…` | `…0120400` | `SessionStart` `resume`, same session ID |
| Teammate | `…0135959`–`…0141463` | `SubagentStart`/`SubagentStop` for `probe-mate`, `TeammateIdle`, then two parent `Stop`s 1.27s apart with no event between (the lead picked up the reply); both list the teammate as `running` |
| `/exit` | `…0179150` | `SessionEnd` `prompt_input_exit` |
| `--resume <unknown id>` | `…0182765` | only `SessionEnd` (reason `other`) carrying the unknown ID `00000000-…`; Claude exited 1 |

Roster samples taken alongside (not stored here) showed the roster reaching
each new status 0–90ms before the matching hook's timestamp, and staying
`busy` through both intermediate `Stop`s.
