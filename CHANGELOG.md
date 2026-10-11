## [0.14.0] - 2026-10-11

### 🚀 Features

- *(hooks)* Register status events and parse their fields
- *(hooks)* Stamp each event with when its hook ran
- *(session)* Merge Claude status observations, newest first
- *(session)* Map Claude hook events to status observations
- *(session)* Track Claude status, conversation and last message per instance
- *(session)* Install loom's hooks on every Claude launch
- *(app)* Let a gated job be requested mid-flight
- *(app)* Merge roster answers with hook events per instance
- *(app)* Read hook events within a second and apply their status
- *(session)* Persist the Claude conversation to resume (schema v7)
- *(session)* Resume the recorded conversation after a crash
- *(ui)* Show Claude's last message on a stopped session's card
- *(account)* Registry of extra Claude accounts
- *(account)* Link the main config dir into account dirs
- *(account)* Read an account's identity and log it in
- *(account)* Probe plan usage with a headless get_usage request
- *(session)* Persist each instance's Claude account (schema v8)
- *(session)* Launch as the instance's account; fail closed when it is gone
- *(session)* Query the roster and remote-control auth as an account
- *(ui)* Account usage strip
- *(ui)* Show each Claude session's account on cards and the pane title
- *(overlay)* Account row in Session Launch Options
- *(overlay)* Accounts screen in Settings
- *(app)* Load the account registry and read each account's auth
- *(app)* Query the Claude roster once per account in use
- *(app)* Poll each account's plan usage on its own gate
- *(app)* Choose the Claude account in Launch Options and on R
- *(app)* Account usage strip above the tab bar
- *(app)* Add, log in, remove and pick Claude accounts from Settings
- *(cmd)* Loom account add/login/list/use/sync/remove
- *(account)* Export OwnedDir and ValidateMainDir for the CLI
- *(account)* Bootstrap the main dir's projects/ on a fresh install
- *(account)* Detect a credential env var that overrides every account
- *(ui)* A warning slot leading the account strip
- *(overlay)* A screen-wide notice above the Accounts rows
- *(overlay)* Pass an account notice through Settings to its Accounts screen
- Session lifecycle never attaches; the TUI owns its pane clients
- One loom TUI per global dir; a second one takes over
- *(core)* Instance views, IDs, and requests by ID
- *(claudetmp)* Locate, archive and restore Claude's per-session temp dirs
- *(session)* Archive Claude's temp dir on pause and kill, restore on resume
- *(app)* Sweep orphaned Claude temp dirs into archives at workspace load
- Claude_tmp_archive_dir relocates the Claude temp-dir archives
- *(core,config)* Workspace views, IDs and settings as values
- *(core)* The model's own loop (core.Loop)
- *(core)* Publish the whole state a replica needs
- *(rpc)* The wire, a server and a replicating client
- *(core)* Ready the model to serve every workspace
- *(rpc)* Route a request's answer to the client that made it
- *(core)* Serve every registered workspace
- *(serve)* The loom daemon process
- *(app)* Loom is a client of the loom daemon
- *(claudemd)* Lint CLAUDE.md structure and stale identifiers
- *(core)* Stable IDs across daemons (stage 3R A)
- The daemon says bye; stop order; no-spawn connect (stage 3R B)
- The TUI survives the daemon going away (stage 3R C)
- *(core)* Unregister, rename, reset and forced account removal (stage 3C+4 A)
- The subcommands are clients of the daemon (stage 3C+4 B)
- The daemon answers trust prompts and scans hooks with no TUI (stage 3C+4 C)

### 🐛 Bug Fixes

- *(account)* Close a Remove path-escape and add safety guards
- *(account)* Bound subprocess waits and refuse a missing account dir
- *(account)* Export a Command builder; refactor AuthStatus/ProbeUsage onto it
- *(session)* Roster query fails closed when an account's config dir is gone
- *(account)* Stop Unshared false positives, harden the within check
- *(session)* Distinguish an unregistered, unloadable, and dir-missing account
- *(session)* Start's account check runs even with a preset tmux session
- *(session,tmux)* Restart relaunches under a freshly resolved account env
- *(session)* Remote-control auth keeps the identity when an override blocks it
- *(session)* Resume fails closed on a missing account before touching anything
- *(session)* FromInstanceData normalizes the default account name to ""
- *(overlay)* Target Accounts remove/cursor by identity; stop rows wrapping
- *(ui)* Width-only rail badge decision, painted gap, truncation order
- *(overlay)* Reset the Account row's selection when it drops below two
- *(ui)* Stop the strip's elevated-usage color from reading as Attention
- *(account)* Resolve symlinked ancestors of a not-yet-existing path
- *(session)* Resume's account check no longer blocks a live-session reattach
- *(tmux)* TmuxSession.Env() returns a clone, not the internal slice
- *(ui)* Rail badge decision must ignore the index prefix's own width
- *(overlay)* Stop the Accounts screen misleading at its real width
- *(account)* Reload keeps an Unavailable registry's own error
- *(app)* Reread accounts.json on the health tick only when it changed
- *(app)* Reread account auth when accounts appear or lose plan access
- *(cmd)* Split remove's --force from --yes, fix ordering and leaks
- *(app)* Show the account switch when R's account was removed
- *(app)* Say "logged out" once on the Accounts screen
- *(app)* Count a loaded session once when refusing an account removal
- *(app)* Point the Settings unshared-files refusal at the CLI's --force
- *(cmd)* Break the cmd -> session import cycle
- *(app)* Refuse a TUI login on an account whose config dir is gone
- *(app)* Skip the accounts' Sync when the main config dir is unsafe
- *(cmd)* Catch, don't ignore, SIGINT around the login child
- *(account)* Remove could delete through a symlink-disguised owned dir
- *(session)* Remote-control override check covers CLAUDE_CODE_OAUTH_TOKEN
- *(session)* MissingAccountError names R, the actual recovery action
- *(cmd)* Warn on a credential override, fix global config dir lookup
- *(app)* An extra account's blocked hint names its own login
- *(app)* Keep menu highlighting from reordering keys typed in Settings
- *(app)* Warn once when loom itself runs as an extra account
- *(session)* AccountDirMissingError points at the actual recovery
- *(app)* Warn on the strip when a credential overrides every account
- *(app)* Notice a credential override on the Accounts screen too
- *(account)* Trim ActiveCredentialOverride's env value like the RC check
- *(account)* Compare account dirs by resolved path, not exact spelling
- *(account)* Close resolvedOrClean's not-yet-existing-path bypass
- *(session)* Mark a CLI-logged-in account's onboarding done before launching
- *(tmux)* Type text through paste-buffer, not send-keys -l
- Release pane clients only after the registry drops them; attach after Lua resume
- *(tmux,ui)* A pane client whose pump hit EOF on its own is dead
- *(app)* Persist idle agents; skip only never-started instances
- *(ui)* Bound the Dead event's repair of a client that exits on attach
- *(tmux)* Set detach-on-destroy on every agent session
- No-client status scans, paused send_prompt, batched release
- *(core)* Package A review fixes
- *(core)* Package B review fixes
- *(app)* A start landing on a killed instance never enters inline attach
- *(core)* Package C review fixes
- *(core)* Final review fixes for stage 1B
- *(orphan)* A live orphan keeps the account its agent runs on
- *(core)* Request preconditions and job nil guards
- *(app,core)* Show refused requests; keep busy rows' terminal clients
- *(script)* The yielding methods' pcall refusal names the TUI
- *(script)* Resumed errors name their file; tail calls raise without a position
- *(script,app,core)* Lua instances refresh after a lifecycle call; ladder survives a failed op; dead code
- *(script)* A killed handle reaches no pane; pin a recovered handle's refresh
- *(git)* Remove worktrees an interrupted add left locked
- *(app)* Slots take the model's newest workspace view
- *(ui)* No terminal shell for a session being killed
- *(app)* Draw the issue picker
- Review fixes for daemon stage 3A
- *(serve)* Package A's review; build on Windows again
- *(core,app)* The daemon's environment decides, and the TUI shows it
- The review fixes for B, C, D and the final review
- *(tmux)* Run every tmux client as UTF-8 with -u
- *(claudemd)* Close the review's gaps in fences, the index and phrases
- *(claudemd)* Leave CLAUDE.local.md to its owner
- *(core)* An orphan placeholder's ID is the same on every daemon
- *(rpc)* Bye waits until the replies of the calls it let in are queued
- *(app)* Fail requests refused around the bye at once; resend the last used tab
- *(e2e)* Prove the pause is in flight before the stop; KillDaemon reports no kill that did not happen
- *(app)* A daemon that dies soon after a rejoin counts as a failed start
- *(devsandbox)* Retry Start when it reaches a tmux server mid-exit
- *(session)* Give account sessions their resolved memory dir
- *(core)* Answer a refused Reset with its Reply, as every request does
- *(cli)* Warn on the daemon's credential, refuse a non-daemon lock holder, report a reset once
- *(daemon)* Wait out a probe over a stopped daemon's record in TakeLock
- *(core)* An idle hook scan wakes no client
- *(git)* Reset removes the branches of prefixed worktrees
- *(app,cli)* Defer a live tab close, follow offline renames, guard login default
- *(core,agent)* Answer each trust prompt, and Claude's safety check by its option
- *(core)* Keep no terminal that failed to start; Register applies the registry

### 💼 Other

- *(script,app)* Lua and issue fetches through the model

### 🚜 Refactor

- *(session)* Move the hook transport into session/hooks
- *(session)* Launch env as a LaunchEnv struct with CLAUDE_CONFIG_DIR
- *(tmux)* Split a lifecycle-only Session from the attach client
- *(ui,app)* Read agent panes through PaneClients
- *(core)* The model owns workspaces and their instances
- *(core)* Lifecycle operations and completions move into the model
- *(core)* The health tick and background jobs move into the model
- *(ui,app)* The TUI renders instance views
- *(app)* Lifecycle operations are requests by ID
- *(app)* Creation flows hold a draft until Create
- *(app)* The pane ladder is a display overlay; tmux by name
- *(app)* Prompt sends hold input to their session until they land
- *(app,core)* The TUI acts on instances only by request
- *(script,app)* Lua and issue fetches go through the model
- *(app,ui)* The TUI holds workspace views, not workspaces
- *(core,app)* Core.Core is value-typed
- *(core,app)* The model runs on its own loop
- *(app)* The TUI talks to its model over the wire
- *(app)* The tabs, the classic workspace and startup are the client's
- Delete dead code and stale comments (stage 4)

### 📚 Documentation

- *(spec)* Design Claude hook events for status, resume and last message
- *(spec)* Amend hook-events design with live probe results
- *(plan)* Implementation plan for Claude hook events
- Describe hook-driven Claude status, resume and last message
- Design for choosing a Claude account per session
- Implementation plan for Claude account selection
- *(plan)* Fix the get_usage fixture's missing brace
- Fold the Phase A-C review decisions into the plan and spec
- Claude account selection
- *(spec)* Split loom account remove's --yes from --force
- Catch up account docs with the CLI/TUI fix rounds
- Catch up account docs with the final review's fix waves
- Describe the registry's clean-form check and in-memory canonicalization
- *(spec)* Supervised workspaces design
- *(spec)* Restructure supervised workspaces around a work log
- *(spec)* Record supervised-workspaces probe results
- *(plan)* Supervised workspaces stage 1
- *(spec,plan)* Fold review fixes into supervised workspaces stage 1
- *(spec)* Loom daemon and scrum workspaces designs
- *(plan,spec)* Daemon stage 1A pane split; split stage 1 into 1A-1D
- *(plan)* Regroup daemon stage 1A into four packages
- *(plan)* Amend 1A for paste-buffer input and C1's stuck-pump test
- *(plan)* Binding amendments to Package C from the A and B reviews
- *(plan)* Amendment 6, close released pane clients in parallel
- *(plan)* Amendments 7-8 from Package C's review
- CLAUDE.md for the pane split (daemon stage 1A)
- Paste-buffer input and the pane-client liveness rule
- Lua input docs, repair log names its path, round 3 rules
- Detach-on-destroy covers every loom session; send_keys history
- *(plan)* Record stage 1A outcome and follow-ups
- *(plan)* Daemon stage 1B, the core model
- CLAUDE.md and spec for the core model (daemon stage 1B)
- *(plan)* Record stage 1B outcome and follow-ups
- *(spec)* Locked worktree removal and Claude temp-dir archiving
- *(plan)* Daemon stage 1C, the instance boundary
- CLAUDE.md, scripting and spec for the instance boundary (daemon stage 1C)
- Issue fetches in CLAUDE.md; new_instance's workspace and ctx's lifetime in scripting.md
- *(plan)* Record stage 1C outcome and follow-ups
- *(plan)* Locked worktrees and Claude temp-dir archiving
- *(plan)* Amendment B-1, the contract test drives an interactive Claude
- *(plan)* Review amendments B-2..B-8, C-1, C-2, D-1
- *(plan)* Review amendments A-1, B-9, C-3..C-7, D-2
- *(plan)* Review amendments A-2, C-8, D-3
- *(plan)* Final-review amendments F-1..F-6
- *(plan)* Record final-review follow-ups
- *(plan)* Amendment F-7 from the final smoke run
- *(plan)* Record the terminal-pane follow-up
- *(plan)* Widen the error-bar truncation follow-up
- The resume status gate lives in core now
- *(plan)* Daemon stage 1D, the workspace boundary
- CLAUDE.md and spec for the workspace boundary (daemon stage 1D)
- *(plan)* Record stage 1D outcome and follow-ups
- *(plan)* Daemon stage 1E, the model's own loop
- CLAUDE.md and spec for the model loop (daemon stage 1E)
- *(plan)* Record stage 1E outcome and follow-ups
- *(plan)* Daemon stage 2, the wire and the client replica
- CLAUDE.md and spec for the wire (daemon stage 2)
- *(plan)* Record stage 2 outcome and follow-ups
- *(plan)* Daemon stage 3A, the multi-client model
- CLAUDE.md and specs for the multi-client model (daemon stage 3A)
- *(plan)* Record stage 3A outcome and follow-ups
- *(plan)* Daemon stage 3B, loom serve
- CLAUDE.md, USAGE.md and the specs for loom serve (daemon stage 3B)
- *(plan)* Record stage 3B outcome and follow-ups
- *(spec)* Design stage 3R, live reconnect
- *(app,ui,script,account,cmd,config,log,review,devsandbox,tools,e2e)* Rules and facts per package
- *(tools)* List claudemd's documented blind spots
- *(core,rpc,serve,session,tmux,git,vt,exec,testenv)* Rules and facts per package
- *(session)* Name the two account-selection gaps of resume and orphan recovery
- *(app,ui,script,account,cmd,config,log,review,tools)* Narrow guards to what tests pin
- *(tools)* Note indented code blocks and the auto-loaded files claudemd checks
- Root CLAUDE.md to the conventions; ARCHITECTURE, guide index, root guides
- *(script)* Drop the root keybinding table, which USAGE.md now holds
- The playbook's bars for docs/claude/INDEX.md and the root's conventions
- A replacing TUI stops the daemon it dialed; replaceGuard's LOOM_GLOBAL_DIR case
- *(app,config,review)* Narrow the last guards to the tests that pin them
- *(core,rpc,serve,session,tmux,git,vt,exec,testenv)* Fix review findings
- Correct stale package comments
- The Docs workflow and go test now enforce the CLAUDE.md lint
- *(plan)* Stage 3R, live reconnect
- Live reconnect (stage 3R)
- *(plan)* Record stage 3R outcome and follow-ups
- *(core,rpc,cmd)* Document the methods revive flags on main
- CI lints only the lines a push changes; how to run its linter locally
- Say what the lint checks in README.md and docs/ARCHITECTURE.md
- Document newerDaemonError.Is (revive)
- *(spec)* Design stage 3C+4
- *(plan)* Stage 3C+4, subcommands as clients, headless duties, cleanup
- Subcommands as clients, headless duties; v0.14.0 (stage 3C+4)
- *(plan)* Record stage 3C+4 outcome and follow-ups

### 🧪 Testing

- *(fakeagent)* Fire loom's hooks like Claude; hook-driven e2e test
- *(session)* Opt-in contract test for Claude's status hooks
- *(account)* Opt-in contract test for the usage probe
- *(session)* Cover the remaining account fail-closed/fresh-env paths
- *(app)* An Accounts-screen add against an unsafe main dir says why
- *(session)* Switching accounts still resumes the recorded conversation
- *(app)* Flow-selection fixtures start their instances
- *(app)* Pin the view store's freshness; prune bells
- *(git)* Isolate TestStashOnDisk's temp dir from concurrent runs
- *(ui)* Config literals for the embedded Settings
- *(e2e)* The daemon's lifecycle end to end; loomdev runs a sandbox daemon
- *(app)* Stop fixtures depending on the user name and time zone
- *(claudemd)* Enforce the conventions on the repo; Docs workflow
- E2e and loomdev for live reconnect (stage 3R D)
- Isolate the main Claude config dir in every test binary
- E2e for subcommands as clients and headless trust prompts (stage 3C+4 D)
- *(e2e)* Read only the next daemon's log, wait out the dead daemon's lock

## [0.13.0] - 2026-09-23

### 🚀 Features

- *(git)* Configurable base branch and per-session branch prefix
- *(session)* Derive Claude status from the agent roster
- *(ui)* Show Claude's wait reason on prompting cards
- *(subagent)* Parse Claude hook events and agent metadata
- *(subagent)* Track live subagents and teammates from hook events
- *(subagent)* Write per-launch hook settings and event folder
- *(subagent)* Scan hook events into a replayable compact log
- *(config)* Add Track Subagents preference
- *(agent)* Add --settings flag injection for Claude
- *(session)* Launch Claude with subagent hooks and track agents
- *(app)* Scan subagent hook events on the health tick
- *(ui)* Show live subagent count on rail cards
- *(ui)* List live subagents on overview cards
- *(tmux)* Route every tmux exec through a socket-aware builder
- *(config)* LOOM_GLOBAL_DIR relocates the workspace registry
- Refuse to start loom inside its own tmux session
- *(tools)* Add fakeagent, a deterministic agent stand-in
- *(devsandbox)* Named sandboxes with toy repo and seeded config
- *(devsandbox)* Headless driver on the private tmux server
- *(tools)* Add loomdev, the dev sandbox CLI
- *(github)* Add session/github types and pure state join
- *(github)* Fold check rollups, PR state and review decisions
- *(github)* Query PRs and issues through the gh CLI
- *(github)* Issue slug, prompt seeding and #n shorthand parsing
- *(git)* Ahead/behind counting, targeted ref fetch, shared gh check
- *(session)* Link instances to GitHub issues (schema v6) and hold transient GitHub/parity state
- *(app)* Batched GitHub poller with roster-style throttle and wholesale replace
- *(app)* Count base-branch parity on the metadata tick; refresh GitHub state after push
- *(ui)* Issue, PR, checks and parity badges on rail and overview cards
- *(script)* Bind I to cs.actions.new_from_issue
- *(overlay)* GitHub issue picker with type-to-filter
- *(app)* Issue picker state and issue-seeded session creation
- *(app)* Expand a leading #n in the new-session prompt into the issue

### 🐛 Bug Fixes

- *(git)* Recover a half-removed worktree instead of wedging resume
- *(session)* Never destroy a live session's worktree on a stale probe
- *(session)* Pause must not remove the worktree on an inconclusive liveness probe
- *(git)* Give `worktree remove` its own deadline instead of the 8s tick budget
- *(app)* Throttle the Claude roster poll off the health tick
- *(ui)* Re-render agent/terminal panes on mouse-wheel scroll
- *(session)* Join the Claude roster on interactive sessions only
- *(session)* Reset subagent state on every launch
- *(session)* Make Restart a real launch through launchProgram
- *(session)* Keep each hooks folder a single path segment
- *(ui)* Sanitize subagent text and wait reason on cards
- *(subagent)* Drop rows when a tracked launch loses its hooks folder
- *(session)* Launch Claude sessions with the fullscreen renderer
- *(devsandbox)* Refuse an incomplete toy repo instead of skipping it
- *(devsandbox)* Keep the scrollback margin to dead driver panes
- *(devsandbox)* Treat a missing driver pane as not running
- *(tmux)* Scope the nesting guard's socket check to the enclosing server
- *(loomdev)* Reject an empty --text on wait
- *(session)* List tmux sessions for the hook sweep via the socket-aware builder
- *(app)* Surface the poll error in the issue picker instead of waiting forever
- *(session)* Preserve undecodable instance records and refuse saves after a failed load
- *(session)* Wrap load failures on every write path and exempt preserved records from the orphan sweep
- *(app)* Keep preserved workspace terminals and skip the restore sweep when a workspace fails to load
- *(app)* Load global storage when every workspace restore fails
- *(app)* Refuse new sessions while storage is latched, let q quit, isolate app tests from the real tmux server
- *(app)* Give script hosts a main-goroutine snapshot instead of the live model
- *(script)* Rebind ctx to the resume host so post-yield calls aren't dropped
- Keep script-reachable terminal sends off the pane lock during tmux I/O; drop unused Host.Workspaces
- *(script)* Answer HasAction/Registrations from a published snapshot so a running handler can't freeze the UI
- *(script)* Bound engine shutdown so quit can't hang behind a running handler
- *(exec)* Build every git/gh subprocess through LC_ALL=C constructors, enforced by test
- *(exec)* Force only git's message language, keep the user's character set for hooks and filters
- *(script)* Route Lua print to the script log instead of stdout
- *(script)* Send script logs to the structured logger instead of an undrained buffer
- *(app)* Hold the focused-slot invariant at every slot mutation
- *(app)* Release attach PTYs when a workspace slot is dropped
- *(app)* Resolve start/recover completions against the owning slot, never the focused selection
- *(app)* Release terminal-pane attach clients of dropped slots
- *(app)* Set menu state on Update, not inside Cmd closures
- *(ui)* Keep the list selection on its row when an earlier row is removed
- *(app)* Never move the selection under an open flow; cancel paths kill the pending instance by identity
- *(app)* Match reopened twins by worktree identity; stamp start owners by identity
- *(script)* Let cs.await(cs.actions.X()) continue after the intent completes
- *(script)* Cancel dropped coroutines and name the running handler in logs
- *(app)* Register the startup workspace on Update, not inside the confirmation Cmd
- *(ui)* Keep the selection visible when a workspace terminal is prepended
- *(app)* Stamp resume completions with their owning slot and release orphaned resumes
- *(app)* Keep failed-to-restore workspaces open until opened or deselected
- *(app)* Load global mode like startup and release its stale terminal clients
- *(session)* Relaunch in place when resuming onto an intact worktree instead of force-removing it
- *(app)* Save tabs before loading global state; resolve the global dir like startup
- *(app)* Skip the reload when re-entering global mode; release previews of failed resumes
- *(tmux)* Kill sessions by exact name, never by prefix
- *(git)* Drop stashes by verified sha; parse rev-parse stdout only
- *(session)* Never re-apply a dropped stash; clear the stash of an aborted pause
- *(session)* Only delete a branch a failed Start created
- *(session)* Record the base commit on in-place resume; refuse unverifiable trees helpfully
- *(session)* The orphan sweep kills only sessions whose cwd this process owns
- *(tmux)* Map ':' and '.' out of tmux session names
- *(tmux)* Target sessions and panes exactly; never let a name prefix-match a sibling
- *(git)* Unlock an interrupted worktree add before rebuilding; give worktree add its own deadline
- *(session)* Surface stash notices; keep a failed start's worktree unless its agent is gone
- *(reset)* Stop before removing worktrees when the tmux sweep fails; report sweep counts
- *(git)* Unlock a worktree entry only when its path provably holds no tree
- *(session)* Point a skipped failed-start cleanup at recovery, not D
- *(nix,test)* Make the package check phase and CI pass without a dev machine's setup

### 🚜 Refactor

- *(app)* Split app.go into init, overview and workspace files
- *(app)* Replace hand-rolled poll throttles with pollGate
- *(app)* Key poll intervals by gate kind so a zero-value gate still throttles
- *(app)* Make the focused workspace slot the single owner of per-workspace state
- *(app)* Tidy slot-focus leftovers
- *(session)* Move agent-pane I/O behind Instance.Pane()
- *(session)* Encapsulate Instance launch fields behind accessors
- *(session)* Drop the dead AgentPane.CheckAndHandleTrustPrompt
- Delete dead selection- and title-based removal APIs

### 📚 Documentation

- *(specs)* Design subagent nesting on loom cards
- *(specs)* Amend subagent nesting for loom restarts
- *(plans)* Implementation plan for subagent nesting
- Document subagent tracking
- Document hooks folder location, escaping and Restart relaunch
- *(specs)* Design for the loom-in-loom dev sandbox
- *(plans)* Implementation plan for the dev sandbox
- Document the dev sandbox and its isolation knobs
- Correct roster and tmux-quirk explanations, document guard scope
- Design spec for GitHub issue integration
- Add base-branch parity section to GitHub issues spec
- Implementation plan for GitHub issue integration
- GitHub issue integration keybinding, package and poller gotcha
- *(plan)* Architecture audit remediation plan
- Sync CLAUDE.md and package docs with the audit remediation
- Record the resume, stash and tmux-kill safety rules in CLAUDE.md

### ⚡ Performance

- *(subagent)* Skip lstat for entries a scan does not use

### 🧪 Testing

- *(subagent)* Add opt-in real Claude hook contract test
- *(subagent)* Assert the teammate shutdown sequence in the real Claude test
- *(vt)* Deflake the real-tmux scrollback tests
- Route preview test tmux cleanup through the builder
- *(e2e)* Smoke-test a sandboxed loom end to end
- *(e2e)* Make the cross-server sweep test able to fail; prove a clean restart quit
- Enforce keymap parity, overlay conformance and sandbox strip-list; isolate ui tests from the real tmux server
- *(exec)* Isolate the child-env git test from user config and keep env out of failure output
- *(tmux)* Run the real-tmux capture test on a private server
- *(tmux)* Shorten private socket names to fit the sun_path limit
- *(session)* Isolate orphan-discovery tests from the real tmux server
- Isolate every package's tests from the real ~/.loom

### ⚙️ Miscellaneous Tasks

- *(app)* Name a reopened owner, give orphan placeholders the slot's program, warn before closing unloaded workspaces
- *(nix)* Bump nixpkgs so the repo's own flake meets go.mod

## [0.12.0] - 2026-08-21

### 🚀 Features

- *(config)* Add the Claude1MContext global default
- *(app)* Carry Context1M through LaunchOptions
- *(app)* Decode quoted and [1m]-suffixed --model values
- *(app)* Compose the [1m] suffix onto supported models
- *(ui)* Add the 1M Context row to Session Launch Options
- *(ui)* Add the 1M Context row to Claude Preferences

### 🐛 Bug Fixes

- *(agent)* Single-quote the --model value so [1m] survives zsh

### 🚜 Refactor

- *(config)* Move the 1M capability onto ClaudeModels

### 📚 Documentation

- Drop stale web/ entry from CLAUDE.md
- *(specs)* Design for the Claude [1m] long-context option
- *(plans)* Implementation plan for the Claude [1m] option
- Document the Claude1MContext config field

### ⚙️ Miscellaneous Tasks

- *(build)* Drop windows from the build/test matrix

## [0.11.0] - 2026-07-29

### 🚀 Features

- *(review)* Vendor crit review store, root-threaded
- *(review)* Compose review comments into an agent prompt
- *(review)* Vendor crit git diff parser, dir-threaded
- *(ui/review)* Vendor crit TUI as embeddable review pane
- *(ui)* Workbench review tab hosting the review pane
- *(app)* Workbench review tab wiring — open, freeze, route, teardown
- *(app)* Send review comments to the agent with confirmation
- *(app)* Code review mode + focus-mode review entry (c / 5)

### 🐛 Bug Fixes

- *(app)* Resolve kill/fail completions by instance, not focused list
- *(ui)* Expire ErrBox info toasts so they stop lingering
- *(review)* Close review lock fd on contended-save path
- *(ui/review)* Tolerate per-file load errors; drop dead errMsg/filePath
- *(ui/review)* Non-fatal save errors, empty-pane key guard, exit-persist test
- *(ui/review)* Apply footer style once in renderFooter
- *(ui/review)* Narrow idle key capture, async per-file diffs, guard saves after worktree removal
- *(app)* Clear review pane on session retarget; review-tab wheel, notices, prev-tab return
- *(app)* Gate review wheel-scroll out of modal capture; doc prose fixes

### 📚 Documentation

- Crit absorption design spec
- Crit absorption implementation plan
- Attribute vendored crit code in NOTICE.md
- Workbench review tab usage and keybindings

### 🧪 Testing

- *(app)* Pin the non-git-worktree error path for code review entry

### ⚙️ Miscellaneous Tasks

- Add crit vendoring dependencies
- Sync vendor/ for chroma v2.23.1 upgrade
- Pin go-gitdiff to upstream crit's v0.8.1
- Remove unused web/ marketing site

## [0.10.0] - 2026-07-23

### 🚀 Features

- *(vt)* Emulator scrollback API + alt-screen filter for tmux client streams
- *(tmux)* Seed pre-attach history on Restore and expose emulator scroll accessors
- *(ui)* Add ScrollModel — shared emulator-backed pane scroll state
- *(ui)* Drive agent-pane scrolling from emulator scrollback
- *(ui)* Terminal pane on ScrollModel; fix lock-across-capture and alt-screen scroll
- *(config)* Add fable to the Claude model selector
- *(config)* Add Theme field with locked accessor
- *(ui)* Theme registry with role vars and rebuild hooks; migrate all styles
- *(settings)* Theme selector row with live apply
- *(session)* Track status age for card wait labels
- *(ui)* Card component with densities, tails, and overview sort
- *(ui)* Render session list as live mini-card rail with peer summaries
- *(app)* Feed live peer-workspace summaries to the rail
- *(config)* Persist UI layout prefs in state.json
- *(ui)* Adjustable agent/terminal split with hideable terminal pane
- *(app)* Rail visibility + persisted layout prefs plumbing
- *(keys)* Attention jumps, rail/terminal toggles, split resize via Lua actions
- *(ui)* Overview card-grid component with grouping and collapse
- *(app)* Overview view mode with tab toggle, sorted grid cursor, and key whitelist
- *(config)* Add ClaudeLoomContext with nil-on default
- *(session)* Add embedded loom-context prompt files
- *(agent)* Add ApplyLoomContextFlag for --append-system-prompt-file
- *(session)* Add loom-context enabled flag, file writer, launch wrapper
- *(session)* Inject loom-context flag at instance launch
- *(app)* Write loom-context files and sync enabled flag
- *(ui)* Add Loom Context toggle to Claude Preferences
- *(app)* Background slot flag and foreground-only tab bar/persistence
- *(app)* PromoteSlot/demoteSlot for background-slot transitions
- *(app)* Off-goroutine workspace reconcile and ensureFleetLoaded
- *(app)* Append background slot on workspaceActivatedMsg
- *(app)* Trigger progressive fleet load on overview entry
- *(overview)* Multi-group fleet data model and rendering
- *(overview)* Combined vertical windowing keeps fleet cursor visible
- *(app)* Global fleet cursor across overview groups
- *(app)* FocusCursorSlot primitive for cross-workspace actions
- *(app)* Cross-workspace enter/D/r/n via focusCursorSlot in overview
- *(app)* Fleet-wide jump-to-waiting across workspaces
- *(app)* Fleet-engaged tab toggle demotes/promotes instead of teardown
- *(ui)* [**breaking**] Overview shows only open workspaces (drop lazy fleet loading)
- *(config)* Persist per-session workbench split ratios
- *(files)* MostRecentMarkdown follow-scan helper
- *(ui)* Theme-derived glamour style for markdown rendering
- *(ui)* MarkdownPane glamour viewer with follow/pin and scroll
- *(ui)* MarkdownPane raw-text edit mode
- *(ui)* Workbench right panel (tabs: markdown/diff/files/terminal)
- *(app)* Workbench view mode — enter/esc/tab, key gating, layout
- *(app)* Workbench markdown follow scan, load, and files listing
- *(app)* Workbench markdown save with mtime conflict guard
- *(app)* Workbench mouse routing; docs for workbench mode

### 🐛 Bug Fixes

- *(app)* Converge status detection so idle agents don't latch on Running
- *(vt)* Harden alt-screen filter CSI abort and spike-test teardown
- *(tmux)* Drop stale seed history when a re-capture fails
- *(ui)* Align ScrollModel with the emulator's true screen span
- *(vt)* Make scrollback integration test CI-proof
- *(ui)* Solid selected-card background; harden card text handling
- *(ui)* In-memory card tails, rail height clamp, richer agent pane title
- *(app)* Refresh peer sections on bell; pin classification branches
- *(app)* Apply layout prefs on all startup paths; deterministic ratio reset
- *(app)* Pending-ratio precedence, slot-switch flush, resize regression tests
- *(ui)* Overview width hardening; pin windowing and card-height invariants
- *(app)* Stable triage grid, focus-drop for create flow, visible-window rendering
- *(ui)* Deleting instances never attention-ranked or jump-selected; global overview header
- *(session)* Write loom-context files atomically
- *(loom-context)* Inject on classic startup, resume, and crash-restart
- *(app)* Workspace picker excludes background slots from pre-checked set
- *(app)* Reconcile fleet overview with global mode; heal stale cursors
- *(ui)* Card tails skip Claude Code's bottom chrome
- *(ui)* Card tails skip Tip hints and tolerate the agents-strip footer
- *(ui)* Clamp markdown scroll on height resize; zero mtime on Clear
- *(app)* Clean up workbench state on implicit workspace slot switch
- *(app)* Workbench final-review fixes — terminal-visibility guard, global-mode cleanup, wheel guards

### 🚜 Refactor

- *(ui)* Rename ScrollModel.Window to AdvanceAndRender; document cap limits; backfill routing tests

### 📚 Documentation

- Add emulator-owned scrollback design spec
- Add emulator-scrollback implementation plan
- Amend scrollback spec — tmux clients live on the alt screen; adopt mode-1049 filter
- Amend scrollback plan — mode-1049 filter task, RenderWindow contract fix, probe-based alt routing
- *(specs)* Mission control GUI design — two-mode UI, card system, afterglow theme
- *(plans)* Mission control GUI implementation plan (phases 1-3)
- *(plans)* Amend Task 8 — committed card.go supersedes plan's RenderCard code
- Mission-control keys, view modes, theme system, ui prefs
- *(spec)* Design loom context injection into Claude sessions
- *(plan)* Implementation plan for loom-context injection
- *(spec)* Cross-workspace overview (mission control phase 4) design
- *(spec)* Clarify focusCursorSlot primitive and D/r/n promotion
- *(plan)* Cross-workspace overview (phase 4) implementation plan
- Cross-workspace fleet overview (phase 4) behavior and gotchas
- Session workbench (focus view) design spec
- Session workbench implementation plan

### 🧪 Testing

- *(vt)* Pin RenderWindow/ScrollbackLen lock discipline under -race
- *(ui)* Pin terminal-pane capture revalidation; guard the unlock/relock idiom
- *(settings)* Harden theme row tests; guard empty theme registry
- *(app)* Pin that quit persists background fleet slots
- *(overview)* Harden fleet windowing (up-scroll, short preceding groups)

### 🔧 Build

- *(deps)* Add charm.land/glamour/v2 for markdown rendering

### ⚙️ Miscellaneous Tasks

- Remove capture-pane scroll remnants; document emulator scrollback
- *(release)* V0.9.0
- *(config)* Remove DaemonPollInterval field
- *(ui)* Remove dead repo-badge machinery; flush ratio saves on workspace registration
- Restore vendor to upstream (drop accidental gofmt reformat)
## [0.8.1] - 2026-07-13

### 🐛 Bug Fixes

- *(vt)* Deliver BEL notifications outside the emulator write lock

### ⚙️ Miscellaneous Tasks

- *(release)* V0.8.1
## [0.8.0] - 2026-07-12

### 🚀 Features

- *(tmux)* Pane-event notifier registry and per-session coalescer
- *(tmux)* Emit dirty/quiet/dead pane events from the output pump
- *(app)* Render panes on pump dirty events; gate preview tick to snapshot mode
- *(tmux)* Source status detection from the emulator, capture-pane fallback
- *(app)* Run status detection on pane quiet events
- *(app)* Demote metadata poll to 3s health tick; verified PTY-death handling
- *(vt)* Track cursor visibility, shape, and blink via x/vt callbacks
- *(session,ui)* Expose emulator cursor state through tmux, instance, terminal pane
- *(ui)* Map focused-pane emulator cursor to split-local coordinates
- *(app)* Render the native hardware cursor over the focused pane
- *(app)* Pass inner-app OSC titles through to the host terminal
- *(app)* Surface pane bells as attention badges in the session list
- *(app)* Forward host focus events to panes, gated on mode 1004

### 📚 Documentation

- Spec for event-driven panes and native cursor
- Implementation plan for event-driven panes and native cursor
- Document event-driven pane updates and native terminal behavior

### ⚙️ Miscellaneous Tasks

- *(release)* V0.8.0
## [0.7.0] - 2026-07-12

### 🚀 Features

- *(app)* Give recovery a spinner, retryable failure, and success feedback
- *(app)* Surface load-failed sessions in the recovery summary
- *(ui)* Explain recoverable sessions in the preview pane
- *(cli)* Require --force for loom reset

### 🐛 Bug Fixes

- *(session)* Stop tmux sessions from silently going unusable
- *(tmux)* Route prompt/trust detection through the agent adapter registry
- *(tmux)* Reap PTY children and stop leaking the new-session ptmx
- *(tmux)* Sweep legacy-prefixed sessions on reset; drop dead setPumpDest
- *(app)* Exclude Recoverable placeholders from the metadata tick
- *(app)* Open the post-prompt help screen on the main goroutine
- *(app)* Snapshot the instance list for Pause/Resume persistence
- *(app)* Persist settings edits made in global mode
- *(app)* Surface instanceChanged errors on cancel paths, no menu flash in title entry
- *(session)* Release the start reservation on worktree-construction failure
- *(session)* Checkpoint the stash ref before pause's destructive steps
- *(git)* Guard baseCommitSHA and stashRef with a mutex
- *(session)* Discover orphans at any branch-prefix depth
- *(config)* Load partial config files over defaults
- *(config)* Harden workspace registry writes and lookups
- *(script)* Make cs.sprintf a true string.format alias
- *(ui)* Drop the dead overlay shadow pass, fix fill-rune width advance
- *(cli)* Exit non-zero and print CLI errors to stderr
- *(ui)* Show recover/discard in the menu and help for orphaned sessions
- *(app)* Keep a failed discard's row instead of silently dropping it
- *(app)* Scale error toast duration; name the stash on resume conflict

### 🚜 Refactor

- *(agent)* Dedupe claude flag insertion, tolerate leading whitespace
- *(session)* Delegate fromInstanceDataPaused to FromInstanceData

### 📚 Documentation

- Document session recovery in USAGE.md

### ⚙️ Miscellaneous Tasks

- *(release)* V0.7.0
## [0.6.0] - 2026-07-06

### 🚀 Features

- *(tmux)* Support per-session env vars via new-session -e
- *(session)* Add HeadroomProxyEnv, delete BuildHeadroomWrapCommand
- *(session)* Add InstanceData.HeadroomProxy, bump schema to v3
- *(session)* Wire Instance.HeadroomProxy into tmux session creation
- *(app)* Set Instance.HeadroomProxy at every instance-creation site
- Add Cache TTL (1h) launch option for Claude's prompt cache

### 🐛 Bug Fixes

- *(session)* Remove .loom-title sidecar when auto-cleaning orphaned worktrees
- *(session)* Kill terminal pane's tmux session on pause/kill
- *(cmd)* Mirror InstanceData.HeadroomProxy in migrationInstance
- *(app)* Check focused pane's tmux liveness during inline attach

### 🚜 Refactor

- *(agent)* Stop parsing a headroom-wrap prefix out of program
- *(config)* Rename HeadroomWrap to HeadroomProxy
- *(ui)* Rename Headroom Wrap to Headroom Proxy in both screens
- *(app)* Drop headroom-wrap command composition step

### 📚 Documentation

- *(spec)* Design Headroom Proxy (env-var) replacement for Headroom Wrap
- *(plan)* Implementation plan for Headroom Proxy
## [0.5.1] - 2026-07-06

### 🚀 Features

- *(git)* Add StashChanges/ApplyStash/DropStash to GitWorktree
- *(git)* Add a mutable StashRef field to GitWorktree
- *(session)* Persist StashRef on GitWorktreeData, bump schema to v3
- *(session)* Stash uncommitted changes on Pause instead of auto-committing
- *(session)* Restore stashed changes on Resume
- *(session)* Thread StashRef through Snapshot/FromInstanceData/reconcile
- *(config)* Add ClaudeEffort/ClaudeEfforts/Effort()
- *(agent)* Add ApplyEffortFlag to the Adapter interface
- *(session)* Add BuildEffortCommand
- *(ui)* Add Effort as a fifth Session Launch Options row
- *(ui)* Add Effort as a fifth Claude Preferences row
- *(app)* Compose Effort into applyLaunchOptions
- *(app)* Add ParseLaunchOptions, the reverse of applyLaunchOptions
- *(app,script)* Wire the R keybinding to a stub RestartWithOptionsIntent handler
- *(app)* Implement restart-with-options — reverse-parse, seed modal, resume

### 🐛 Bug Fixes

- Pass the matched adapter name to headroom wrap, not the raw program
- *(git)* Unstage previously-untracked files after ApplyStash
- *(session)* Drop a Paused instance's leftover stash on Kill
- *(ui)* Drop Effort's empty-string normalization, match sibling rows
- *(app)* Guard Async against a failed Loading transition, add missing RequestWindowSize
- *(app)* Fix RequestWindowSize for the blocked-RC restart path

### 🚜 Refactor

- *(keys)* Rename KeyCheckout to KeyStash, key 'c' to 's'
- *(app)* Rename helpTypeInstanceCheckout to helpTypeInstanceStash
- *(script)* Rename CheckoutIntent/checkout_selected to Stash
- *(app,ui)* Rename checkout call sites to stash
- *(app)* Generalize the Session Launch Options cancel path

### 📚 Documentation

- Add design spec for restarting a paused session with new options
- Fold Effort into the restart-launch-options design spec
- Switch Pause/Resume to git stash instead of an auto-commit
- Rename the checkout keybinding to stash in the design spec
- Add implementation plan for restart-launch-options
- *(app)* Update stale 'checkout' comment reference to 'stash'
- Fix remaining stale checkout references in scripting.md and intents.go
- Update checkout references to stash in current-state docs
- Describe stash-based (not commit-based) Pause/Resume in USAGE.md
- Fix remaining stale wording found in Phase 2 doc review
- Document the R (resume with options) keybinding
- *(app)* Drop stale 'added in a later task' from a landed comment

### 🧪 Testing

- *(session)* Exercise tracked-file modification in Resume stash test
- Rename checkout references to stash across the suite
- *(ui)* Update stale row-count test name/assertions for Effort row
- *(app)* Dispatch a real cancel keypress for the blocked-RC restart guard
## [0.5.0] - 2026-07-06

### 🚀 Features

- *(config)* Add HeadroomWrap and ClaudeModel settings
- *(agent)* Add ApplyModelFlag to the Adapter interface
- *(session)* Add BuildModelCommand and BuildHeadroomWrapCommand
- *(ui)* Add Model and Headroom Wrap rows to Claude Preferences
- *(ui)* Add SessionLaunchOptions per-instance modal
- *(app)* Add stateLaunchOptions and the Session Launch Options handler
- *(app)* Show Session Launch Options before starting a new instance

### 🐛 Bug Fixes

- *(session)* Guard BuildHeadroomWrapCommand against an empty program
- Preserve adapter identity through headroom wrap, fix stale RC-blocked check
- Detect remote-control auth on the real binary through headroom wrap

### 🚜 Refactor

- *(app)* Compose launch options via overlay.LaunchOptions

### 📚 Documentation

- Add design spec for Headroom Wrap, Claude Model, and launch options modal
- Add implementation plan for Headroom Wrap, Claude Model, and launch options modal

### 🧪 Testing

- *(app)* Cover the real launch-options closure and blocked-RC-via-modal path
## [0.4.0] - 2026-07-02

### 🚀 Features

- *(config)* Add ClaudePermissionMode setting
- *(agent)* Add ApplyPermissionModeFlag to the Adapter interface
- *(session)* Add BuildPermissionModeCommand
- *(app)* Apply Claude permission-mode flag at instance creation
- *(ui)* Add Permission Mode row to Claude Preferences

### 🐛 Bug Fixes

- *(recovery)* Harden orphan picker, reconcile, and slot-restore paths
- *(recovery)* Persist worktree title to recover exact orphan identity
- *(session)* Resolve merge artifacts from rebasing recovery work onto main
- *(session)* Mark Recoverable placeholders as started in FromInstanceData

### 📚 Documentation

- Add design spec for Claude permission-mode setting
- Add implementation plan for Claude permission-mode setting
## [0.3.0] - 2026-07-01

### 🚀 Features

- *(vt)* Add Emulator interface for pane display
- *(vt)* Add x/vt-backed Emulator with golden + race tests
- *(tmux)* Platform newEmulator factory with LOOM_PANE_RENDERER kill-switch
- *(tmux)* Add emulator field, geometry tracking, and RenderEmulator accessor
- *(tmux)* Pump ptmx output into the emulator (io.Discard fallback)
- *(tmux)* Build emulator in Restore; tear down in Close/PausePreview
- *(tmux)* Resize emulator and record geometry in SetDetachedSize
- *(tmux)* Disable status bar so the emulator stream matches capture-pane
- *(session)* Source agent-pane preview from the emulator with capture-pane fallback
- *(ui)* Source terminal pane from the emulator with capture-pane fallback
- *(vt)* Add RenderWindow + ScrollbackLen for windowed scrollback rendering
- *(session)* Expose ScrollbackLen/RenderWindow through TmuxSession and Instance
- *(ui)* Live-scroll offset model for the agent pane (drop viewport mode-switch)
- *(ui)* Live-scroll offset model for the terminal pane
- *(ui)* Jump-to-bottom footer with live new-line count while scrolled
- *(ui)* Forward scroll into TUI agents instead of windowing (no-op) history
- *(phase3)* Clipboard copy command + selection model (foundation)
- *(phase3)* Mouse drag-select + copy + click-to-focus
- *(phase5)* ForwardMouse + bracketed Paste primitives (foundation)
- *(phase5)* Route mouse + bracketed paste into the focused pane in interact mode
- *(phase5)* 'i' to interact, double-esc exit, relabel attach->interact
- *(agent)* Auto-launch Claude sessions with --remote-control
- *(agent)* Detect Claude auth before enabling remote control
- *(session)* Add Recoverable status for inline orphan recovery
- *(session)* Detect uncommitted changes on orphan candidates
- *(session)* Classify orphans into clean vs needs-review
- *(session)* Add branch-preserving orphan worktree removal
- *(session)* Orphan->InstanceData builder and Recoverable EnsureRunning guard
- *(app)* Exclude Recoverable instances from persistence
- *(ui)* Render Recoverable status in list and preview
- *(ui)* Add info-style status line to the error bar
- *(app)* ReconcileOrphans engine with auto-clean and inline review
- *(app)* Run orphan reconcile on every workspace-load path
- *(app)* Inline recover and discard actions for Recoverable orphans
- *(ui)* Add settings overlay skeleton with Auto Yes toggle
- *(app)* Wire S to open the settings overlay
- *(ui)* Support text-edit fields in the settings overlay
- *(ui)* Implement the Profiles CRUD sub-screen
- *(ui)* Implement the Claude Preferences sub-screen
- *(daemon)* Reload DaemonPollInterval every tick
- [**breaking**] Remove the Auto Yes feature entirely
- *(git)* Add GitWorktree.Merge
- *(script)* Add MergeSessionsIntent
- *(script)* Wire cs.actions.merge_selected()
- *(script)* Bind m to merge_selected in default keymap
- *(ui)* Add MergePicker overlay
- *(app)* Wire the merge-session picker end to end
- *(keys)* Add KeyMerge for the help-panel listing

### 🐛 Bug Fixes

- *(vt)* Drain x/vt reply pipe to prevent startup deadlock
- *(ui)* Source scroll history from tmux capture-pane, not the emulator
- *(app)* Re-render the scrolled agent pane on the preview tick
- *(phase3)* Offset selection hit-test by the tab bar height
- *(phase3)* Allow drag-select in inline-attach, not just default state
- *(phase5)* Drag-select in interact copies to Loom clipboard, not tmux
- *(ui)* Agent pane shows its full height (drop the ellipsis row)
- *(ui)* Clamp split pane to its height; truncate over-wide lines
- *(ui)* Revert per-line truncation in renderPane (broke the box right border)
- *(ui)* Cap pane content by rows so the bottom/right border isn't clipped
- *(ui)* Size pane body to full width so the right/bottom border reaches the edge
- *(ui)* Clamp pane dimensions so a small terminal can't crash on launch
- *(config)* Make Config safely mutable at runtime
- *(ui)* Stop MergePicker's digit-jump from crashing on out-of-range input
- *(app)* Capture merge target/sources at picker-open time, not commit time
- *(ui)* Support real two-digit index jumps in MergePicker
- *(tmux)* Poll instead of sleep in TestCaptureHistoryRealTmux
- *(tmux)* Set TERM in TestCaptureHistoryRealTmux for CI's tty-less runners

### 💼 Other

- *(ui)* Dampen forwarded agent wheel speed; smaller page jump

### 🚜 Refactor

- Retire capture-pane history path now that scrollback is in the emulator
- *(app)* Remove obsolete orphan-recovery overlay and plumbing
- *(ui)* Extract DisplayIndex helper for list numbering

### 📚 Documentation

- Correct CLAUDE.md drift and document concurrency invariant
- Add design spec for native terminal experience (embedded VT)
- Add Bubble Tea v2 migration plan (Phase 0)
- Add Phase 1 embedded-VT implementation plan
- Add Phase 2 live-scroll implementation plan
- Add recovery overhaul design spec
- Add recovery overhaul implementation plan
- Update CLAUDE.md for inline recovery; add recover-gate test; lint fix
- Add settings menu design spec
- Correct daemon-reload section of settings menu spec
- Document the settings menu keybinding
- Add design spec for git-merge session hotkey
- Fix merge-hotkey spec eligibility rules to match reality
- Fix merge-hotkey spec notification mechanism to match reality
- Add implementation plan for git-merge session hotkey
- Document the m (merge session) keybinding
- *(app)* Fix instanceByDisplayIndex comment to describe the snapshot it actually resolves against

### ⚡ Performance

- *(ui)* Forward agent scroll via PTY write, not a send-keys subprocess
- *(app)* Run the interact tick at ~60fps (16ms) for snappier echo

### 🧪 Testing

- Update key construction for Bubble Tea v2; fix space token
- *(app)* Reframe inline-attach scroll-reset test for the offset model
- *(nix)* Provide tmux to checkPhase so the real-tmux scroll test runs
- *(app)* Add merge_selected to the dispatch parity suite

### ⚙️ Miscellaneous Tasks

- *(ui)* Temporary panescroll diagnostics (offset/total/rows/maxOff)
- *(ui)* Log agent-pane CaptureHistory total/rows to pin the bail path
- *(ui)* Remove temporary panescroll diagnostics
- *(app)* Temporary mouseselect diagnostics for drag-select debugging
- *(app)* Remove temporary mouseselect diagnostics
- Temporary panesize diagnostics (PTY size vs rendered line width)
- Log reported terminal size in window-size handler (panesize diag)
- Log composed view widths (panesize diag)
- Log raw emulator line count vs pane height (panesize diag)
- Log terminal pane height (panesize diag)
- Remove temporary panesize diagnostics
## [0.1.4] - 2026-06-19

### 🚀 Features

- *(cli)* Add -v/--version flag

### 🐛 Bug Fixes

- *(session)* Guard Storage mutable state with a mutex
- *(config)* Guard State saves with a mutex
- *(tmux)* Guard ptmx and monitor with a mutex
- *(app,script)* Apply script nav effects on the main goroutine

### 📚 Documentation

- Add multi-agent codebase audit report

### ⚙️ Miscellaneous Tasks

- *(nix)* Derive flake version from main.go at eval time
- Run the test suite under the race detector
## [0.1.3] - 2026-05-11

### 🚀 Features

- *(session)* Add orphan worktree discovery
- *(ui/overlay)* Add orphan recovery picker overlay
- *(app)* Wire orphan recovery into startup flow

### 🐛 Bug Fixes

- *(config,app)* Preserve workspace registry on picker toggle
- *(session,app)* Restart dead workspace terminal tmux sessions
- *(session)* Preserve instances on transient reconcile failures
- *(app)* Round-trip global ↔ workspace mode transition
- *(app)* Trigger orphan recovery on workspace registration
- *(session,app)* Preserve reconcile failures across all load paths
- *(app)* Route recovered orphans to their source workspace
## [0.1.2] - 2026-05-04

### 🚀 Features

- *(script,app)* Wake panes from scroll mode on input/attach
- *(script,ui)* Add inst:send_terminal_keys lua method

### 🐛 Bug Fixes

- *(session,app)* Persist instances during kill window

### 📚 Documentation

- *(script)* Add open_emacs sample script
## [0.1.1] - 2026-04-21

### 🚀 Features

- *(session/files)* Add git-aware file listing package
- *(ui/overlay)* Add fuzzy subsequence matcher
- *(ui/overlay)* Add file explorer overlay
- *(script)* Add toggle_file_explorer action and intent
- *(app)* Wire file explorer overlay into state machine

### 🐛 Bug Fixes

- *(session,app)* Make storage deletion idempotent on kill path
- *(app,ui)* Capture repo name before kill to prevent counter leak

### ⚙️ Miscellaneous Tasks

- *(release)* Pass changelog file path directly to goreleaser
- *(ui)* Rebrand fallback splash from claude-squad to loom
## [0.1.0] - 2026-04-20

### 🚀 Features

- *(session/git)* Add support for checking out worktrees ([#5](https://github.com/aidan-bailey/loom/pull/5))
- *(ui, session/worktree)* Show `git diff` output in diff pane ([#9](https://github.com/aidan-bailey/loom/pull/9))
- *(app, session, ui)* Add support for poweruser instance creation ([#15](https://github.com/aidan-bailey/loom/pull/15))
- *(ui)* Allow scrolling diff pane with mouse scroll ([#17](https://github.com/aidan-bailey/loom/pull/17))
- *(ui)* Underline menu items on select ([#19](https://github.com/aidan-bailey/loom/pull/19))
- *(ui/overlay)* Add cursor to text input, allow moving cursor
- *(session/worktree)* Open submitted branch
- *(ui)* Add help screen ([#34](https://github.com/aidan-bailey/loom/pull/34))
- *(app, ui)* Add help screen ([#36](https://github.com/aidan-bailey/loom/pull/36))
- *(app/help)* Only show just-in-time help screens once ([#37](https://github.com/aidan-bailey/loom/pull/37))
- *(web)* Add landing page ([#77](https://github.com/aidan-bailey/loom/pull/77))
- *(web)* Add video to landing page ([#78](https://github.com/aidan-bailey/loom/pull/78))
- *(web)* Add copy button for install script ([#79](https://github.com/aidan-bailey/loom/pull/79))
- *(worktree)* Add configurable branch name prefix
- *(release)* Add brew section to goreleaser ([#99](https://github.com/aidan-bailey/loom/pull/99))
- Confirmation modal for `D` and `p` ([#105](https://github.com/aidan-bailey/loom/pull/105))
- *(web)* Add brew instructions to landing page ([#108](https://github.com/aidan-bailey/loom/pull/108))
- Detect username for branch prefix
- Add Gemini support ([#145](https://github.com/aidan-bailey/loom/pull/145))
- *(ui)* Support scrolling in preview pane ([#176](https://github.com/aidan-bailey/loom/pull/176))
- Show working indicator while creating worktrees ([#252](https://github.com/aidan-bailey/loom/pull/252))
- Add Terminal tab for interactive shell access ([#247](https://github.com/aidan-bailey/loom/pull/247))
- Enable selecting source branch for session ([#262](https://github.com/aidan-bailey/loom/pull/262))
- Allow configuring preset profiles for creating sessions ([#264](https://github.com/aidan-bailey/loom/pull/264))
- *(config)* Support CLAUDE_SQUAD_HOME env var for custom config directory
- *(config)* Add workspace registry and CLI subcommands
- Add workspace switching UI and startup detection
- *(cli)* Add workspace migrate command
- Add workspace tab bar UI, navigation keys, and state isolation
- Add workspace slot infrastructure and convert picker to checkbox toggle
- Wire workspace slots into app lifecycle and delete old reload path
- *(ui)* Show prompt indicator on workspace tabs when sessions await input
- *(ui)* Add startup mode to workspace picker overlay
- *(cli)* Add workspace use, rename, and status subcommands
- *(cli)* Add optional directory arg and fullscreen workspace picker
- *(keys)* Add KeyQuickInteract keybinding for 'i' key
- *(ui)* Add QuickInputBar component for inline tmux input
- *(ui)* Add SendPrompt to TerminalPane and TabbedWindow
- *(app)* Wire stateQuickInteract state machine, view rendering, and menu state
- *(session)* Add permanent workspace terminal pinned at top of instance list
- *(ui)* Add distinct highlight styling for workspace terminal
- *(keys)* Add h/l as workspace tab navigation aliases
- *(app)* Add keyMsgToBytes conversion for tmux PTY forwarding
- *(keys)* Add inline attach state and fullscreen attach keybinding
- *(app)* Implement core inline attach behavior
- Increase inline attach refresh rate to ~30fps
- *(ui)* Add prompting status indicator for permission prompts
- *(app)* Use Esc to detach inline attach and add capturing indicator
- *(keys)* Remap vim navigation from hjkl to jkl;
- *(keys)* Add KeyDiff binding for d key
- *(ui)* Add SplitPane component for agent+terminal layout
- *(ui)* Update menu for split pane layout with diff hotkey
- *(app)* Replace TabbedWindow with SplitPane in app
- *(app)* Add confirmation dialog before pausing a session
- *(ui)* Add titled pane borders and replace menu with status line
- *(keys)* Add targeted input and direct attach shortcuts
- *(app)* Auto-focus agent pane after new session creation
- *(session)* Add Deleting status to Instance enum
- *(ui)* Render Deleting status with ✕ icon in instance list
- *(app)* Set Deleting status immediately on kill confirm, revert on failure
- Add alt+a/alt+t fullscreen attach, remove broken O binding
- *(config)* Add AtomicWriteFile helper (temp+fsync+rename)
- *(session)* Add RWMutex to Instance, guard Status access
- *(daemon)* Reload instances.json on every poll tick
- *(session)* Add crash recovery health check and action determination
- *(app)* Reconcile instance state on startup for crash recovery
- *(session)* Clean up orphaned tmux sessions on startup
- *(session)* Append --continue to claude on crash recovery restart
- *(session)* Checkpoint save during Pause/Resume for crash resilience
- *(ui)* Grey out and skip deleting instances
- *(app)* Restore open workspace tabs across launches
- *(script)* Add Lua scripting engine with sandbox and userdata API
- *(app)* Dispatch Lua scripts after built-in key miss
- *(script)* Add Intent types and IntentID generator
- *(script)* Add coroutine tracking and Engine.Resume
- *(script)* Add cs.await for coroutine-based intent waiting
- *(script)* Add cs.bind and cs.unbind with coroutine-run actions
- *(script)* Expose sync cs.actions primitives via Host
- *(script)* Add deferred cs.actions lifecycle primitives
- *(script)* Add cs.actions.new_instance/show_help/open_workspace_picker
- *(script)* Add cs.actions attach + quick_input primitives
- *(app)* Drain script intents into scriptDoneMsg
- *(app)* Dispatch script intents via existing runXYZ handlers
- *(script)* Embed defaults.lua and load before user scripts
- *(app)* Add --no-scripts flag and narrow reserved keyset
- *(app)* Hard-reserve ctrl+c ahead of script dispatch
- *(log)* Add Debug level, subsystem loggers, and --log-level flag
- *(log)* Add context-based trace IDs for cross-goroutine correlation
- *(script)* Thread ctx through dispatch and trace keystroke flow
- *(session)* Add per-instance logger and lifecycle entry/exit logs
- *(daemon)* Add per-tick visibility and migrate to KV logging
- *(git)* Log every git command and worktree lifecycle ops
- *(script)* Add Engine.CleanupAllCoroutines for graceful drain
- *(app)* Drain script coroutines and close engine on shutdown
- *(log)* Enforce level gate on legacy loggers and rotate mid-run
- *(ui)* Add scroll primitives to preview, terminal, diff panes
- *(ui)* Route keyboard scroll through SplitPane dispatchers
- *(ui)* Add page and jump navigation to session list
- *(script)* Expose scroll and list nav primitives to Lua
- *(script)* Bind scroll and list navigation keys in defaults.lua
- *(app)* Route mouse wheel by cursor position
- *(ui)* Show scroll position indicator in pane titles

### 🐛 Bug Fixes

- *(app)* Allow quitting with Ctrl-C
- *(session)* Bound SIGWINCH channel to prevent unbounded events during resize
- *(ui)* Remove extraneous lines from preview
- *(app, session)* Allow a freeform session title
- *(ui)* Prevent layout shift between selected and unselected items
- *(app)* Pressing escape when creating a new instance should cancel
- *(ui/preview)* Add paused empty state
- *(session/worktree)* Use HEAD commit for worktree base
- *(ci)* Release workflow
- *(ci)* Escape changelog characters
- *(ci)* Use goreleaser instead
- *(session)* Ensure we automatically pass initial prompt ([#20](https://github.com/aidan-bailey/loom/pull/20))
- *(app)* Fix delete and reset bugs ([#21](https://github.com/aidan-bailey/loom/pull/21))
- *(worktree)* Improve commit message to have human readable date
- *(ui)* Use nicer enter key icon
- *(app)* Ensure auto yes mode works on aider ([#22](https://github.com/aidan-bailey/loom/pull/22))
- *(ui)* Make menu shorter and improve underlining ([#23](https://github.com/aidan-bailey/loom/pull/23))
- *(ui)* Set size on error box
- Install script
- *(session)* Replace . with _ for tmux session names
- *(session)* Reduce text for claude autoyes mode
- *(ui/overlay)* Update wrapText to account for spaces
- *(ui/overlay)* Use textarea bubble instead of custom implementation
- *(ui)* Truncate errors that do not fit on the screen
- *(ui/preview)* Guard against negative padding
- Validate that app is used within git repository ([#30](https://github.com/aidan-bailey/loom/pull/30))
- *(tmux)* Ensure consistent state after detach error ([#33](https://github.com/aidan-bailey/loom/pull/33))
- *(ui)* Use dynamic size for prompt input and fix cursorline background
- *(ui)* Remove focused style from prompt
- *(session/worktree)* Don't open branch when pausing instance
- *(keys)* Better description for tab
- *(app)* Ensure reset correctly cleans up claudesquad instances
- *(ui)* Improve help text
- *(menu)* Add empty state, fixes action highlight
- *(help)* Reorder help sections
- *(app)* Fix attaching bugs ([#38](https://github.com/aidan-bailey/loom/pull/38))
- *(app)* Use shift tab for auto yes ([#40](https://github.com/aidan-bailey/loom/pull/40))
- *(diff)* Compute git diff in a better way ([#41](https://github.com/aidan-bailey/loom/pull/41))
- *(app)* Prevent deleting checked out instance ([#45](https://github.com/aidan-bailey/loom/pull/45))
- *(session/git)* Include untracked files in the diff
- *(app)* Ensure autoyes mode still works ([#48](https://github.com/aidan-bailey/loom/pull/48))
- *(ui)* Prevent nil deref
- *(ui)* Remove limit from prompt text input ([#55](https://github.com/aidan-bailey/loom/pull/55))
- *(session/worktree)* Commit with `--no-verify`
- *(install.sh)* Fix windows platform detection
- *(worktree)* Better error message when using cs in an uninitialized repo
- *(worktree)* Traverse dirs to find repository root
- *(app)* Don't open UI when pausing
- Rename workflow file
- *(install.sh)* Upgrade if binary already installed
- *(release)* Add base to gorelease brew script
- Don't show empty state when agents exist ([#95](https://github.com/aidan-bailey/loom/pull/95))
- Show error message when exiting tmux session without detaching ([#110](https://github.com/aidan-bailey/loom/pull/110))
- Update binary version number, fix lint workflow
- Resolve claude binary path, accounting for command aliasing
- Initialize logger in app_test.go
- `c` Command Checkout Locally Only ([#123](https://github.com/aidan-bailey/loom/pull/123))
- *(session)* Retain stdout when pausing/checking out a session ([#177](https://github.com/aidan-bailey/loom/pull/177))
- Exempt from CLA directly
- Auto-accept claude trust prompt
- Make session creation faster
- Fix nil pointer dereference panic in debug command ([#196](https://github.com/aidan-bailey/loom/pull/196))
- Sanitize final branch name to handle invalid characters from branch prefix ([#221](https://github.com/aidan-bailey/loom/pull/221))
- Proper Unicode/Chinese character handling in UI ([#234](https://github.com/aidan-bailey/loom/pull/234))
- *(ui)* Remove extra background ([#255](https://github.com/aidan-bailey/loom/pull/255))
- Correct typosquatted org name in website install command ([#259](https://github.com/aidan-bailey/loom/pull/259))
- Update claude trust prompt handling ([#263](https://github.com/aidan-bailey/loom/pull/263))
- Resolve nil panics, lost state, and silent failures across core paths
- *(tmux)* Prevent panic on dead session detach and add defensive nil checks
- *(app)* Use workspace repo path instead of process cwd for worktree creation
- Resolve resume fallback, diff baseline loss, and pause state corruption
- *(app)* Auto-pause instances with dead tmux sessions instead of spamming errors
- Add DoesSessionExist check and make quick input discoverable
- *(app)* Create workspace terminal via workspace picker and review fixes
- *(ui)* Reset scroll mode when selected instance changes
- *(ui)* Prevent accidental scroll mode entry and auto-exit at bottom
- *(ui)* Scroll up one line when entering scroll mode
- Add ShiftTab, modifier+arrow, Insert mappings to keyMsgToBytes
- Trigger WindowSize on instance death during inline attach
- *(ui)* Prevent workspace indicators from blinking on workspace switch
- *(tmux)* Drain PTY output to prevent inline attach input deadlock
- *(keys)* Update tab help text from 'switch tab' to 'focus'
- *(ui)* Address code review findings
- *(ui)* Differentiate workspace tab annotations by session status
- *(ui)* Fix layout overflow and route inline attach to focused pane
- *(ui)* Add top border to split pane to prevent agent clipping
- *(ui)* Prevent pane shift when showing quick input bar
- *(app)* Preserve state transitions from help screen OnDismiss callbacks
- *(ui)* Add viewport windowing to session list to prevent screen scroll
- *(keys)* Remap vim navigation from jkl; to standard hjkl
- *(ui)* Refresh terminal pane independently of agent content hash
- *(app)* Move state mutations out of Cmd goroutines to prevent data race crash
- *(tmux)* Replace panics in Detach with graceful error handling
- *(log)* Overhaul logging infrastructure
- *(app)* Skip Deleting instances in key handlers and metadata tick
- *(app)* Exclude Deleting instances from persistence on quit
- Update help screens and menu groups for removed keybindings
- *(config)* Write state.json atomically to survive mid-write crashes
- *(config)* Write config.json atomically
- *(config)* Write workspaces.json atomically
- *(daemon)* Write pidfile atomically
- *(config,session)* Address code review of phase 1 / phase 2 start
- *(session)* Guard Instance.diffStats and Branch with mutex
- *(session)* Guard tmuxSession/gitWorktree/started with mutex
- *(session)* Add GetBranch accessor and fuse Start's worktree writes
- *(session)* Serialize Instance fields under RLock in Snapshot
- *(session)* Make Kill idempotent, nil handles after cleanup
- *(session)* Make Start idempotent
- *(daemon)* Do not write state.json on shutdown
- *(daemon)* Respect per-instance AutoYes instead of forcing true
- *(app)* Resize slot components after tab bar update on workspace switch
- *(ui)* Size list title block for variable-width status icons
- *(ui)* Prevent terminal pane height overflow from wrapped lines
- *(session)* Wire logRecoveryAction into ReconcileAndRestore
- *(tmux)* Close prior PTY before Restore to stop FD leak
- *(session)* Stop agent on pause and resume with --continue
- *(app)* Reconcile instances on workspace activation
- *(session)* Match claude by basename in BuildRecoveryCommand
- *(session)* Add subprocess timeouts to prevent indefinite UI hangs
- *(app)* Run metadata gather off update goroutine
- *(app)* Run terminal-pane cleanup off update goroutine for kill/pause
- *(app)* Run list kill off update goroutine
- *(app)* Run instance resume off update goroutine
- *(app)* Guard Resume paused status at call site
- *(app)* Flip to Loading on pause confirm so the spinner shows
- *(session)* Avoid PTY spawn in DeleteInstance and UpdateInstance
- *(cmd)* Surface corrupt data during workspace migrate
- *(git)* Block path traversal in sanitizeBranchName
- *(git)* Delete branches during CleanupWorktrees
- *(session)* Close TOCTOU in Start idempotency guard
- *(config)* Write .gitignore atomically in EnsureGitignore
- *(script)* Close ResumeWithHost race and QuitIntent coroutine leak
- *(app)* Surface previously-swallowed TransitionTo and registry errors
- *(tmux)* Bound pump-exit wait in Close/Restore/PausePreview
- *(daemon)* Bound per-instance tick to isolate wedged sessions
- *(script)* Cs.await with no args yields last-enqueued intent id
- *(config)* Quarantine corrupt state/config on parse failure
- *(app)* Unify handleQuit save-error policy across slot paths
- *(session)* Propagate Pause/Resume saveState errors
- *(session)* Classify branch-gone errors and hint recovery on Resume
- *(git)* Use fresh context for fallback push after gh sync
- *(tmux)* Initialize monitor in constructor
- *(script)* Honour lastEnqueued in bare cs.await()
- *(script)* Align scroll bindings with approved plan
- *(log)* Return init errors, stderr rotation, and replace racy timer
- *(app)* Return startup errors via cobra RunE instead of os.Exit
- *(session)* Join errors and propagate CaptureAndProcess failures
- *(git)* Log non-absent worktree cleanup failures
- *(tmux)* Interrupt blocked pump Read via SetReadDeadline on stop
- *(daemon)* Bound per-daemon goroutine pool across ticks
- *(app)* Kill orphan tmux before workspace terminal auto-create
- *(config)* Emit both-dirs-present warning to stderr, add migration tests
- *(daemon)* Eliminate tickInstanceTimeout race

### 💼 Other

- Add menu
- Working on layout
- Layout and up/down keystrokes done
- Added error message and finished list except for new
- Add spinners
- Add comments to spinner
- Add comments to spinner in list component
- Improve menu visibility
- Add ready icon
- Improve error styling
- Don't wrap list navigation
- Add tmux session interface
- Add worktree, pushing branch
- Context switching in/out working
- Fix window sizes ([#2](https://github.com/aidan-bailey/loom/pull/2))
- Separate start from struct initialization
- Add ability to enter name for session ([#4](https://github.com/aidan-bailey/loom/pull/4))
- Update empty state
- Add tabs to ui and render preview in tabs
- Add tab switching
- Make claudesquad two words in fallback text for small terminals
- Update instance styles to use background with no border ([#6](https://github.com/aidan-bailey/loom/pull/6))
- Show branch name in list
- Nuke control characters when attaching
- Clarify paused state better
- Allow for quitting while paused ([#8](https://github.com/aidan-bailey/loom/pull/8))
- Update statuses depending on claude stdout ([#7](https://github.com/aidan-bailey/loom/pull/7))
- Add agpl 3.0 license
- Skip 'do you trust files' screen in instance
- Add git diff +- to list card
- Show repo name when multiple repos are used ([#10](https://github.com/aidan-bailey/loom/pull/10))
- Add yolo mode ([#12](https://github.com/aidan-bailey/loom/pull/12))
- Make auto-yes mode config / cli flag only ([#13](https://github.com/aidan-bailey/loom/pull/13))
- Add log package to introduce logging channels ([#11](https://github.com/aidan-bailey/loom/pull/11))
- Make diff scroll text smaller
- Remove extra whitespace from preview window
- Add vibe coded readme file ([#14](https://github.com/aidan-bailey/loom/pull/14))
- Introduce autoyes daemon ([#16](https://github.com/aidan-bailey/loom/pull/16))
- Mention that autoyes works for claude code and aider only
- Update auto-yes text search

### 🚜 Refactor

- *(config)* Use config directory everywhere
- Convert --reset flag to reset subcommand
- *(help screen)* Improve enums ([#168](https://github.com/aidan-bailey/loom/pull/168))
- *(config)* Add WorkspaceContext type and config directory helpers
- Replace env var propagation with explicit workspace context threading
- Simplify config loading, remove redundant state, and fix consistency issues
- *(ui)* Remove TabbedWindow (replaced by SplitPane)
- Remove tab, i, enter/o, shift+up/down keybindings
- Route all Instance.Status access through Get/SetStatus
- *(session)* Snapshot IsWorkspaceTerminal under Kill's lock
- *(keys)* Remap to vim keys k/j for nav, l/; for workspace tabs
- *(tmux)* Replace custom attach with tea.ExecProcess
- *(cmd)* Plumb cmd.Executor through git worktree ops
- *(session)* Add Status state machine with allow-list transitions
- *(session)* Introduce SessionBackend for worktree vs terminal routing
- *(ui)* Centralize theme colors and layout constants
- *(overlay)* Add Overlay interface with unified HandleKey
- *(config)* Require explicit dir in Load helpers and thread WorkspaceContext
- *(app)* Adopt overlay/theme/status primitives and add ActionRegistry
- *(theme,help,log)* Consolidate colors, unify help text, guard Every mutex
- *(config,daemon,session)* Thread WorkspaceContext, dedup Executor, drop SetStatus shim
- *(app)* Finish ActionRegistry migration; delete handleKeyPress switch
- *(overlay)* Collapse four overlay pointers to one + kind tag
- *(app)* Split handleKeyPress into per-state handlers
- *(session)* Introduce AgentAdapter consolidating per-agent logic
- *(session,daemon)* Decouple FromInstanceData from PTY attach (DAEMON-05)
- *(storage,cmd)* Add SchemaVersion + Migrate, harden workspace migrate
- *(config,log)* Remove Config.configDir, add slog backend + test gaps
- *(script)* Cs.register_action becomes thin alias over cs.bind
- Retire Go ActionRegistry in favor of Lua-driven keymap
- Migrate legacy Printf sites to structured log.For
- Rename Go module to github.com/aidan-bailey/loom
- Rename runtime identifiers and add claude-squad → loom migration
- Update release infrastructure for loom rebrand

### 📚 Documentation

- Update CONTRIBUTING.md
- Update README to reflect CLI usage
- Update README.md
- Update README.md
- Add demo video to README
- Update README.md
- Add homebrew installation steps
- Update README.md
- Fix Gemini CLI link
- Add CLAUDE.md with project guidance for Claude Code
- Update CLAUDE.md with CLI usage, env vars, and fix session lifecycle
- Update CLAUDE.md with workspace feature and fix outdated details
- Add workspaces spec and link from CLAUDE.md
- Update workspace spec for WorkspaceContext and new CLI commands
- Update CLAUDE.md for workspace context refactoring and new CLI commands
- Add comprehensive USAGE.md with TUI layout, lifecycle, and CLI reference
- Update CLAUDE.md with keybindings, workspace terminals, CI, and missing references
- Update keybindings for inline attach and fullscreen modes
- Add interactive preview design and implementation plan
- Add design for diff tab removal and split pane layout
- Add implementation plan for diff tab removal
- Add design for immediate UI feedback on session deletion
- Add implementation plan for immediate delete UI feedback
- Add design for removing tab-based pane selection
- Add implementation plan for removing tab-based pane selection
- Update keybinding references for removed keys
- Add implementation plan for race condition fixes
- *(daemon)* Cleanup orphaned shutdown comment, note DAEMON-05 cost
- Add crash recovery design for OOM/unclean shutdown resilience
- Add crash recovery implementation plan
- Refresh CLAUDE.md for action dispatch + schema/log additions
- Document Lua scripting package and gotchas
- Add scripting spec and link from CLAUDE.md index
- *(plans)* Scriptable hotkeys migration design
- *(plans)* Scriptable hotkeys implementation plan
- Update scripting spec for bind/actions/await migration
- *(rebrand)* Rewrite docs, web site, and user-facing strings for loom
- *(usage)* Add workspace terminals, profiles, branch prefix, daemon troubleshooting
- *(go)* Add package comments to core packages
- *(session/git)* Document worktree-from-storage constructors and DiffStats.IsEmpty
- *(config)* Document Profile struct and fields
- *(session)* Elevate Instance, NewInstance, and Migrate docstrings
- *(session/tmux)* Document program constants, TmuxSession, and PTY factory
- *(script)* Document intent struct types and attach-pane constants
- *(log)* Document legacy logger vars and NewEvery
- *(app,keys)* Elevate Run, GlobalInstanceLimit, and KeyName/GlobalkeyBindings
- *(ui)* Document List, SplitPane, PreviewPane, DiffPane, TerminalPane
- *(ui/overlay)* Add package comment
- *(ui,keys)* Fill remaining exported-symbol gaps
- *(session/agent)* Document adapter method implementations
- *(app)* Document scriptHost and home Bubble Tea model methods
- *(session,tmux,log,config)* Fill remaining exported-symbol gaps

### ⚡ Performance

- Improve worktree startup time ([#256](https://github.com/aidan-bailey/loom/pull/256))
- Optimize navigation with 8 behavior-preserving improvements
- *(tmux)* Dedup hash and remove pane buffer alloc
- *(app)* Skip diff refresh when tmux pane unchanged
- *(git)* Cache untracked-file probe in diff path
- *(config)* Skip atomic write when state bytes unchanged
- *(daemon)* Parallelise auto-yes checks with worker bound
- *(git)* Rate-limit git.cmd.ok debug stream

### 🎨 Styling

- Fix gofmt alignment in session/instance.go
- Gofmt formatting fixes
- *(script)* Tidy space after comma in engine test Dispatch calls

### 🧪 Testing

- *(tmux)* Add test for name sanitization ([#85](https://github.com/aidan-bailey/loom/pull/85))
- Add tests for config.go
- Add workspace picker overlay tests
- *(app)* Add tests for Deleting status, kill failure revert, and persistence filtering
- Remove scroll handler test for removed keybindings
- *(tmux)* Lock multi-word program argv delivery
- *(app)* Migration parity for retired hotkeys
- *(cmd)* Guard workspace_migrate mirror against field-type drift
- *(session)* Verify Kill returns bounded on stuck tmux pump
- *(script)* Guard cs.bind reserved-key silent-drop behavior
- *(log)* Cover rotateIfNeeded startup and Every rate limiter
- *(ui)* Cover list page navigation cursor math
- *(ui)* Cover pane-level scroll mode entry/exit invariants
- *(git)* Skip fresh-context test when gh CLI not on PATH

### 🔧 Build

- *(nix)* Add flake with package and dev shell
- Update vendorHash and vendor directory for new dependencies
- *(deps)* Vendor gopher-lua; prune unused indirect deps
- *(nix)* Use committed vendor/ directly instead of fixed-output hash

### ⚙️ Miscellaneous Tasks

- *(config)* Add default config, allow specifying program to spawn
- Add build + release workflows, install script ([#3](https://github.com/aidan-bailey/loom/pull/3))
- Add lint workflow, run gofmt
- Use go version 1.23 in ci
- *(readme)* Update screenshot
- *(gitignore)* Add DS_Store to gitignore
- Remove unused init code
- *(ui)* Fix auto-yes mode indicator
- *(readme)* Update readme with more usage details
- Update gorelease config
- Add version command
- Install binary as `cs` and update README
- *(readme)* Update screenshot with new menu and auto yes card ([#25](https://github.com/aidan-bailey/loom/pull/25))
- *(readme)* Use hd screenshot
- *(keys)* Shift + D to kill an instance
- *(daemon, log)* Clean up error logging ([#31](https://github.com/aidan-bailey/loom/pull/31))
- *(app)* Error handling updates ([#32](https://github.com/aidan-bailey/loom/pull/32))
- Remove unused file
- *(log)* Remove extraneous log line
- *(readme)* Put install script above features
- Update install.sh to install dependencies
- Bump version
- *(daemon)* Remove noisy log
- *(tmux)* Add test infrastructure and basic test for starting tmux session ([#92](https://github.com/aidan-bailey/loom/pull/92))
- *(release)* Use goreleaser action v6
- *(release)* Set skip_upload:false for goreleaser to ensure rb file is pushed
- *(release)* Use proper token for cross repo PR generation
- *(ci)* Don't unnecessarily run actions for non-code changes
- *(ci)* Don't override GITHUB_TOKEN env var
- Cache golangci-lint action step
- Update program description
- Use golangci-lint v1.60.0
- Run CI workflows when workflow definition changes
- Bump version
- *(brew)* Remove brew tap
- Add CLA and CLA action ([#128](https://github.com/aidan-bailey/loom/pull/128))
- Update CLA message
- *(readme)* Add faq section ([#169](https://github.com/aidan-bailey/loom/pull/169))
- Add script to bump app version
- Add exceptions for CLA
- Verify release tag matches binary version
- Bump version
- Bump version
- Bump version to 1.0.17
- Add result dir to .gitignore
- Add race-detector workflow
- Drop obsolete attach plumbing after tea.ExecProcess migration
- Add .serena to gitignore
- *(ci)* Remove GitHub Pages deployment
- *(release)* Adopt git-cliff changelog and auto-triggered release
- *(build)* Upgrade setup-go action to v5
- *(lint)* Exclude vendor/ from gofmt check
- *(ci)* Enforce revive.exported docstrings across the repo
- Gitignore git-cliff/ and .claude-squad/
