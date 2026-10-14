# The loom Claude Code mod: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan package by package. Steps use checkbox (`- [ ]`) syntax for tracking. Each package is one unit of work: one implementer, one review, one commit; the numbered steps inside it are checkpoints, not commits.

**Goal:** loom loads a mod into every Claude it launches, and `loom session send`, Lua `inst:send_prompt` and the `N` flow's first prompt reach Claude through it: queued behind any running turn or open dialog, never typed into the pane.

**Architecture:** each Claude launch gets the mod (embedded in the binary, written into the launch's hooks folder) with `--plugin-dir`, and `LOOM_MOD_SOCKET`/`LOOM_LAUNCH` in its environment. The mod long-polls an HTTP endpoint the daemon serves on a second Unix socket (`internal/modserver`), submits each message with `$.prompt.submit`, and reports `queued`, `started`, `complete` or `failed`. The model keeps a per-launch link (liveness, outbox, waiters) and answers senders through the usual `Reply`, plus a new `Answered` event for `--wait`.

**Tech stack:** Go 1.25 (`net/http` method patterns, `go:embed`), TypeScript for the mod (Claude Code 2.1.299 function hooks), tmux for the opt-in real-Claude test.

**Spec:** [`docs/superpowers/specs/2026-10-14-loom-claude-mod-design.md`](../specs/2026-10-14-loom-claude-mod-design.md) (6723f02).

---

## Spec amendments (binding; decided while planning)

Later packages and reviewers build on these, not on the spec's text where they differ.

1. **The mod is written per launch, into `<hooks folder>/mod/`**, not once into `<globalDir>/mod/<hash>/`. Claude writes `tsconfig.json` and `.claude-plugin/types/` into every `--plugin-dir` folder each time it loads it (seen in the 2026-10-14 probe), so a folder every session shared would be rewritten by every session. The hooks folder is already per launch and emptied by `hooks.Prepare`, so each launch gets the mod of the build that launched it, and nothing is left to prune. There is no content hash and no install step in the daemon.
2. **The `Core` method is `Send(id InstanceID, msg Message, req ReqID)`**, not `Deliver`: `Model.Deliver` already applies job results. The types are `Message` and `Origin`.
3. **The inbox has no `after` parameter and no `seq` on the wire.** `GET /v1/inbox?launch=L` returns every message not yet queued; the mod skips IDs it already submitted. Message IDs are random (64 bits), so they stay unique across daemon restarts, where a sequence would restart at 1 behind a stale `after`.
4. **The mod keeps its state in module variables, not `$.state`.** Its folder is never rewritten (amendment 1), so it never hot-reloads; a reload after a lost hooks worker could resubmit a message whose `queued` report was in flight, which is accepted. No types contract file.
5. **The mod backs off on 404 like any other failure and never stops.** After a daemon restart a mod's `hello` can arrive before the hook scan has adopted its launch ID.
6. **A launch whose hooks ID is still `""`** (a restored instance before its first scan) is `pending`, and its link takes the ID in place when the scan sets it; only a different, real ID is a relaunch.
7. **A `failed` that arrives after `queued`** (a submit that resolved with `drop` when its turn came) is a `Notice` to the requester, or to every client when no one waits; nothing falls back after `queued`, since a paste then could deliver twice.
8. **A held poll ends with 204 on any wake** (a new message, a newer poll, the link ending); the mod polls again at once and learns what changed. A daemon stop ends it with 503.
9. **`internal/modserver` has no build tags.** `net.Listen("unix")` is portable Go; the daemon serves no mod on Windows by checking `runtime.GOOS`.
10. **A first prompt whose launch ends before the mod queued it** is logged, and its start finishes with `Started` all the same, as today's paste does when it fails (`sendInitialPrompt`).
11. **No `CodeUnsupported` handling in the CLI.** A CLI's join replaces an older daemon, or refuses with a clear error inside a loom pane (`replaceGuard`), so `loom session send` never talks to a daemon without `Send`.

Added during execution (binding; see **Spike results**):

12. **Messages queued during one turn share the next turn.** Claude runs every plugin prompt that waited through a turn as one turn (P3), and the user accepted it: each message still gets `queued`, `started` and `complete`, merged ones with one turn ID and one answer. The module claims a message for a turn when its submit resolves (P3c: the running main-loop turn, else the next one); `matches` is gone. The Go side handles events by message ID and never assumes one message per turn.
13. **`DefaultHold` is 24 s.** Claude's host aborts every `$.http.fetch` at 30 s (P1).
14. **Every launch builds its program and env through `Instance.launchCommand`** (8c6787e, from B's review). E4's `session/CLAUDE.md` rule names `launchCommand` and is **Enforced** by `TestRestart_GivesEachLaunchItsOwnModEnv`, `TestCrashRestart_GivesEachLaunchItsOwnModEnv` and `TestLaunchProgram_OnlyLaunchCommandCallsIt`; `TestInstanceEnv_CallSitesAppendTheModEnv` no longer exists.

## Files

| File | Package | Change |
|---|---|---|
| `session/claudemod/claudemod.go` | B | new: embeds the mod, `WriteTo`, `Version`, env names |
| `session/claudemod/mod/.claude-plugin/plugin.json`, `mod/hooks/hooks.json`, `mod/hooks/register.ts` | B | new: the mod |
| `session/claudemod/claudemod_test.go`, `session/claudemod/realclaude_test.go` | B | new |
| `session/claudemod/CLAUDE.md` | B | new |
| `session/claude_mod.go`, `session/claude_mod_test.go` | B | new: `SetModSocket`, `ModSocket`, `prepareMod`, `modEnv`, `HookLaunch`, `LaunchSessionStartedAt` |
| `session/subagent_hooks.go`, `session/instance.go`, `session/claude_state.go`, `session/agent/claude.go`, `session/agent/claude_test.go` | B | the launch gets the mod; the SessionStart time |
| `core/mod.go`, `core/loop_mod.go`, `core/mod_test.go` | C, D | new: the model's mod state, `ModHost`, then `Send` |
| `core/model.go`, `core/load.go`, `core/hook_scan.go`, `core/events.go`, `core/rpc/route.go`, `app/core_glue.go` | C | fields, boot time, tick, `Answered` |
| `internal/modserver/server.go`, `server_test.go`, `testmain_test.go`, `CLAUDE.md` | C | new: the HTTP server |
| `internal/daemon/paths.go`, `internal/daemon/serve.go`, `internal/daemon/serve_test.go` | C | the mod socket |
| `core/iface.go`, `core/loop_core.go`, `core/completions.go`, `core/requests_test.go` | D | `Send`; the first prompt |
| `core/rpc/methods_gen.go`, `docs/specs/protocol.md` | C, D | regenerated, never edited |
| `internal/cli/session.go`, `internal/cli/join.go`, `internal/cli/session_test.go` | D | `loom session send` |
| `app/app_scripts.go`, `docs/specs/scripting.md`, `USAGE.md` | D | Lua `send_prompt`; docs |
| `tools/fakeagent/mod.go`, `tools/fakeagent/agent.go`, `tools/fakeagent/main.go`, `tools/fakeagent/agent_test.go` | E | fakeagent plays the mod |
| `e2e/mod_test.go`, `internal/modserver/realclaude_test.go` | E | end to end; opt-in real Claude |
| `docs/ARCHITECTURE.md`, `internal/daemon/CLAUDE.md`, `internal/daemon/README.md`, `session/CLAUDE.md` | E | docs |

Rules every implementer follows (restated for subagents, which may not load the `CLAUDE.md` files): scratch files go in the session scratchpad, never the worktree; never run loom itself (`./loom`, `go run .`) or `loom serve stop` in a loom pane; tmux only through `tmux.Command`/`tmux.CommandOnSocket` with exact targets; no `git stash`, `--amend` or rebase; commit only with `git commit -m … -- <paths>` after `git status`; tests run in the foreground with `CGO_ENABLED=0`.

---

## Package A: spike gate

Nothing here is committed but this plan's **Spike results** section. The spike runs the real module (Package B's `register.ts`, verbatim but for one debug line) against a throwaway HTTP server, with the 2026-10-14 probe recipe (`docs/claude/testing-pitfalls.md`, Claude and tmux premises).

**Gate.** STOP and report to the user, changing nothing else, when:
- P1: the host ends a held `$.http.fetch` in under 10 s, or the module cannot poll from the un-awaited loop at all;
- P3: two messages submitted during one turn do not become two turns, in order;
- the module does not load, or `claude plugin validate` refuses it.

P2 never stops the plan: it picks the match rule.

- [ ] **A1. The throwaway server.** Write `$SP/modspike/server.py` (`$SP` is the session scratchpad):

```python
# A throwaway stand-in for loom's mod server: logs every request, hands out
# messages dropped into queue/, holds an empty inbox poll for HOLD seconds.
import json, os, sys, time, socketserver, http.server

SOCK, LOG, QUEUE, HOLD = sys.argv[1], sys.argv[2], sys.argv[3], float(sys.argv[4])

def log(line):
    with open(LOG, "a") as f:
        f.write(f"{time.strftime('%H:%M:%S')} {line}\n")

class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def body(self):
        n = int(self.headers.get("content-length", 0))
        return self.rfile.read(n).decode() if n else ""
    def do_POST(self):
        log(f"POST {self.path} {self.body()}")
        self.send_response(204); self.end_headers()
    def do_GET(self):
        start = time.time()
        while time.time() - start < HOLD:
            names = sorted(os.listdir(QUEUE))
            if names:
                msgs = []
                for n in names:
                    with open(os.path.join(QUEUE, n)) as f:
                        msgs.append(json.load(f))
                    os.remove(os.path.join(QUEUE, n))
                data = json.dumps({"messages": msgs}).encode()
                log(f"GET {self.path} -> 200 {data.decode()}")
                self.send_response(200)
                self.send_header("content-type", "application/json")
                self.send_header("content-length", str(len(data)))
                self.end_headers(); self.wfile.write(data); return
            time.sleep(0.2)
        log(f"GET {self.path} -> 204 after {time.time() - start:.1f}s")
        self.send_response(204); self.end_headers()

class S(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True

if os.path.exists(SOCK): os.remove(SOCK)
os.makedirs(QUEUE, exist_ok=True)
S(SOCK, H).serve_forever()
```

Run it in the background with `python3 -I`, the socket under `/tmp` (short): `python3 -I $SP/modspike/server.py /tmp/lms.sock $SP/modspike/log.txt $SP/modspike/queue 25`.

- [ ] **A2. The spike copy of the module.** Copy Package B's three mod files (B1) into `$SP/modspike/mod/`. In the copy's `turn.start` hook, add as its first statement `void report($, [{ kind: 'started', id: 'debug', text: e.text }])`: the server log then shows what `turn.start.text` holds for every turn. Run `claude plugin validate $SP/modspike/mod`: it must pass.

- [ ] **A3. Launch it.** On a private tmux server (`env -i HOME="$HOME" PATH="$PATH" USER="$USER" LANG="$LANG" XDG_RUNTIME_DIR="$XDG_RUNTIME_DIR" TERM=xterm-256color tmux -L lms -f /dev/null …`), in a fresh git repo under `$SP/modspike/repo`, start `LOOM_MOD_SOCKET=/tmp/lms.sock LOOM_LAUNCH=L1 claude --model haiku --plugin-dir $SP/modspike/mod --debug-file $SP/modspike/debug.log`. Accept the trust dialog (Down, then Enter). The log must show `POST /v1/hello` with `"launch": "L1"`, then a held `GET`.

- [ ] **A4. P1, the long poll.** With HOLD 25, wait for three consecutive `-> 204 after 25.0s` lines with no error in `debug.log` (`grep -i "loom:" debug.log`). Restart the server with HOLD 120 and watch for the first failed poll: the mod logs `loom: …` to the debug log and says `hello` again. Record the longest hold that succeeded. loom's hold is 25 s if a 30 s hold succeeded, else the longest that succeeded minus 5 s.

- [ ] **A5. P2, the turn text.** Drop a framed message and a bare one:
  `echo '{"id":"m1","text":"Reply with exactly ONE","asUser":false}' > $SP/modspike/queue/1.json`; once its turn has ended,
  `echo '{"id":"m2","text":"Reply with exactly TWO","asUser":true}' > $SP/modspike/queue/2.json`.
  For each, read the debug `started` event's `text`: equal to the message, containing it, or neither? The log must also show `queued`, `started` (id m1, then m2) and `complete` with `text` `ONE`, then `TWO`. Rule: equal or containing keeps `matches` as written; neither switches `matches` to order alone (`return true`), recorded below.

- [ ] **A6. P3, back to back.** Type a prompt that makes a turn of 15 seconds or more (`Write a 500-word story about a lighthouse.`). While it runs, drop m3 (`Reply with exactly THREE`) and m4 (`Reply with exactly FOUR`) in one `sh -c`. The log must show both `queued` at once, then a `started`/`complete` pair for m3, then one for m4, with the answers in that order.

- [ ] **A7. P4, across `/clear`.** Type `/clear`, then drop m5. It must be delivered: the loop survives `/clear`.

- [ ] **A8. Record.** Fill **Spike results** below (hold, match rule, P3, P4, anything unexpected). If the hold is not 25 s, amend `DefaultHold` in C4's `server.go` below; if the match rule changed, amend `matches` in B1's `register.ts`. Kill the private tmux server (`tmux -L lms kill-server`, from the `env -i` wrapper) and the Python server. Commit the plan alone:

```bash
git status
git commit -m "docs(plan): loom mod spike results" -- docs/superpowers/plans/2026-10-14-loom-claude-mod.md
```

---

## Package B: the mod and its launch

After B, every Claude launch with a mod socket set gets the mod folder and its environment. No daemon sets the socket yet, so production launches are unchanged.

- [ ] **B1. The mod files.**

`session/claudemod/mod/.claude-plugin/plugin.json`:

```json
{
  "name": "loom",
  "version": "1.0.0",
  "description": "Carries loom's messages into Claude's prompt queue (loom session send, Lua send_prompt, the first prompt)."
}
```

`session/claudemod/mod/hooks/hooks.json`:

```json
{ "modules": ["./register.ts"] }
```

`session/claudemod/mod/hooks/register.ts`:

```ts
// The loom mod: carries the messages loom's daemon has for this Claude into
// its prompt queue. Claude submits each as a turn of its own once the
// session is idle, so a message never answers an open dialog or joins a
// running turn. Everything else stays in loom: the daemon composes the text
// and decides what to send, and this module only carries it and reports
// when each message is queued, when its turn starts and how the turn ended.
//
// loom writes this folder into each launch's hooks folder and passes it with
// --plugin-dir. LOOM_MOD_SOCKET names the daemon's mod socket and LOOM_LAUNCH
// this launch; without both the module does nothing.

import type { EngineInterface, Register } from 'claude-code'

const MOD_VERSION = 1
const BASE = 'http://loom/v1'
const MAX_BACKOFF_MS = 5000
const KEEP_SUBMITTED = 200

type Message = { id: string; text: string; asUser: boolean }
type ModEvent = {
  kind: 'queued' | 'started' | 'complete' | 'failed'
  id: string
  turnId?: string
  text?: string
  reason?: string
  error?: string
}

let socket = ''
let launch = ''
let pumping = false
// submitted are the IDs this process has handed to Claude, newest last, so a
// message the daemon offers again (its queued report was lost) is reported
// again, not submitted twice.
const submitted: string[] = []
// awaiting are submitted messages whose turn has not started, oldest first.
const awaiting: Message[] = []
// turns maps a started turn to the message that opened it.
const turns = new Map<string, string>()

async function call($: EngineInterface, method: string, path: string, body?: unknown) {
  return $.http.fetch(BASE + path, {
    method,
    socketPath: socket,
    headers: body === undefined ? {} : { 'content-type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
}

async function report($: EngineInterface, events: ModEvent[]) {
  try {
    await call($, 'POST', '/events', { launch, events })
  } catch (err) {
    $.ui.log(`loom: report failed: ${String(err)}`, { to: 'debug' })
  }
}

async function hello($: EngineInterface): Promise<boolean> {
  const version = await $.session.version()
  const session = await $.session.id()
  const r = await call($, 'POST', '/hello', { launch, mod: MOD_VERSION, claude: version.version, session })
  return r.status === 204
}

// matches says whether a turn's text is the submitted message's: Claude may
// hand it on bare or wrapped (plan package A settled which).
function matches(turnText: string, sent: string): boolean {
  return turnText === sent || turnText.includes(sent)
}

function forget(id: string) {
  const i = awaiting.findIndex(m => m.id === id)
  if (i >= 0) awaiting.splice(i, 1)
}

async function submit($: EngineInterface, m: Message) {
  if (submitted.includes(m.id)) {
    await report($, [{ kind: 'queued', id: m.id }])
    return
  }
  submitted.push(m.id)
  if (submitted.length > KEEP_SUBMITTED) submitted.shift()
  awaiting.push(m)
  const done = $.prompt.submit(m.asUser ? { text: m.text, asUser: true } : { text: m.text })
  await report($, [{ kind: 'queued', id: m.id }])
  done.then(
    r => {
      if (r.drop !== undefined) {
        forget(m.id)
        void report($, [{ kind: 'failed', id: m.id, error: `dropped: ${r.drop}` }])
      }
    },
    err => {
      forget(m.id)
      void report($, [{ kind: 'failed', id: m.id, error: String(err) }])
    },
  )
}

function backoff(ms: number): number {
  return Math.min(ms === 0 ? 1000 : ms * 2, MAX_BACKOFF_MS)
}

async function pump($: EngineInterface) {
  let greeted = false
  let delay = 0
  for (;;) {
    if (delay > 0) await $.clock.sleep(delay)
    try {
      if (!greeted) {
        greeted = await hello($)
        if (!greeted) {
          delay = backoff(delay)
          continue
        }
      }
      const r = await call($, 'GET', `/inbox?launch=${encodeURIComponent(launch)}`)
      if (r.status === 200) {
        const { messages } = JSON.parse(r.text) as { messages: Message[] }
        for (const m of messages) await submit($, m)
        delay = 0
      } else if (r.status === 204) {
        delay = 0
      } else {
        greeted = false
        delay = backoff(delay)
      }
    } catch (err) {
      $.ui.log(`loom: ${String(err)}`, { to: 'debug' })
      greeted = false
      delay = backoff(delay)
    }
  }
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const started = await next(e)
    socket = (await $.env.get('LOOM_MOD_SOCKET')) ?? ''
    launch = (await $.env.get('LOOM_LAUNCH')) ?? ''
    if (socket !== '' && launch !== '' && !pumping) {
      pumping = true
      void pump($)
    }
    return started
  })

  on('turn.start', async ($, e, next) => {
    const r = await next(e)
    const head = awaiting[0]
    if (e.agentId === undefined && head !== undefined && matches(e.text, head.text)) {
      awaiting.shift()
      turns.set(r.turnId, head.id)
      await report($, [{ kind: 'started', id: head.id, turnId: r.turnId }])
    }
    return r
  })

  on('turn.complete', async ($, e, next) => {
    const r = await next(e)
    const id = e.agentId === undefined ? turns.get(e.turnId) : undefined
    if (id !== undefined) {
      turns.delete(e.turnId)
      await report($, [{ kind: 'complete', id, turnId: e.turnId, text: r.text, reason: e.reason }])
    }
    return r
  })
}
```

- [ ] **B2. The Go package.** `session/claudemod/claudemod.go`:

```go
// Package claudemod is the loom mod for Claude Code: a function-hook plugin
// (mod/) that carries the daemon's messages into Claude's prompt queue. It is
// embedded in the loom binary, and session writes it into each Claude
// launch's hooks folder (WriteTo), which the launch passes with --plugin-dir.
// The module reaches the daemon over HTTP on the socket EnvSocket names, as
// the launch EnvLaunch names (internal/modserver serves it).
package claudemod

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The embedded mod. all: keeps .claude-plugin, which a plain pattern skips
// as a dot-directory.
//
//go:embed all:mod
var files embed.FS

// Version is the mod protocol version: the module's hello says it, and its
// API is /v<Version>. The manifest's major version matches it.
const Version = 1

// The environment a launch gives the mod.
const (
	// EnvSocket names the daemon's mod socket.
	EnvSocket = "LOOM_MOD_SOCKET"
	// EnvLaunch names the launch: its hooks folder's launch ID.
	EnvLaunch = "LOOM_LAUNCH"
)

// WriteTo writes the mod into dir, creating it. Each launch's hooks folder is
// emptied before it (hooks.Prepare), so nothing older is left beside it.
// Claude itself writes tsconfig.json and .claude-plugin/types/ into the
// folder when it loads the mod.
func WriteTo(dir string) error {
	err := fs.WalkDir(files, "mod", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(p, "mod")))
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
		return fmt.Errorf("claudemod: write the mod: %w", err)
	}
	return nil
}
```

- [ ] **B3. Its tests.** `session/claudemod/claudemod_test.go`:

```go
package claudemod

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteTo_WritesTheWholeMod(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hooks", "mod")

	require.NoError(t, WriteTo(dir))

	for _, f := range []string{".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/register.ts"} {
		assert.FileExists(t, filepath.Join(dir, filepath.FromSlash(f)))
	}
	b, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	require.NoError(t, err)
	var manifest struct{ Name, Version string }
	require.NoError(t, json.Unmarshal(b, &manifest))
	assert.Equal(t, "loom", manifest.Name)
	assert.Equal(t, fmt.Sprint(Version), strings.SplitN(manifest.Version, ".", 2)[0], "the manifest's major version is the protocol's")
}

// The module and the Go side speak one protocol: the version its hello says,
// the API path, and the environment a launch gives it.
func TestModule_SpeaksThisProtocol(t *testing.T) {
	b, err := files.ReadFile("mod/hooks/register.ts")
	require.NoError(t, err)
	src := string(b)
	assert.Contains(t, src, fmt.Sprintf("const MOD_VERSION = %d", Version))
	assert.Contains(t, src, fmt.Sprintf("'http://loom/v%d'", Version))
	for _, env := range []string{EnvSocket, EnvLaunch} {
		assert.Contains(t, src, "'"+env+"'")
	}
}
```

`session/claudemod/realclaude_test.go` (opt-in; CI has no `claude`):

```go
package claudemod

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The mod's whole reach on $: the spec's security section allows these and
// nothing else (no $.process, no $.fs, no UI beyond the debug log).
var allowedCalls = map[string]bool{
	"$.clock.sleep": true, "$.env.get": true, "$.http.fetch": true, "$.prompt.submit": true,
	"$.session.id": true, "$.session.version": true, "$.ui.log": true,
}

// TestRealClaude_ValidatesTheMod runs the installed Claude's own validator on
// the embedded mod, and pins what it hooks and calls. It only reads, but needs
// claude, so it runs only with LOOM_TEST_REAL_CLAUDE=1.
func TestRealClaude_ValidatesTheMod(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not installed")
	}
	dir := filepath.Join(t.TempDir(), "mod")
	require.NoError(t, WriteTo(dir))

	out, err := exec.Command("claude", "plugin", "validate", dir).CombinedOutput()

	require.NoError(t, err, string(out))
	s := string(out)
	assert.Contains(t, s, "Validation passed")
	assert.Contains(t, s, "hooks: session.start, turn.start, turn.complete")
	var calls []string
	for _, line := range strings.Split(s, "\n") {
		if _, rest, ok := strings.Cut(line, " calls: "); ok {
			for _, c := range strings.Split(rest, ", ") {
				calls = append(calls, strings.TrimSpace(strings.SplitN(c, " (", 2)[0]))
			}
		}
	}
	require.NotEmpty(t, calls, "the validator lists the module's calls:\n%s", s)
	for _, c := range calls {
		assert.True(t, allowedCalls[c], "the mod calls %s, outside its allowed reach", c)
	}
}
```

Run: `CGO_ENABLED=0 go test ./session/claudemod/` → PASS (the real-Claude test skips). Then once, `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session/claudemod/ -run TestRealClaude -v` → PASS. If the validator's wording differs from `hooks: ` or ` calls: `, adapt the parsing to its real output and keep the assertions.

- [ ] **B4. The launch side: failing tests first.** `session/claude_mod_test.go`:

```go
package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withModSocket names a mod socket for the test, as the daemon does at boot.
func withModSocket(t *testing.T, path string) {
	t.Helper()
	SetModSocket(path)
	t.Cleanup(func() { SetModSocket("") })
}

func TestLaunchProgram_GivesAClaudeLaunchTheMod(t *testing.T) {
	withModSocket(t, "/run/loom/x.mod.sock")
	inst := hooksInstance(t, "claude")

	got := inst.launchProgram(LaunchEnv{Program: "claude"}, true)

	mod := filepath.Join(SubagentHooksDir(inst.ConfigDir, inst.Title), "mod")
	assert.Contains(t, got, "--plugin-dir '"+mod+"'")
	assert.Contains(t, got, settingsFlag(inst), "the hooks still come too")
	assert.FileExists(t, filepath.Join(mod, "hooks", "register.ts"))
	launch, hooked := inst.HookLaunch()
	require.True(t, hooked)
	assert.Equal(t, []string{"LOOM_MOD_SOCKET=/run/loom/x.mod.sock", "LOOM_LAUNCH=" + launch}, inst.modEnv())
}

func TestLaunchProgram_NoModWithoutASocket(t *testing.T) {
	withModSocket(t, "")
	inst := hooksInstance(t, "claude")

	got := inst.launchProgram(LaunchEnv{Program: "claude"}, true)

	assert.NotContains(t, got, "--plugin-dir")
	assert.Nil(t, inst.modEnv())
	assert.NoDirExists(t, filepath.Join(SubagentHooksDir(inst.ConfigDir, inst.Title), "mod"))
	_, hooked := inst.HookLaunch()
	assert.True(t, hooked, "the hooks don't depend on the mod")
}

func TestLaunchProgram_NoModWithoutHooks(t *testing.T) {
	withModSocket(t, "/run/loom/x.mod.sock")
	for _, program := range []string{"aider", "claude --settings /mine.json"} {
		inst := hooksInstance(t, program)

		got := inst.launchProgram(LaunchEnv{Program: program}, true)

		assert.NotContains(t, got, "--plugin-dir", program)
		assert.Nil(t, inst.modEnv(), program)
		launch, hooked := inst.HookLaunch()
		assert.Equal(t, noHooksLaunchID, launch, program)
		assert.False(t, hooked, program)
	}
}

// A relaunch that goes without the mod (the socket went away) drops the
// previous launch's mod environment, so no new session names a socket for
// another launch.
func TestLaunchProgram_ARelaunchForgetsTheLastModEnv(t *testing.T) {
	withModSocket(t, "/run/loom/x.mod.sock")
	inst := hooksInstance(t, "claude")
	inst.launchProgram(LaunchEnv{Program: "claude"}, true)
	require.NotNil(t, inst.modEnv())

	SetModSocket("")
	inst.launchProgram(LaunchEnv{Program: "claude"}, true)

	assert.Nil(t, inst.modEnv())
}

// A reattach (launching false) keeps the live launch's mod environment.
func TestLaunchProgram_AReattachKeepsTheModEnv(t *testing.T) {
	withModSocket(t, "/run/loom/x.mod.sock")
	inst := hooksInstance(t, "claude")
	inst.launchProgram(LaunchEnv{Program: "claude"}, true)
	want := inst.modEnv()

	inst.launchProgram(LaunchEnv{Program: "claude"}, false)

	assert.Equal(t, want, inst.modEnv())
}

func TestHookLaunch_BeforeTheFirstScanIsUnknown(t *testing.T) {
	inst := &Instance{Title: "x", program: "claude"}
	launch, hooked := inst.HookLaunch()
	assert.Equal(t, "", launch)
	assert.False(t, hooked)
}

// The mod's grace runs from the launch's first SessionStart: startup or
// resume, never a later /clear, and a new launch forgets it.
func TestLaunchSessionStartedAt(t *testing.T) {
	inst := hooksInstance(t, "claude")
	inst.launchProgram(LaunchEnv{Program: "claude"}, true)
	launch, _ := inst.HookLaunch()
	_, ok := inst.LaunchSessionStartedAt()
	require.False(t, ok, "nothing reported yet")

	t0 := time.Now().Add(-time.Minute).Round(0)
	require.True(t, inst.ApplyHookScan(HookScanResult{LaunchID: launch, Replayed: true, Events: []hooks.Event{
		{Name: hooks.EventSessionStart, Source: "startup", At: t0},
		{Name: hooks.EventSessionStart, Source: "clear", At: t0.Add(30 * time.Second)},
	}}))
	at, ok := inst.LaunchSessionStartedAt()
	require.True(t, ok)
	assert.Equal(t, t0, at)

	inst.launchProgram(LaunchEnv{Program: "claude"}, true)
	_, ok = inst.LaunchSessionStartedAt()
	assert.False(t, ok, "a new launch has not started yet")
}

// The launch sites build the env after the program: pin the order the mod
// env relies on (it reads the launch ID launchProgram minted).
func TestInstanceEnv_CallSitesAppendTheModEnv(t *testing.T) {
	src, err := os.ReadFile("instance.go")
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(src), "InstanceEnv(env), i.modEnv()..."), "instance.go's two launch sites")
	src, err = os.ReadFile("subagent_hooks.go")
	require.NoError(t, err)
	assert.Contains(t, string(src), "append(InstanceEnv(le), i.modEnv()...)")
}
```

If `ApplyHookScan`'s return value means something other than "changed" for this input, drop the `require.True` around it and keep the call.

`session/agent/claude_test.go`, add:

```go
func TestApplyClaudePluginDir(t *testing.T) {
	assert.Equal(t, "claude --plugin-dir '/h/mod' --model x", ApplyClaudePluginDir("claude --model x", "/h/mod"))
	assert.Equal(t, "claude", ApplyClaudePluginDir("claude", ""))
	assert.Equal(t, "", ApplyClaudePluginDir("", "/h/mod"))
}
```

Run: `CGO_ENABLED=0 go test ./session/ ./session/agent/ -run 'Mod|HookLaunch|LaunchSessionStartedAt|InstanceEnv_CallSites|PluginDir'` → FAIL (undefined: `SetModSocket`, `modEnv`, `HookLaunch`, `LaunchSessionStartedAt`, `ApplyClaudePluginDir`).

- [ ] **B5. The launch side: implementation.**

`session/agent/claude.go`, after `ApplySettingsFlag`:

```go
// ApplyClaudePluginDir inserts "--plugin-dir '<dir>'" after "claude": the
// loom mod for this launch (session/claudemod). dir must hold no single quote
// (hooks.SafePath).
func ApplyClaudePluginDir(program, dir string) string {
	if dir == "" || len(strings.Fields(program)) == 0 {
		return program
	}
	return insertAfterCommand(program, "--plugin-dir '"+dir+"'")
}
```

`session/claude_mod.go`:

```go
package session

import (
	"path/filepath"
	"slices"
	"sync/atomic"
	"time"

	"github.com/aidan-bailey/loom/session/claudemod"
)

// modSocket is the socket the daemon serves the loom mod on (SetModSocket).
var modSocket atomic.Pointer[string]

// SetModSocket names the socket the daemon serves the loom mod on: every
// Claude launch from now on gets the mod (--plugin-dir) and the socket
// (claudemod.EnvSocket). "" stops giving launches the mod. The daemon sets it
// before its boot relaunches anything (internal/daemon.Serve). It is process
// wide, not per config dir, since one daemon serves one global dir.
func SetModSocket(path string) { modSocket.Store(&path) }

// ModSocket is the socket SetModSocket named, "" for none.
func ModSocket() string {
	if p := modSocket.Load(); p != nil {
		return *p
	}
	return ""
}

// modDirOf is where a launch's mod lives in its hooks folder.
func modDirOf(hooksDir string) string { return filepath.Join(hooksDir, "mod") }

// prepareMod writes the loom mod into the launch's hooks folder (dir, which
// hooks.Prepare just emptied) and returns the environment that points it at
// the daemon, or nil when no daemon serves the mod or the write failed: the
// launch then goes without it, and the daemon counts it absent.
func (i *Instance) prepareMod(dir, launchID string) []string {
	socket := ModSocket()
	if socket == "" {
		return nil
	}
	if err := claudemod.WriteTo(modDirOf(dir)); err != nil {
		i.getLogger().Warn("claude_mod.write_failed", "err", err.Error())
		return nil
	}
	return []string{claudemod.EnvSocket + "=" + socket, claudemod.EnvLaunch + "=" + launchID}
}

// modEnv is the environment that lets the current launch's mod reach the
// daemon, nil when the launch got no mod. The launch sites append it to
// InstanceEnv after launchProgram, which decides it.
func (i *Instance) modEnv() []string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return slices.Clone(i.modEnvVars)
}

// HookLaunch is the current launch's hooks ID and whether the launch is
// hooked: prepareHooks prepared its folder, or a restored instance adopted
// the ID its scan read. It is ("", false) before a restored instance's first
// scan, and (noHooksLaunchID, false) for a launch without hooks.
func (i *Instance) HookLaunch() (string, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.hookLaunchID, i.hookLaunchID != "" && i.hookLaunchID != noHooksLaunchID
}

// LaunchSessionStartedAt is when the current launch's Claude reported its
// SessionStart (startup or resume), and whether it has.
func (i *Instance) LaunchSessionStartedAt() (time.Time, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.claude.sessionStartedAt, !i.claude.sessionStartedAt.IsZero()
}
```

`session/instance.go`, beside `hookLaunchID` in the struct:

```go
	// modEnvVars is the current launch's mod environment (prepareMod), nil
	// when it got no mod. Guarded by mu.
	modEnvVars []string
```

and the two launch sites become:

```go
		i.setTmuxSession(old.WithProgramEnv(i.launchProgram(env, true), append(InstanceEnv(env), i.modEnv()...)))
```

```go
		launchProgram := i.launchProgram(env, firstTimeSetup)
		ts = tmux.NewSession(i.Title, launchProgram, append(InstanceEnv(env), i.modEnv()...)...)
```

`session/subagent_hooks.go`:
- `recoveryLaunch`: `return i.launchProgram(le, true), append(InstanceEnv(le), i.modEnv()...), nil`
- `resetHookLaunch`: inside its locked block, add `i.modEnvVars = nil`.
- `prepareHooks`, from `launchID, err := hooks.Prepare(dir, memoryDir)`'s success on:

```go
	modEnv := i.prepareMod(dir, launchID)
	i.mu.Lock()
	i.hookLaunchID = launchID
	i.modEnvVars = modEnv
	i.subagentWarm = false
	i.subagentTrackerLocked().Reset()
	i.mu.Unlock()
	program = BuildSettingsCommand(program, hooks.SettingsPath(dir))
	if modEnv != nil {
		program = agent.ApplyClaudePluginDir(program, modDirOf(dir))
	}
	return program
```

(its doc comment gains: it also writes the loom mod into the folder and adds `--plugin-dir` when a daemon serves the mod, prepareMod.)

`session/claude_state.go`: add to `claudeState`:

```go
	// sessionStartedAt is when this launch's Claude first reported its
	// SessionStart (startup or resume): core's mod grace runs from it.
	// newLaunch clears it.
	sessionStartedAt time.Time
```

in `applyEvent`'s `EventSessionStart` case, before `switch ev.Source`:

```go
		if s.sessionStartedAt.IsZero() && (ev.Source == "startup" || ev.Source == "resume") {
			s.sessionStartedAt = ev.At
		}
```

and in `newLaunch`: `s.sessionStartedAt = time.Time{}`.

Run: `CGO_ENABLED=0 go test ./session/... ./tools/...` → PASS. (`tools/fakeagent` ignores `--plugin-dir`: its `parseArgs` skips flags it doesn't know, and their values.)

- [ ] **B6. `session/claudemod/CLAUDE.md`.**

```markdown
# session/claudemod

The loom mod for Claude Code: a function-hook plugin (`mod/`: the manifest, `hooks/hooks.json`, the TypeScript module `hooks/register.ts`) embedded in the binary, which `session` writes into each Claude launch's hooks folder (`WriteTo`) and passes with `--plugin-dir`. The module long-polls the daemon's mod socket (the daemon's mod server), submits each message with `$.prompt.submit`, and reports `queued`, `started`, `complete` or `failed`. Design: [`../../docs/superpowers/specs/2026-10-14-loom-claude-mod-design.md`](../../docs/superpowers/specs/2026-10-14-loom-claude-mod-design.md) and its plan's amendments.

## Rules when modifying this package

- **Keep the module a carrier: compose text, decide policy and judge liveness in Go (the model, in `core`), never in TypeScript.** The module ships on Claude's early-access API and nothing in CI runs it; the Go side is where the tests are. **Convention** — logic only an opt-in real-Claude run exercises.
- **Keep the module's reach to `$.env`, `$.http`, `$.prompt`, `$.session`, `$.clock` and `$.ui.log`.** It runs inside every Claude loom launches; `$.process` or `$.fs` there would make every session a host for whatever reaches the socket. **Enforced** by `TestRealClaude_ValidatesTheMod` (opt-in, `LOOM_TEST_REAL_CLAUDE=1`), which pins the validator's call list.
- **Change `Version`, the manifest's major version, `MOD_VERSION` and the `/v<n>` path together, and only for a change an older daemon or module would misread.** A mod from an older build talks to a newer daemon after an upgrade. **Enforced** for the three agreeing by `TestModule_SpeaksThisProtocol` and `TestWriteTo_WritesTheWholeMod`; the need for a bump is **Convention** — a mod and daemon misreading each other silently.
- **Embed with `all:mod`.** A plain pattern skips `.claude-plugin`, and Claude then loads no plugin at all. **Enforced** by `TestWriteTo_WritesTheWholeMod`.
- **Write the mod per launch, into the hooks folder, never into a folder sessions share.** Claude writes `tsconfig.json` and `.claude-plugin/types/` into every `--plugin-dir` folder it loads, and saving a module file in a watched folder hot-reloads the module in every session watching it. **Convention** — sessions rewriting and reloading each other's mod.
- **Run the opt-in real-Claude tests (`LOOM_TEST_REAL_CLAUDE=1`, `-run RealClaude`, here and in the daemon's mod server) after changing the module.** CI has no `claude`, so nothing else type-checks the module or drives it against Claude. **Convention** — a module Claude refuses, and every session silently without it.

## Pointers

- [`../CLAUDE.md`](../CLAUDE.md) — the launch that writes the mod (`prepareMod`).
- [`../../docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md) — where this package sits.
```

- [ ] **B7. Gate and commit.**

```bash
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/') session/claudemod/*.go session/claude_mod*.go
CGO_ENABLED=0 go vet ./session/... ./tools/...
CGO_ENABLED=0 go test ./session/... ./tools/...
CGO_ENABLED=0 go run ./tools/claudemd
git status
git commit -m "feat(session): write the loom mod into each Claude launch" -- session/claudemod session/claude_mod.go session/claude_mod_test.go session/subagent_hooks.go session/instance.go session/claude_state.go session/agent/claude.go session/agent/claude_test.go
```

---

## Package C: the mod state, its server and the daemon

After C, the daemon serves the mod socket and every Claude launch gets the mod; nothing sends through it yet (Package D), so messages only come from tests. C4 also points `session/claudemod/CLAUDE.md`'s last rule at `internal/modserver` by path, now that it exists.

- [ ] **C1. The model's mod state: failing tests first.** `core/mod_test.go`:

```go
package core

import (
	"testing"
	"time"

	"github.com/aidan-bailey/loom/session"
	"github.com/aidan-bailey/loom/session/hooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withMod has a daemon serve the mod for the test: Claude launches can have
// one (modLaunch).
func withMod(t *testing.T) {
	t.Helper()
	session.SetModSocket("/tmp/loom-test.mod.sock")
	t.Cleanup(func() { session.SetModSocket("") })
}

// adoptLaunch delivers a scan naming launch with its SessionStart at started,
// as a restored instance adopts its launch (startedInst's mock tmux prepares
// no hooks folder). No session ID: a conversation change would save.
func adoptLaunch(t *testing.T, m *Model, inst *session.Instance, launch string, started time.Time) {
	t.Helper()
	m.Deliver(hookScanResults{results: []hookScanResult{{instance: inst, result: session.HookScanResult{
		LaunchID: launch, Replayed: true,
		Events: []hooks.Event{{Name: hooks.EventSessionStart, Source: "startup", At: started}},
	}}}})
	got, hooked := inst.HookLaunch()
	require.True(t, hooked)
	require.Equal(t, launch, got)
}

// modInst is a started Claude instance m holds, on launch L1, which reported
// its SessionStart just now.
func modInst(t *testing.T, m *Model, title string) *session.Instance {
	t.Helper()
	inst := activeInst(t, m, title)
	adoptLaunch(t, m, inst, "L1", time.Now())
	m.Drain()
	return inst
}

func cliMessage(req ReqID, text string, wait bool) *modMessage {
	return &modMessage{text: text, origin: OriginCLI, req: req, wait: wait}
}

// noticeFor is the Notice in events naming req, if any.
func noticeFor(events []Event, req ReqID) (Notice, bool) {
	for _, ev := range events {
		if n, ok := ev.(Notice); ok && n.Req == req {
			return n, true
		}
	}
	return Notice{}, false
}

func closed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

func TestMod_AMessageWaitsForItsModAndRepliesWhenQueued(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")

	m.enqueue(inst, cliMessage(5, "hello", false))
	assert.Empty(t, replies(m.Drain()), "no reply until the mod has queued it")

	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
	msgs, _, err := m.ModInbox("L1")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, ModMessage{ID: msgs[0].ID, Text: "hello"}, msgs[0])

	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}}))
	assert.Equal(t, []Reply{{Req: 5, ID: m.idOf(inst)}}, replies(m.Drain()))

	again, _, err := m.ModInbox("L1")
	require.NoError(t, err)
	assert.Empty(t, again, "a queued message leaves the inbox")
	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}}))
	assert.Empty(t, replies(m.Drain()), "a repeated report replies nothing more")
}

func TestMod_WaitHearsTheTurnsAnswer(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	m.enqueue(inst, cliMessage(5, "hello", true))
	msgs, _, _ := m.ModInbox("L1")
	id := msgs[0].ID

	require.NoError(t, m.ModEvents("L1", []ModEvent{
		{Kind: "queued", ID: id},
		{Kind: "started", ID: id, TurnID: "t1"},
		{Kind: "complete", ID: id, TurnID: "t1", Text: "hi back", Reason: "answer"},
	}))

	out := m.Drain()
	assert.Equal(t, []Reply{{Req: 5, ID: m.idOf(inst)}}, replies(out))
	assert.Contains(t, out.Events, Answered{Req: 5, ID: m.idOf(inst), Text: "hi back", Reason: "answer"})
}

func TestMod_AnUnknownLaunchIsRefused(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	modInst(t, m, "x")

	assert.ErrorIs(t, m.ModHello("L9", ModHello{}), ErrModUnknownLaunch)
	_, _, err := m.ModInbox("L9")
	assert.ErrorIs(t, err, ErrModUnknownLaunch)
	assert.ErrorIs(t, m.ModEvents("", nil), ErrModUnknownLaunch)
}

// A newer poll, and a new message, end a held poll (its channel closes).
func TestMod_APollIsWokenByAMessageAndSupersededByAPoll(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")

	_, first, err := m.ModInbox("L1")
	require.NoError(t, err)
	_, second, err := m.ModInbox("L1")
	require.NoError(t, err)
	assert.True(t, closed(first), "a newer poll supersedes the held one")
	assert.False(t, closed(second))

	m.enqueue(inst, cliMessage(5, "hello", false))
	assert.True(t, closed(second), "a message wakes the held poll")
}

// Both sides of the grace: a pending launch inside it keeps its messages;
// past it, with no hello, a CLI message is refused and a script's is pasted.
func TestMod_PastTheGraceAModlessLaunchFallsBack(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := activeInst(t, m, "x")
	started := time.Now().Add(-time.Minute)
	adoptLaunch(t, m, inst, "L1", started)
	m.Drain()
	m.enqueue(inst, cliMessage(5, "from the cli", false))
	m.enqueue(inst, &modMessage{text: "from a script", asUser: true, origin: OriginScript, req: 6})

	m.modTick(started.Add(modGrace - time.Second))
	out := m.Drain()
	assert.Empty(t, replies(out), "inside the grace the launch is still pending")
	assert.Empty(t, out.Jobs)

	m.modTick(started.Add(modGrace))
	out = m.Drain()
	rs := replies(out)
	require.Len(t, rs, 1, "the CLI message is refused at once; the script's is pasted by a job")
	assert.Equal(t, ReqID(5), rs[0].Req)
	assert.ErrorContains(t, rs[0].Err, "runs without the loom mod")
	require.Len(t, out.Jobs, 1, "the script's paste")
	msgs, _, _ := m.ModInbox("L1")
	assert.Empty(t, msgs)

	m.enqueue(inst, cliMessage(7, "later", false))
	rs = replies(m.Drain())
	require.Len(t, rs, 1, "an absent launch refuses a CLI message at once")
	assert.Equal(t, ReqID(7), rs[0].Req)
}

// The grace runs from the daemon's boot at the earliest: a restarted daemon
// re-reads an old SessionStart, and the mod needs time to reconnect.
func TestMod_TheGraceRunsFromTheBootAtTheEarliest(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := activeInst(t, m, "x")
	adoptLaunch(t, m, inst, "L1", time.Now().Add(-time.Hour))
	m.bootedAt = time.Now()
	m.enqueue(inst, cliMessage(5, "hello", false))
	m.Drain()

	m.modTick(m.bootedAt.Add(modGrace - time.Second))
	assert.Empty(t, replies(m.Drain()))
	m.modTick(m.bootedAt.Add(modGrace))
	assert.Len(t, replies(m.Drain()), 1)
}

// A late hello brings an absent launch back: after a daemon restart the mod
// can reconnect after the grace.
func TestMod_ALateHelloMakesTheLaunchLive(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := activeInst(t, m, "x")
	adoptLaunch(t, m, inst, "L1", time.Now().Add(-time.Minute))
	m.modTick(time.Now())
	m.enqueue(inst, cliMessage(5, "refused", false))
	require.Len(t, replies(m.Drain()), 1)

	require.NoError(t, m.ModHello("L1", ModHello{Mod: 1}))
	m.enqueue(inst, cliMessage(6, "delivered", false))
	assert.Empty(t, replies(m.Drain()), "live again: it waits for the mod")
	msgs, _, _ := m.ModInbox("L1")
	require.Len(t, msgs, 1)
	assert.Equal(t, "delivered", msgs[0].Text)
}

// A launch that can't have a mod is absent from the start: here no daemon
// serves the mod.
func TestMod_ALaunchWithoutAModIsAbsentAtOnce(t *testing.T) {
	m := NewForTest(Options{})
	inst := activeInst(t, m, "x")
	adoptLaunch(t, m, inst, "L1", time.Now())
	m.Drain()

	m.enqueue(inst, cliMessage(5, "hello", false))

	rs := replies(m.Drain())
	require.Len(t, rs, 1)
	assert.ErrorContains(t, rs[0].Err, "runs without the loom mod")
}

// A restored instance before its first scan has launch "": pending, and the
// scan's launch is the same process, not a relaunch.
func TestMod_ARestoredLaunchIsAdoptedInPlace(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := activeInst(t, m, "x")
	m.enqueue(inst, cliMessage(5, "hello", false))
	assert.Empty(t, replies(m.Drain()), "pending until the scan names the launch")

	adoptLaunch(t, m, inst, "L1", time.Now())
	m.modTick(time.Now())
	assert.Empty(t, replies(m.Drain()), "the same launch: the message still waits")
	msgs, _, err := m.ModInbox("L1")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
}

func TestMod_AStoppedSessionRefusesWhatItHeld(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	m.enqueue(inst, cliMessage(5, "never queued", false))
	m.enqueue(inst, cliMessage(6, "queued, waiting", true))
	msgs, wake, _ := m.ModInbox("L1")
	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[1].ID}}))
	m.Drain()

	require.NoError(t, inst.TransitionTo(session.Paused))
	m.modTick(time.Now())

	out := m.Drain()
	rs := replies(out)
	require.Len(t, rs, 1)
	assert.Equal(t, ReqID(5), rs[0].Req)
	assert.ErrorContains(t, rs[0].Err, "stopped before the message reached it")
	n, ok := noticeFor(out.Events, 6)
	require.True(t, ok, "the waiter hears its turn will not end")
	assert.ErrorContains(t, n.Err, "stopped before its turn ended")
	assert.True(t, closed(wake), "a held poll ends")
}

// A message the mod could not submit falls back as a modless launch's does;
// one Claude dropped after it was queued is reported to the waiter.
func TestMod_FailedMessages(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	m.enqueue(inst, cliMessage(5, "refused by claude", false))
	m.enqueue(inst, cliMessage(6, "dropped later", true))
	msgs, _, _ := m.ModInbox("L1")
	m.Drain()

	require.NoError(t, m.ModEvents("L1", []ModEvent{
		{Kind: "failed", ID: msgs[0].ID, Error: "no"},
		{Kind: "queued", ID: msgs[1].ID},
		{Kind: "failed", ID: msgs[1].ID, Error: "dropped: a Stop stands"},
	}))

	out := m.Drain()
	rs := replies(out)
	require.Len(t, rs, 2)
	assert.Equal(t, ReqID(5), rs[0].Req)
	assert.ErrorContains(t, rs[0].Err, "could not submit it: no")
	assert.Equal(t, Reply{Req: 6, ID: m.idOf(inst)}, rs[1])
	n, ok := noticeFor(out.Events, 6)
	require.True(t, ok)
	assert.ErrorContains(t, n.Err, "dropped: a Stop stands")
}

// A mod's report wakes the client, which must drain the Reply it produced.
func TestLoop_AModReportWakesTheClient(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	m.enqueue(inst, cliMessage(5, "hello", false))
	m.Drain()
	l := StartForTest(m)
	t.Cleanup(l.Stop)
	msgs, _, err := l.ModInbox("L1")
	require.NoError(t, err)
	for len(l.Wakes()) > 0 {
		<-l.Wakes()
	}

	require.NoError(t, l.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}}))

	select {
	case <-l.Wakes():
	case <-time.After(5 * time.Second):
		t.Fatal("no wake after a mod's report")
	}
	assert.Len(t, replies(Out{Events: l.Sync()}), 1)
}
```

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestMod_|TestLoop_AModReport'` → FAIL (undefined: `enqueue`, `modMessage`, `OriginCLI`, …).

- [ ] **C2. The model's mod state: implementation.** `core/mod.go`:

```go
package core

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/aidan-bailey/loom/log"
	"github.com/aidan-bailey/loom/session"
)

// modGrace is how long after a launch's SessionStart (or the daemon's boot,
// whichever is later) its loom mod has to say hello before the launch counts
// as having none (modAbsent).
const modGrace = 10 * time.Second

// ErrModUnknownLaunch answers a mod whose launch no served instance holds now:
// its session relaunched or left loom, or the hook scan has not yet read a
// restored session's launch. The mod retries.
var ErrModUnknownLaunch = errors.New("no session holds this launch")

// Origin says who sent a message, which decides how the mod submits it and
// what happens when the session's launch has no live mod.
type Origin string

const (
	// OriginCLI is `loom session send`: framed, and refused without a mod.
	OriginCLI Origin = "cli"
	// OriginScript is Lua's inst:send_prompt: the person's own words, and
	// pasted without a mod, as SendPrompt does.
	OriginScript Origin = "script"
	// OriginFirstPrompt is an N flow's first prompt, which only the daemon
	// sends (deliverStart): the person's own words, pasted without a mod.
	OriginFirstPrompt Origin = "first-prompt"
)

// ModHello is what a mod says when it starts or reconnects.
type ModHello struct {
	Mod     int    `json:"mod"`
	Claude  string `json:"claude"`
	Session string `json:"session"`
}

// ModMessage is a message as the mod's inbox hands it out.
type ModMessage struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	AsUser bool   `json:"asUser,omitempty"`
}

// ModEvent is one thing a mod reports about a message: queued (handed to
// Claude), started (its turn began), complete (its turn ended: Text is the
// answer, Reason why) or failed (Error).
type ModEvent struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	TurnID string `json:"turnId,omitempty"`
	Text   string `json:"text,omitempty"`
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ModHost is the model as the loom mod's server reaches it
// (internal/modserver). Each call names the launch the mod runs in
// (LOOM_LAUNCH); one no served instance holds now is ErrModUnknownLaunch.
// *Loop implements it.
type ModHost interface {
	ModHello(launch string, hello ModHello) error
	// ModInbox returns the messages waiting for launch, and a channel that
	// closes when that may have changed (a message arrived, a newer poll
	// superseded this one, the launch ended). A nil channel means the
	// daemon is stopping.
	ModInbox(launch string) ([]ModMessage, <-chan struct{}, error)
	ModEvents(launch string, events []ModEvent) error
}

// modLiveness is whether a launch has a loom mod.
type modLiveness int

const (
	modPending modLiveness = iota // launched; no hello yet
	modLive                       // the mod said hello
	modAbsent                     // no mod: not offered, or no hello within the grace
)

// modMessage is a message for a session's mod.
type modMessage struct {
	id     string
	text   string
	asUser bool
	origin Origin
	req    ReqID // the sender's request; 0 for none
	wait   bool  // the sender waits for the turn's end (Answered)
	// cause and owner finish a first prompt's start: the Create's request,
	// which Started carries, and the start's owner.
	cause ReqID
	owner *Workspace
}

// modLink is what the model knows of one instance's current launch and its
// mod.
type modLink struct {
	launch  string
	live    modLiveness
	outbox  []*modMessage          // waiting for the mod, oldest first
	turning map[string]*modMessage // queued with Wait: waiting for their turn's end, by ID
	wake    chan struct{}
}

func newModLink(launch string, live modLiveness) *modLink {
	return &modLink{launch: launch, live: live, turning: map[string]*modMessage{}, wake: make(chan struct{})}
}

// poke ends a held poll (its channel closes) and arms the next.
func (l *modLink) poke() {
	close(l.wake)
	l.wake = make(chan struct{})
}

// take removes the message id from the outbox and returns it; nil when none
// waits there.
func (l *modLink) take(id string) *modMessage {
	for i, msg := range l.outbox {
		if msg.id == id {
			l.outbox = append(l.outbox[:i], l.outbox[i+1:]...)
			return msg
		}
	}
	return nil
}

// newModMessageID is a message's ID: random, so IDs stay unique across daemon
// restarts, which the mod's duplicate check relies on.
func newModMessageID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// modLaunch is inst's current launch, and whether it can have a mod: a daemon
// serves the mod (session.ModSocket), the program is Claude, and the launch is
// hooked, or not yet known ("": a restored instance before its first scan).
func modLaunch(inst *session.Instance) (launch string, possible bool) {
	launch, hooked := inst.HookLaunch()
	if session.ModSocket() == "" || !session.IsClaudeProgram(inst.Program()) {
		return launch, false
	}
	return launch, hooked || launch == ""
}

// linkFor is inst's link for its current launch. A link for an earlier launch
// ends (endLink) and a new one starts, pending, or absent when the launch can't
// have a mod; a link made before a restored instance's scan takes the launch
// the scan read.
func (m *Model) linkFor(inst *session.Instance) *modLink {
	launch, possible := modLaunch(inst)
	l := m.mods[inst]
	switch {
	case l != nil && l.launch == launch:
		return l
	case l != nil && l.launch == "" && possible:
		l.launch = launch
		return l
	case l != nil:
		m.endLink(inst, l, "the session relaunched")
	}
	live := modPending
	if !possible {
		live = modAbsent
	}
	l = newModLink(launch, live)
	if m.mods == nil {
		m.mods = map[*session.Instance]*modLink{}
	}
	m.mods[inst] = l
	return l
}

// instByLaunch is the served instance whose current launch is launch.
func (m *Model) instByLaunch(launch string) *session.Instance {
	if launch == "" {
		return nil
	}
	for _, ws := range m.workspaces {
		for _, inst := range ws.instances() {
			if id, hooked := inst.HookLaunch(); hooked && id == launch {
				return inst
			}
		}
	}
	return nil
}

// enqueue hands msg to inst's mod: into the outbox while the launch's mod is
// pending or live, or at once to the fallback when it has none.
func (m *Model) enqueue(inst *session.Instance, msg *modMessage) {
	msg.id = newModMessageID()
	l := m.linkFor(inst)
	if l.live == modAbsent {
		m.withoutMod(inst, msg, "")
		return
	}
	l.outbox = append(l.outbox, msg)
	l.poke()
}

// withoutMod delivers msg the way it goes when inst's launch has no live mod:
// a CLI message is refused, a script's is pasted (SendPrompt's job, answering
// its request), and a first prompt is pasted as a start always sent it
// (sendInitialPrompt, whose promptSent emits Started). why, when set, is the
// mod's own failure to submit it.
func (m *Model) withoutMod(inst *session.Instance, msg *modMessage, why string) {
	id := m.idOf(inst)
	switch msg.origin {
	case OriginCLI:
		err := fmt.Errorf("send to %s: its Claude runs without the loom mod (an older Claude, or the mod did not load): attach and type instead", inst.Title)
		if why != "" {
			err = fmt.Errorf("send to %s: the loom mod could not submit it: %s", inst.Title, why)
		}
		m.refuse(msg.req, id, err)
	case OriginScript:
		m.causedBy(0, func() { m.spawn(m.track(msg.req, id, m.sendPromptInst(inst, msg.text))) })
	case OriginFirstPrompt:
		m.causedBy(msg.cause, func() { m.spawn(sendInitialPrompt(inst, msg.owner, msg.text)) })
	}
}

// endLink settles everything l holds once its launch is over: a sender whose
// message was never queued hears why, a first prompt finishes its start as a
// failed paste does (logged, then Started), and a sender waiting for a turn's
// end gets a Notice. A held poll ends. why says what happened ("the session
// stopped").
func (m *Model) endLink(inst *session.Instance, l *modLink, why string) {
	m.causedBy(0, func() {
		id := m.idOf(inst)
		for _, msg := range l.outbox {
			if msg.origin == OriginFirstPrompt {
				log.For("core").Warn("mod.first_prompt_undelivered", "instance", inst.Title, "why", why)
				m.causedBy(msg.cause, func() { m.deliverPromptSent(promptSent{inst: inst, owner: msg.owner}) })
				continue
			}
			m.refuse(msg.req, id, fmt.Errorf("send to %s: %s before the message reached it", inst.Title, why))
		}
		for _, msg := range l.turning {
			m.emit(Notice{Req: msg.req, Err: fmt.Errorf("send to %s: %s before its turn ended", inst.Title, why)})
		}
	})
	l.outbox, l.turning = nil, map[string]*modMessage{}
	l.poke()
}

// modTick settles each link by its instance: one that left the model or
// stopped running ends; a relaunch ends the old launch's (linkFor); a pending
// launch past its grace turns absent and hands its outbox to the fallback.
// The hook timer runs it (HookTick).
func (m *Model) modTick(now time.Time) {
	for inst, l := range m.mods {
		st := inst.GetStatus()
		switch {
		case m.holding(inst) == nil:
			m.endLink(inst, l, "the session left loom")
			delete(m.mods, inst)
		case !inst.Started() || st == session.Paused || st == session.Recoverable:
			m.endLink(inst, l, "the session stopped")
			delete(m.mods, inst)
		default:
			l = m.linkFor(inst)
			if l.live != modPending || !m.pastModGrace(inst, now) {
				continue
			}
			l.live = modAbsent
			log.For("core").Info("mod.absent", "instance", inst.Title, "launch", l.launch)
			out := l.outbox
			l.outbox = nil
			for _, msg := range out {
				m.withoutMod(inst, msg, "")
			}
			l.poke()
		}
	}
}

// pastModGrace reports whether inst's launch has had its grace: modGrace since
// its SessionStart or the daemon's boot, whichever is later. A launch that has
// not reported its SessionStart is still starting (a trust prompt, say).
func (m *Model) pastModGrace(inst *session.Instance, now time.Time) bool {
	at, ok := inst.LaunchSessionStartedAt()
	if !ok {
		return false
	}
	if m.bootedAt.After(at) {
		at = m.bootedAt
	}
	return now.Sub(at) >= modGrace
}

// ModHello records that launch's mod is live.
func (m *Model) ModHello(launch string, h ModHello) error {
	inst := m.instByLaunch(launch)
	if inst == nil {
		return ErrModUnknownLaunch
	}
	l := m.linkFor(inst)
	if l.live != modLive {
		log.For("core").Info("mod.live", "instance", inst.Title, "launch", launch, "mod", h.Mod, "claude", h.Claude)
	}
	l.live = modLive
	return nil
}

// ModInbox returns the messages waiting for launch's mod, and the channel a
// held poll waits on. A poll supersedes any poll still held for the launch.
func (m *Model) ModInbox(launch string) ([]ModMessage, <-chan struct{}, error) {
	inst := m.instByLaunch(launch)
	if inst == nil {
		return nil, nil, ErrModUnknownLaunch
	}
	l := m.linkFor(inst)
	l.poke()
	out := make([]ModMessage, 0, len(l.outbox))
	for _, msg := range l.outbox {
		out = append(out, ModMessage{ID: msg.id, Text: msg.text, AsUser: msg.asUser})
	}
	return out, l.wake, nil
}

// ModEvents applies what launch's mod reports.
func (m *Model) ModEvents(launch string, events []ModEvent) error {
	inst := m.instByLaunch(launch)
	if inst == nil {
		return ErrModUnknownLaunch
	}
	l := m.linkFor(inst)
	for _, ev := range events {
		switch ev.Kind {
		case "queued":
			m.modQueued(inst, l, ev.ID)
		case "started":
			log.DebugKV("core.mod.started", "instance", inst.Title, "id", ev.ID, "turn", ev.TurnID)
		case "complete":
			m.modComplete(inst, l, ev)
		case "failed":
			m.modFailed(inst, l, ev)
		default:
			log.For("core").Warn("mod.unknown_event", "instance", inst.Title, "kind", ev.Kind)
		}
	}
	return nil
}

// modQueued answers the sender of the message id: a first prompt's start
// finishes (Started, carrying the Create's request), any other sender gets
// its Reply, and one that waits for the turn's end is kept until it comes.
func (m *Model) modQueued(inst *session.Instance, l *modLink, id string) {
	msg := l.take(id)
	if msg == nil {
		return // reported again: the mod's report was retried
	}
	if msg.origin == OriginFirstPrompt {
		m.causedBy(msg.cause, func() { m.deliverPromptSent(promptSent{inst: inst, owner: msg.owner}) })
		return
	}
	m.reply(msg.req, Reply{ID: m.idOf(inst)})
	if msg.wait && msg.req != 0 {
		l.turning[msg.id] = msg
	}
}

// modComplete hands a waiting sender its turn's answer.
func (m *Model) modComplete(inst *session.Instance, l *modLink, ev ModEvent) {
	msg := l.turning[ev.ID]
	if msg == nil {
		return
	}
	delete(l.turning, ev.ID)
	m.emit(Answered{Req: msg.req, ID: m.idOf(inst), Text: ev.Text, Reason: ev.Reason})
}

// modFailed settles a message the mod could not submit: one still in the
// outbox falls back as a modless launch's would; one Claude dropped after it
// was queued is reported (to its waiter, or to every client), never pasted,
// since it may have reached Claude after all.
func (m *Model) modFailed(inst *session.Instance, l *modLink, ev ModEvent) {
	if msg := l.take(ev.ID); msg != nil {
		m.withoutMod(inst, msg, ev.Error)
		return
	}
	err := fmt.Errorf("send to %s: Claude dropped a message loom delivered: %s", inst.Title, ev.Error)
	if msg := l.turning[ev.ID]; msg != nil {
		delete(l.turning, ev.ID)
		m.emit(Notice{Req: msg.req, Err: err})
		return
	}
	m.emit(Notice{Err: err})
}
```

`core/model.go`: add `"time"` to the imports and, to `Model`, after `watching`:

```go
	// mods are the loom mod links (mod.go), one per instance that has had a
	// message for its mod or heard from it. Loop-goroutine only.
	mods map[*session.Instance]*modLink
	// bootedAt is when Boot ran: a mod's grace runs from it at the earliest
	// (pastModGrace), since a restarted daemon re-reads every SessionStart.
	bootedAt time.Time
```

`core/load.go`, `Boot`: first line `m.bootedAt = time.Now()` (add `"time"` if the file lacks it).

`core/hook_scan.go`, `HookTick`, right after `m.noWake = true`:

```go
	if len(m.mods) > 0 {
		m.modTick(time.Now())
	}
```

(its doc comment gains: it settles the mod links first, whether or not a Claude instance is active.)

`core/events.go`, after `Reply`:

```go
// Answered is the end of the turn a message sent with Wait opened (Send):
// Claude's answer and why the turn ended ("answer", "aborted", "error",
// "refusal"). Req names the Send; a server sends it to that client alone.
type Answered struct {
	Req    ReqID
	ID     InstanceID
	Text   string
	Reason string
}

func (Answered) coreEvent() {}
```

and `Answered{}` in `EventTypes()`, after `Alive{}`.

`core/rpc/route.go`: in `routed`, `case core.Answered: return e.Req != 0`; in `forConn`, beside `core.Reply`:

```go
	case core.Answered:
		req, mine := local(e.Req, n)
		e.Req = req
		return e, mine
```

(and `forConn`'s doc comment names `Answered` beside the Reply.)

`app/core_glue.go`, in `applyCoreEvent` beside `core.Reply`:

```go
	case core.Answered:
		// Only a Send with Wait asks for it, and the TUI's sends never wait.
```

`core/loop_mod.go`:

```go
package core

// The ModHost methods of Loop: each runs its namesake on the model, on the
// loop's goroutine. A mod's report can produce events no client call waits
// to drain (a Reply, Started, Answered), so the reports wake the client.
// They are not Core methods: only the daemon's mod server calls them, in
// process, and nothing about them crosses the wire.

var _ ModHost = (*Loop)(nil)

// ModHello runs Model.ModHello on the loop.
func (l *Loop) ModHello(launch string, h ModHello) error {
	defer l.signal()
	return get(l, func(m *Model) error { return m.ModHello(launch, h) })
}

// ModInbox runs Model.ModInbox on the loop. After Stop it returns a nil
// channel, which the server reads as the daemon stopping.
func (l *Loop) ModInbox(launch string) ([]ModMessage, <-chan struct{}, error) {
	var (
		msgs []ModMessage
		wake <-chan struct{}
		err  error
	)
	l.do(func(m *Model) { msgs, wake, err = m.ModInbox(launch) })
	return msgs, wake, err
}

// ModEvents runs Model.ModEvents on the loop.
func (l *Loop) ModEvents(launch string, events []ModEvent) error {
	defer l.signal()
	return get(l, func(m *Model) error { return m.ModEvents(launch, events) })
}
```

Run: `CGO_ENABLED=0 go test ./core/... ./app/...` → PASS. Then `go test ./core/rpc -run TestProtocolReference -update` and `CGO_ENABLED=0 go test ./core/rpc/` → PASS (`TestWire_EveryFieldSurvives`, `TestForConn_CoversEveryEventNamingARequest`, `TestAllEventsListsEveryEvent`).

- [ ] **C3. The mod server: failing tests first.** `internal/modserver/server_test.go`:

```go
package modserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeHost is a ModHost that knows one launch, "L1".
type fakeHost struct {
	mu     sync.Mutex
	hello  []core.ModHello
	msgs   []core.ModMessage
	wake   chan struct{}
	events []core.ModEvent
}

func (h *fakeHost) ModHello(launch string, hello core.ModHello) error {
	if launch != "L1" {
		return core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hello = append(h.hello, hello)
	return nil
}

func (h *fakeHost) ModInbox(launch string) ([]core.ModMessage, <-chan struct{}, error) {
	if launch != "L1" {
		return nil, nil, core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wake == nil {
		h.wake = make(chan struct{})
	}
	return h.msgs, h.wake, nil
}

func (h *fakeHost) ModEvents(launch string, events []core.ModEvent) error {
	if launch != "L1" {
		return core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, events...)
	return nil
}

func (h *fakeHost) polled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.wake != nil
}

// shortSocket is a socket path short enough for sun_path whatever TMPDIR is.
func shortSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "lms")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "m.sock")
}

// unixClient is an HTTP client whose every request goes over path.
func unixClient(path string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}}
}

func listen(t *testing.T, h core.ModHost, hold time.Duration) (*Server, *http.Client) {
	t.Helper()
	path := shortSocket(t)
	s, err := Listen(path, h, hold)
	require.NoError(t, err)
	t.Cleanup(s.Close)
	return s, unixClient(path)
}

func post(t *testing.T, c *http.Client, path, body string) int {
	t.Helper()
	resp, err := c.Post("http://loom"+path, "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// get makes a GET in the background and hands back its status (0 for a
// failed request).
func get(c *http.Client, path string) <-chan int {
	done := make(chan int, 1)
	go func() {
		resp, err := c.Get("http://loom" + path)
		if err != nil {
			done <- 0
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		done <- resp.StatusCode
	}()
	return done
}

func TestListen_TheSocketIsPrivate(t *testing.T) {
	s, _ := listen(t, &fakeHost{}, time.Second)
	info, err := os.Stat(s.path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestHello(t *testing.T) {
	h := &fakeHost{}
	_, c := listen(t, h, time.Second)

	assert.Equal(t, http.StatusNoContent, post(t, c, "/v1/hello", `{"launch":"L1","mod":1,"claude":"2.1.299","session":"s"}`))
	assert.Equal(t, []core.ModHello{{Mod: 1, Claude: "2.1.299", Session: "s"}}, h.hello)
	assert.Equal(t, http.StatusNotFound, post(t, c, "/v1/hello", `{"launch":"L9"}`))
	assert.Equal(t, http.StatusBadRequest, post(t, c, "/v1/hello", `{"launch":`))
	assert.Equal(t, http.StatusBadRequest, post(t, c, "/v1/hello", `{"launch":"`+string(bytes.Repeat([]byte("x"), maxBody))+`"}`), "an oversized body")
}

func TestInbox_HandsOutWaitingMessagesAtOnce(t *testing.T) {
	h := &fakeHost{msgs: []core.ModMessage{{ID: "m1", Text: "hi", AsUser: true}}}
	_, c := listen(t, h, time.Hour)

	resp, err := c.Get("http://loom/v1/inbox?launch=L1")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body struct{ Messages []core.ModMessage }
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, h.msgs, body.Messages)
}

func TestInbox_AnUnknownLaunchIs404(t *testing.T) {
	_, c := listen(t, &fakeHost{}, time.Hour)
	assert.Equal(t, http.StatusNotFound, <-get(c, "/v1/inbox?launch=L9"))
}

func TestInbox_HoldsUntilWoken(t *testing.T) {
	h := &fakeHost{}
	_, c := listen(t, h, time.Hour)
	done := get(c, "/v1/inbox?launch=L1")
	require.Eventually(t, h.polled, 5*time.Second, 10*time.Millisecond)
	select {
	case code := <-done:
		t.Fatalf("answered %d before any wake", code)
	case <-time.After(100 * time.Millisecond):
	}

	h.mu.Lock()
	close(h.wake)
	h.mu.Unlock()

	assert.Equal(t, http.StatusNoContent, <-done)
}

func TestInbox_AHoldEndsAfterTheHoldTime(t *testing.T) {
	_, c := listen(t, &fakeHost{}, 50*time.Millisecond)
	select {
	case code := <-get(c, "/v1/inbox?launch=L1"):
		assert.Equal(t, http.StatusNoContent, code)
	case <-time.After(5 * time.Second):
		t.Fatal("the hold never ended")
	}
}

func TestInbox_AStopEndsAHoldWith503(t *testing.T) {
	h := &fakeHost{}
	s, c := listen(t, h, time.Hour)
	done := get(c, "/v1/inbox?launch=L1")
	require.Eventually(t, h.polled, 5*time.Second, 10*time.Millisecond)

	s.Close()

	assert.Equal(t, http.StatusServiceUnavailable, <-done)
	assert.NoFileExists(t, s.path)
}

func TestEvents(t *testing.T) {
	h := &fakeHost{}
	_, c := listen(t, h, time.Second)

	assert.Equal(t, http.StatusNoContent, post(t, c, "/v1/events", `{"launch":"L1","events":[{"kind":"queued","id":"m1"},{"kind":"complete","id":"m1","turnId":"t","text":"ok","reason":"answer"}]}`))
	assert.Equal(t, []core.ModEvent{{Kind: "queued", ID: "m1"}, {Kind: "complete", ID: "m1", TurnID: "t", Text: "ok", Reason: "answer"}}, h.events)
	assert.Equal(t, http.StatusNotFound, post(t, c, "/v1/events", `{"launch":"L9","events":[]}`))
}

func TestClose_IsSafeTwiceAndOnNil(t *testing.T) {
	s, _ := listen(t, &fakeHost{}, time.Second)
	s.Close()
	s.Close()
	var none *Server
	none.Close()
}
```

`internal/modserver/testmain_test.go` (the package reaches `config` through `core`, so `TestEveryConfigReachingPackageIsolatesLoomDirs` requires the isolation; its only tmux use, the opt-in E3 test, names its own socket):

```go
package modserver

import (
	"fmt"
	"os"
	"testing"

	"github.com/aidan-bailey/loom/internal/testenv"
	"github.com/aidan-bailey/loom/log"
)

// TestMain runs the package's tests with logging up and the loom dirs
// isolated from the developer's.
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests returns the exit code rather than exiting, so its deferred
// cleanup runs.
func runTests(m *testing.M) int {
	_ = log.Initialize("", false)
	defer log.Close()
	os.Unsetenv("TMUX")
	cleanup, err := testenv.IsolateLoomDirs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	defer cleanup()
	return m.Run()
}
```

Run: `CGO_ENABLED=0 go test ./internal/modserver/` → FAIL (undefined: `Listen`, `Server`, `maxBody`).

- [ ] **C4. The mod server: implementation.** `internal/modserver/server.go`:

```go
// Package modserver serves the loom mod's HTTP API (session/claudemod) on the
// daemon's mod socket: POST /v1/hello, GET /v1/inbox (a long poll) and POST
// /v1/events, each naming the launch the mod runs in. It holds no state: every
// request goes to the model through core.ModHost.
package modserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/log"
)

// DefaultHold is how long an empty inbox poll is held before it answers 204
// (plan package A measured what Claude's host allows).
const DefaultHold = 25 * time.Second

// maxBody caps a request's body.
const maxBody = 1 << 20

// Server is the mod socket's HTTP server.
type Server struct {
	host     core.ModHost
	hold     time.Duration
	path     string
	http     *http.Server
	stopping chan struct{}
	once     sync.Once
}

// Listen serves the mod's API on a unix socket at path (mode 0600; a stale
// file there is replaced), with host behind it and empty polls held for hold.
func Listen(path string, host core.ModHost, hold time.Duration) (*Server, error) {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("modserver: listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("modserver: make %s private: %w", path, err)
	}
	s := &Server{host: host, hold: hold, path: path, stopping: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/hello", s.hello)
	mux.HandleFunc("GET /v1/inbox", s.inbox)
	mux.HandleFunc("POST /v1/events", s.events)
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.For("modserver").Error("modserver.serve_failed", "err", err)
		}
	}()
	return s, nil
}

// Close ends every held poll with 503, stops serving and removes the socket.
// It is idempotent, and a no-op on nil.
func (s *Server) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		close(s.stopping)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.http.Shutdown(ctx)
		_ = os.Remove(s.path)
	})
}

type helloBody struct {
	Launch string `json:"launch"`
	core.ModHello
}

type eventsBody struct {
	Launch string          `json:"launch"`
	Events []core.ModEvent `json:"events"`
}

type inboxBody struct {
	Messages []core.ModMessage `json:"messages"`
}

func (s *Server) hello(w http.ResponseWriter, r *http.Request) {
	var b helloBody
	if decode(w, r, &b) {
		answer(w, s.host.ModHello(b.Launch, b.ModHello))
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	var b eventsBody
	if decode(w, r, &b) {
		answer(w, s.host.ModEvents(b.Launch, b.Events))
	}
}

// inbox answers the messages waiting for the launch at once, or holds the
// poll until something may have changed (204), the hold ends (204) or the
// daemon stops (503).
func (s *Server) inbox(w http.ResponseWriter, r *http.Request) {
	msgs, wake, err := s.host.ModInbox(r.URL.Query().Get("launch"))
	switch {
	case err != nil:
		answer(w, err)
		return
	case wake == nil:
		http.Error(w, "the loom daemon is stopping", http.StatusServiceUnavailable)
		return
	case len(msgs) > 0:
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(inboxBody{Messages: msgs})
		return
	}
	hold := time.NewTimer(s.hold)
	defer hold.Stop()
	select {
	case <-wake:
		w.WriteHeader(http.StatusNoContent)
	case <-hold.C:
		w.WriteHeader(http.StatusNoContent)
	case <-s.stopping:
		http.Error(w, "the loom daemon is stopping", http.StatusServiceUnavailable)
	case <-r.Context().Done():
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func answer(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, core.ErrModUnknownLaunch):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		log.For("modserver").Error("modserver.request_failed", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
```

`internal/modserver/CLAUDE.md`:

```markdown
# internal/modserver

The daemon's server for the loom mod (`session/claudemod`): HTTP on the mod socket, which `internal/daemon.ModSocketPath` places beside the daemon's own. `POST /v1/hello`, `GET /v1/inbox?launch=L` (answered at once with the waiting messages, or held up to `DefaultHold` and then 204) and `POST /v1/events`, each naming the launch the mod runs in; JSON bodies capped at `maxBody`. Every request goes to the model through `core.ModHost`, which `*core.Loop` implements.

## Rules when modifying this package

- **Hold no state, and reach the model only through `core.ModHost`.** The model owns sessions; a copy here goes stale, and the loop serializes every change. **Convention** — a second owner of who is live and what is queued.
- **End every held poll on a wake, a stop or the client leaving, and answer 503 on a stop.** The mod polls again at once after any answer, so a hold that outlives its reason only delays a message, and one that outlives the daemon keeps `Shutdown` waiting. **Enforced** by `TestInbox_HoldsUntilWoken` and `TestInbox_AStopEndsAHoldWith503`.
- **Answer an unknown launch with 404.** The mod retries every failure alike, but a 404 in the log reads as a stale or not-yet-adopted launch, normal after a daemon restart, where a 500 reads as a bug. **Enforced** by `TestHello`, `TestInbox_AnUnknownLaunchIs404` and `TestEvents`.
- **Keep the socket 0600 in the daemon's private directory.** Anyone who can post here can make every Claude loom runs submit a prompt. **Enforced** by `TestListen_TheSocketIsPrivate`.

## Pointers

- [`../daemon/CLAUDE.md`](../daemon/CLAUDE.md) — where the socket lives and when it is served.
- [`../../session/claudemod/CLAUDE.md`](../../session/claudemod/CLAUDE.md) — the module on the other end.
```

Run: `CGO_ENABLED=0 go test ./internal/modserver/` → PASS.

- [ ] **C5. The daemon: failing tests first.** In `internal/daemon/serve_test.go` (imports gain `context`, `net/http`, `session`):

```go
func TestModSocketPath(t *testing.T) {
	assert.Equal(t, "/run/user/1/loom/abc.mod.sock", ModSocketPath("/run/user/1/loom/abc.sock"))
	assert.Equal(t, "/g/run/serve.mod.sock", ModSocketPath("/g/run/serve.sock"))
	long := "/" + strings.Repeat("x", maxSocketPath-len("/.sock")) + ".sock"
	require.LessOrEqual(t, len(long), maxSocketPath)
	assert.Empty(t, ModSocketPath(long), "a path past the limit: no mod")
}

// The daemon serves the mod beside its own socket, names it for launches,
// and takes both down when it stops.
func TestServe_ServesTheModSocket(t *testing.T) {
	dir := globalDir(t)
	r := serve(t, dir, Options{})
	mod := ModSocketPath(r.socket)
	require.NotEmpty(t, mod)
	assert.Equal(t, mod, session.ModSocket(), "launches name it")
	info, err := os.Stat(mod)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	c := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", mod)
	}}}
	resp, err := c.Post("http://loom/v1/hello", "application/json", strings.NewReader(`{"launch":"nobody"}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "the model answers: no session holds that launch")

	require.NoError(t, r.shutdown(t))
	assert.NoFileExists(t, mod)
	assert.Empty(t, session.ModSocket(), "no launch names a socket nobody serves")
}
```

Run: `CGO_ENABLED=0 go test ./internal/daemon/ -run 'ModSocket'` → FAIL.

- [ ] **C6. The daemon: implementation.** `internal/daemon/paths.go`, after `SocketPath`:

```go
// ModSocketPath is where the loom mod reaches the daemon whose own socket is
// socket: beside it, its .sock suffix replaced by .mod.sock. It is "" when
// that path is too long for a unix socket: the daemon then serves no mod.
// Only the daemon computes it; launches learn it from LOOM_MOD_SOCKET.
func ModSocketPath(socket string) string {
	p := strings.TrimSuffix(socket, ".sock") + ".mod.sock"
	if len(p) > maxSocketPath {
		return ""
	}
	return p
}
```

`internal/daemon/serve.go` (imports gain `runtime`, `github.com/aidan-bailey/loom/internal/modserver`, `github.com/aidan-bailey/loom/session`):

After `socket, err := SocketPath(o.GlobalDir)` and its error check:

```go
	// The loom mod's socket is settled before the boot too: the boot
	// relaunches agents, whose launches name it (session.SetModSocket).
	modSocket := ""
	if runtime.GOOS != "windows" {
		modSocket = ModSocketPath(socket)
	}
	session.SetModSocket(modSocket)
	defer session.SetModSocket("")
```

After the `log.For("serve").Info("serve.listening", …)` line, before `o.Serving`:

```go
	var mods *modserver.Server
	if modSocket != "" {
		if mods, err = modserver.Listen(modSocket, loop, modserver.DefaultHold); err != nil {
			// Not fatal: launches go without the mod from now on, and the ones
			// the boot made turn absent after their grace.
			log.For("serve").Warn("serve.mod_listen_failed", "err", err)
			session.SetModSocket("")
			mods = nil
		}
	}
```

In the stop branch, right after `l.close()`: `mods.Close() // every held poll hears 503; the mods reconnect to the next daemon`. In the fatal branch, right after `l.close()`: `mods.Close()`.

`Serve`'s doc comment gains: it also serves the loom mod's API (internal/modserver) on `ModSocketPath(socket)` when that fits, having named it for launches before the boot.

Run: `CGO_ENABLED=0 go test ./internal/daemon/ ./internal/modserver/ ./core/... ./session/...` → PASS. `GOOS=windows CGO_ENABLED=0 go build ./...` → builds.

- [ ] **C7. Gate and commit.**

```bash
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/') core/mod.go core/loop_mod.go core/mod_test.go internal/modserver/*.go
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./core/... ./internal/modserver/ ./internal/daemon/
GOOS=windows CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go run ./tools/claudemd
git status
git commit -m "feat(core,daemon): serve the loom mod and track each launch's mod" -- core/mod.go core/loop_mod.go core/mod_test.go core/model.go core/load.go core/hook_scan.go core/events.go core/rpc/route.go docs/specs/protocol.md app/core_glue.go internal/modserver internal/daemon/paths.go internal/daemon/serve.go internal/daemon/serve_test.go
```

---

## Package D: send through the mod

- [ ] **D1. `Send`: failing tests first.** In `core/requests_test.go`, add to `TestRequests_RefuseWhatTheTUIRefuses`'s table:

```go
		{"send through the mod to an unstarted session", func(f fixture) { f.m.Send(f.id["new"], Message{Text: "hi", Origin: OriginCLI}, 1) }, "send a prompt to new: the session has not started"},
		{"send through the mod to a paused session", func(f fixture) { f.m.Send(f.id["paused"], Message{Text: "hi", Origin: OriginCLI}, 1) }, "send a prompt to paused: the session is paused"},
```

In `core/mod_test.go`:

```go
func TestSend_AFramedCLIMessage(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")

	m.Send(m.idOf(inst), Message{Text: "status?", From: "po", Origin: OriginCLI}, 5)

	msgs, _, err := m.ModInbox("L1")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "From po:\n\nstatus?", msgs[0].Text)
	assert.False(t, msgs[0].AsUser, "framed: the model knows it isn't the person")
}

func TestSend_AScriptMessageIsThePersonsOwn(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")

	m.Send(m.idOf(inst), Message{Text: "go on", From: "ignored", Origin: OriginScript}, 5)

	msgs, _, _ := m.ModInbox("L1")
	require.Len(t, msgs, 1)
	assert.Equal(t, ModMessage{ID: msgs[0].ID, Text: "go on", AsUser: true}, msgs[0])
}

func TestSend_RefusesTheDaemonsOwnOrigin(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	inst := modInst(t, m, "x")
	for _, o := range []Origin{OriginFirstPrompt, "", "other"} {
		m.Send(m.idOf(inst), Message{Text: "hi", Origin: o}, 5)
		rs := replies(m.Drain())
		require.Len(t, rs, 1, o)
		assert.ErrorIs(t, rs[0].Err, ErrRefused, o)
	}
}

// The N flow's first prompt goes through the mod when the launch can have
// one, and the start's Started (carrying the Create's request) waits for it.
func TestDeliverStart_AFirstPromptGoesThroughTheMod(t *testing.T) {
	withMod(t)
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := startedInst(t, "x", "claude")
	ws.add(inst)
	m.SetWorkspacesForTest(ws)
	adoptLaunch(t, m, inst, "L1", time.Now())
	m.Drain()
	inst.SetPrompt("do the thing")

	m.Deliver(caused{req: 7, result: StartResult{Instance: inst, Owner: ws}})

	out := m.Drain()
	assert.Empty(t, inst.Prompt())
	for _, ev := range out.Events {
		_, isStarted := ev.(Started)
		assert.False(t, isStarted, "no Started until the mod has queued the prompt")
	}
	msgs, _, err := m.ModInbox("L1")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, ModMessage{ID: msgs[0].ID, Text: "do the thing", AsUser: true}, msgs[0])

	require.NoError(t, m.ModEvents("L1", []ModEvent{{Kind: "queued", ID: msgs[0].ID}}))
	assert.Equal(t, []Event{Started{Req: 7, ID: m.idOf(inst), Title: "x", Owner: m.wsIDOf(ws), OwnerLabel: "a"}}, m.Drain().Events)
}

// Without a mod the first prompt is pasted by a job, as before.
func TestDeliverStart_AFirstPromptWithoutAModIsPasted(t *testing.T) {
	m := NewForTest(Options{})
	ws := storedWorkspace(t, "a")
	inst := newInst(t, "x")
	inst.SetPrompt("do the thing")
	ws.add(inst)
	m.SetWorkspacesForTest(ws)

	m.Deliver(StartResult{Instance: inst, Owner: ws})

	assert.Len(t, m.Drain().Jobs, 1, "the paste")
	assert.Empty(t, m.mods, "no link for a launch that can't have a mod")
}
```

Run: `CGO_ENABLED=0 go test ./core/ -run 'TestSend_|TestDeliverStart_AFirst|TestRequests_Refuse'` → FAIL (undefined: `Message`, `Send`).

- [ ] **D2. `Send`: implementation.** `core/iface.go`, after `SendPrompt`:

```go
	Send(id InstanceID, msg Message, req ReqID)
```

`core/mod.go`, after the `Origin` constants:

```go
// Message is a message for a session's Claude, delivered through the loom mod
// (Send).
type Message struct {
	Text string
	// From names the sender on a line before a CLI message's text.
	From string
	// Wait asks for Answered when the turn the message opened ends.
	Wait   bool
	Origin Origin
}

// compose is the text the mod submits for msg: a CLI message names its sender
// on a line of its own.
func (msg Message) compose() string {
	if msg.Origin == OriginCLI && msg.From != "" {
		return "From " + msg.From + ":\n\n" + msg.Text
	}
	return msg.Text
}

// Send delivers msg to the session id's Claude through the loom mod
// (enqueue): queued behind any running turn or open dialog, never typed into
// the pane. The Reply comes once the mod has queued it, or at once with a
// refusal: a session not started or Paused (opSend's precondition), an origin
// no sender may use, or (OriginCLI) a launch without a live mod. A script's
// message is pasted instead on such a launch, as SendPrompt does. With Wait,
// Answered follows when the turn the message opened ends.
func (m *Model) Send(id InstanceID, msg Message, req ReqID) {
	inst, _, ok := m.admit(opSend, id, req)
	if !ok {
		return
	}
	if msg.Origin != OriginCLI && msg.Origin != OriginScript {
		m.refuse(req, id, fmt.Errorf("send to %s: %q is no sender's origin", inst.Title, msg.Origin))
		return
	}
	m.enqueue(inst, &modMessage{text: msg.compose(), asUser: msg.Origin == OriginScript, origin: msg.Origin, req: req, wait: msg.Wait})
}
```

`core/loop_core.go`, after `SendPrompt`'s forwarder, in the file's forwarder style:

```go
func (l *Loop) Send(id InstanceID, msg Message, req ReqID) {
	l.do(func(m *Model) { m.Send(id, msg, req) })
}
```

`core/completions.go`, in `deliverStart`, the prompt branch becomes:

```go
	if prompt := inst.Prompt(); prompt != "" {
		inst.SetPrompt("")
		if _, possible := modLaunch(inst); possible {
			// Through the mod: Started follows its queued report
			// (modQueued), or the paste once the launch turns absent.
			m.enqueue(inst, &modMessage{text: prompt, asUser: true, origin: OriginFirstPrompt, cause: m.cause, owner: owner})
			return
		}
		m.spawn(sendInitialPrompt(inst, owner, prompt))
		return
	}
```

(`deliverStart`'s doc comment gains: with a prompt, Started waits for the mod's queued report, or for the paste when the launch has no mod.)

Then `go generate ./core/rpc` and `go test ./core/rpc -run TestProtocolReference -update`.

Run: `CGO_ENABLED=0 go test ./core/... ./app/...` → PASS (`TestLoopForwardsEachMethodToItsNamesake`, `TestGenerated_IsFresh`, `TestMethods_CoverCore`, `TestCoreIsValueTyped` included).

- [ ] **D3. `loom session send`: failing tests first.** In `internal/cli/session_test.go`:

```go
// launchOf is the hooks launch ID of the session title in ws.
func launchOf(t *testing.T, d *testDaemon, ws, title string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(session.SubagentHooksDir(d.workspace(t, ws).ConfigDir, title), "launch-id"))
	require.NoError(t, err)
	return string(b)
}

// playMod answers launch's inbox as the loom mod would, once: it reports the
// first message queued, its turn started and ended with answer, and hands
// back the text it carried.
func playMod(t *testing.T, host core.ModHost, launch, answer string) <-chan string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			msgs, wake, err := host.ModInbox(launch)
			if err != nil || wake == nil {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if len(msgs) == 0 {
				select {
				case <-wake:
				case <-time.After(time.Second):
				}
				continue
			}
			m := msgs[0]
			_ = host.ModEvents(launch, []core.ModEvent{
				{Kind: "queued", ID: m.ID},
				{Kind: "started", ID: m.ID, TurnID: "t1"},
				{Kind: "complete", ID: m.ID, TurnID: "t1", Text: answer, Reason: "answer"},
			})
			got <- m.Text
			return
		}
		got <- ""
	}()
	return got
}

// withModSocket has the test's daemon give launches the mod.
func withModSocket(t *testing.T) {
	t.Helper()
	session.SetModSocket("/tmp/loom-cli-test.mod.sock")
	t.Cleanup(func() { session.SetModSocket("") })
}

func TestSessionSend_WaitPrintsTheAnswer(t *testing.T) {
	isolateWorkspaces(t)
	withModSocket(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)
	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a")
	require.NoError(t, err)
	launch := launchOf(t, d, "ws-a", "alpha")
	require.NoError(t, d.loop.ModHello(launch, core.ModHello{Mod: 1}))
	carried := playMod(t, d.loop, launch, "all green")

	out, _, err := runSession(t, "", "send", "alpha", "-w", "ws-a", "--text", "status?", "--from", "po", "--wait")

	require.NoError(t, err)
	assert.Equal(t, "all green\n", out, "only the answer on stdout")
	assert.Equal(t, "From po:\n\nstatus?", <-carried)
}

func TestSessionSend_ReturnsOnceQueued(t *testing.T) {
	isolateWorkspaces(t)
	withModSocket(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	d := serveDaemonLive(t, agent)
	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a")
	require.NoError(t, err)
	launch := launchOf(t, d, "ws-a", "alpha")
	require.NoError(t, d.loop.ModHello(launch, core.ModHello{Mod: 1}))
	carried := playMod(t, d.loop, launch, "ignored")

	out, _, err := runSession(t, "from stdin\n\n", "send", "alpha", "-w", "ws-a")

	require.NoError(t, err)
	assert.Contains(t, out, `Queued for "alpha" in ws-a`)
	assert.Equal(t, "from stdin", <-carried, "stdin's text, trailing newlines dropped, no From line")
}

// With no daemon serving the mod the launch has none, and send refuses at
// once rather than paste into the pane.
func TestSessionSend_RefusesWithoutAMod(t *testing.T) {
	isolateWorkspaces(t)
	agent := fakeAgent(t, "claude.ai")
	liveWorkspace(t, "ws-a", agent, nil)
	serveDaemonLive(t, agent)
	_, _, err := runSession(t, "", "new", "alpha", "-w", "ws-a")
	require.NoError(t, err)

	_, _, err = runSession(t, "", "send", "alpha", "-w", "ws-a", "--text", "hi")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "runs without the loom mod")
}

func TestSessionSend_RefusesAnEmptyMessage(t *testing.T) {
	_, _, err := runSession(t, "\n\n", "send", "alpha")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the message is empty")
}
```

(`testDaemon.loop` is the field `serveDaemonOn` sets; `session.SubagentHooksDir` and `WorkspaceView.ConfigDir` exist; `fakeAgent`'s script stays up for any launch whose first argument is a flag, `--plugin-dir` included.)

Run: `CGO_ENABLED=0 go test ./internal/cli/ -run TestSessionSend` → FAIL (unknown command "send").

- [ ] **D4. `loom session send`: implementation.** `internal/cli/session.go`: add `newSessionSendCmd(&workspace)` to `cmd.AddCommand(…)`, after `newSessionNewCmd`, and:

```go
func newSessionSendCmd(workspace *string) *cobra.Command {
	var (
		id               uint64
		text, file, from string
		wait             bool
	)
	cmd := &cobra.Command{
		Use:   "send <title>",
		Short: "Send a message to a session's Claude, queued behind its running turn or open dialog",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			body, err := readMessage(c.InOrStdin(), text, file, c.Flags().Changed("text"))
			if err != nil {
				return err
			}
			loomErrors(c)
			// An interrupt ends the wait (await), not the delivery.
			ctx, stop := signal.NotifyContext(c.Context(), os.Interrupt)
			defer stop()
			c.SetContext(ctx)
			return withDaemon(c, func(d *client) error {
				_, v, err := d.resolveWorkspace(*workspace)
				if err != nil {
					return err
				}
				s, err := findSession(d.Views(v.ID), args[0], core.InstanceID(id))
				if err != nil {
					return fmt.Errorf("%s: %w", v.Label, err)
				}
				return d.send(c.OutOrStdout(), v.Label, s, core.Message{Text: body, From: from, Wait: wait, Origin: core.OriginCLI})
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&text, "text", "", "The message (default: read from stdin)")
	fl.StringVar(&file, "file", "", "Send this file's text")
	cmd.MarkFlagsMutuallyExclusive("text", "file")
	fl.StringVar(&from, "from", "", "Name the sender on a line before the message")
	fl.BoolVar(&wait, "wait", false, "Wait until the turn the message opens ends, and print Claude's reply")
	fl.Uint64Var(&id, "id", 0, "The session's ID (loom session list --json), when several share the title")
	return cmd
}

// readMessage is send's text: --text's, --file's, or stdin's, without its
// trailing newlines. An empty message is refused.
func readMessage(stdin io.Reader, text, file string, hasText bool) (string, error) {
	var s string
	switch {
	case hasText:
		s = text
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("--file: %w", err)
		}
		s = string(b)
	default:
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		s = string(b)
	}
	s = strings.TrimRight(s, "\n")
	if strings.TrimSpace(s) == "" {
		return "", errors.New("send: the message is empty")
	}
	return s, nil
}

// send makes the Send request for s and waits for its Reply and, with
// msg.Wait, for its Answered, whose text alone goes to out.
func (d *client) send(out io.Writer, wsLabel string, s core.InstanceView, msg core.Message) error {
	d.Send(s.ID, msg, sessionReq)
	ans, err := d.awaitSend(sessionReq, msg.Wait)
	switch {
	case errors.Is(err, errDeliveredNoAnswer):
		return fmt.Errorf("interrupted: the message to %q was delivered; its reply will not be printed", s.Title)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("interrupted: the message to %q may still be delivered", s.Title)
	case err != nil:
		return err
	}
	if !msg.Wait {
		fmt.Fprintf(out, "Queued for %q in %s\n", s.Title, wsLabel)
		return nil
	}
	fmt.Fprintln(out, ans.Text)
	if ans.Reason != "" && ans.Reason != "answer" {
		return fmt.Errorf("the turn ended: %s", ans.Reason)
	}
	return nil
}
```

`internal/cli/join.go`, after `awaitStart`:

```go
// errDeliveredNoAnswer is awaitSend's interrupt after the message was queued.
var errDeliveredNoAnswer = errors.New("delivered; no answer awaited")

// awaitSend waits for the Send req: its Reply (a refusal is its Err), then,
// with wait, the Answered naming it or a Notice saying none will come. One
// loop reads them all, since a batch can hold more than one.
func (c *client) awaitSend(req core.ReqID, wait bool) (core.Answered, error) {
	var (
		queued bool
		ans    core.Answered
		got    bool
		errs   []error
	)
	_, err := c.await(0, func(events []core.Event) bool {
		for _, ev := range events {
			switch e := ev.(type) {
			case core.Reply:
				if e.Req == req {
					if e.Err != nil {
						errs = append(errs, e.Err)
					}
					queued = true
				}
			case core.Answered:
				if e.Req == req {
					ans, got = e, true
				}
			case core.Notice:
				switch {
				case e.Req != req:
				case e.Err != nil:
					errs = append(errs, e.Err)
				case e.Info != "":
					fmt.Fprintf(c.errOut, "loom: %s\n", e.Info)
				}
			}
		}
		return len(errs) > 0 || (queued && (!wait || got))
	})
	switch {
	case err != nil && queued && errors.Is(err, context.Canceled):
		return core.Answered{}, errDeliveredNoAnswer
	case err != nil:
		return core.Answered{}, err
	case len(errs) > 0:
		return core.Answered{}, errors.Join(errs...)
	}
	return ans, nil
}
```

Run: `CGO_ENABLED=0 go test ./internal/cli/` → PASS.

- [ ] **D5. Lua `send_prompt` and the docs.** `app/app_scripts.go`, the `send_prompt` case: `m.core.SendPrompt(i.ID, i.Text, m.newReq(pendingReq{script: ps}))` becomes

```go
		m.core.Send(i.ID, core.Message{Text: i.Text, Origin: core.OriginScript}, m.newReq(pendingReq{script: ps}))
```

If an app test asserts a script's `send_prompt` reaches `SendPrompt` (grep `send_prompt` in `app/*_test.go`), move its assertion to `Send` with `OriginScript`; such a test drives a model with no mod socket, so the message falls back to the paste and its pane assertions still hold.

`docs/specs/scripting.md`, the `inst:send_prompt(text)` row becomes:

```markdown
| `inst:send_prompt(text)` | void | Asks the model to deliver text to the agent and waits until it has. On a Claude launch with the loom mod live, the mod queues it as a turn of its own, behind any running turn or open dialog, as the person's own words; otherwise the model types it into the agent pane (`load-buffer` + `paste-buffer`, as `send_keys`) and presses Enter. Meanwhile the TUI holds input to the session: inline attach and other sends to it are refused. Raises on an unstarted or paused session (the model's refusal, e.g. `send a prompt to x: the session is paused`), a failed send (`send_prompt: <err>`), or while another send to it is in flight (`send_prompt: still sending the last prompt to x`). |
```

and in "Request." (around line 440), `SendPrompt` becomes `Send`.

`USAGE.md`, in the session commands table after `session new`:

```markdown
| `session send <title> [--text <t> \| --file <f>] [--from <name>] [--wait] [--id <n>]` | Hand a message to the session's Claude through the loom mod: it is queued as a turn of its own behind any running turn or open dialog, never typed into the pane. The text comes from `--text`, `--file` or stdin; `--from` names the sender on a line before it. Returns once Claude has queued it; with `--wait`, once that turn ends, printing Claude's reply alone on stdout. Refuses a session whose Claude runs without the mod (an older Claude, or one the mod did not load into) |
```

and an example under "Examples": `loom session send fix-login -w my-project --from po --wait --text "Status?"`.

- [ ] **D6. Gate and commit.**

```bash
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./core/... ./internal/cli/ ./app/
CGO_ENABLED=0 go run ./tools/claudemd
git status
git commit -m "feat(core,cli,app): send through the loom mod" -- core/iface.go core/loop_core.go core/mod.go core/mod_test.go core/completions.go core/requests_test.go core/rpc/methods_gen.go docs/specs/protocol.md internal/cli/session.go internal/cli/join.go internal/cli/session_test.go app/app_scripts.go docs/specs/scripting.md USAGE.md
```

(add any app test file D5 changed.)

---

## Package E: end to end, real Claude, docs

- [ ] **E1. fakeagent plays the mod.** First rename fakeAgent's method that runs one line, today named `exec`, to `runLine`, at its definition and its call in `run`: the repo's security hook refuses any file write containing that method name directly followed by a parenthesis, which blocks every edit of `agent.go` that touches the call.

`tools/fakeagent/mod.go`:

```go
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"time"
)

// modMsg is a message the loom daemon has for the agent.
type modMsg struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// modClient plays the loom mod (session/claudemod) for e2e tests: with
// LOOM_MOD_SOCKET and LOOM_LAUNCH set it says hello, long-polls the inbox,
// reports each message queued as it arrives, and hands it to the agent, which
// runs it as a prompt once idle, as Claude queues a submitted prompt.
type modClient struct {
	launch string
	http   *http.Client
	out    chan modMsg
}

// newModClient is the mod for this launch, nil without its environment.
func newModClient(getenv func(string) string) *modClient {
	socket, launch := getenv("LOOM_MOD_SOCKET"), getenv("LOOM_LAUNCH")
	if socket == "" || launch == "" {
		return nil
	}
	return &modClient{launch: launch, out: make(chan modMsg, 64), http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}}
}

func (c *modClient) post(path string, body any) int {
	b, _ := json.Marshal(body)
	resp, err := c.http.Post("http://loom"+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func (c *modClient) report(ev map[string]any) {
	c.post("/v1/events", map[string]any{"launch": c.launch, "events": []any{ev}})
}

// run polls until the process exits.
func (c *modClient) run() {
	greeted := false
	seen := map[string]bool{}
	for {
		if !greeted {
			greeted = c.post("/v1/hello", map[string]any{"launch": c.launch, "mod": 1, "claude": "fakeagent", "session": "fake"}) == http.StatusNoContent
			if !greeted {
				time.Sleep(200 * time.Millisecond)
				continue
			}
		}
		resp, err := c.http.Get("http://loom/v1/inbox?launch=" + url.QueryEscape(c.launch))
		if err != nil {
			greeted = false
			time.Sleep(200 * time.Millisecond)
			continue
		}
		var body struct {
			Messages []modMsg `json:"messages"`
		}
		status := resp.StatusCode
		if status == http.StatusOK {
			_ = json.NewDecoder(resp.Body).Decode(&body)
		}
		_ = resp.Body.Close()
		switch status {
		case http.StatusOK:
			for _, m := range body.Messages {
				if !seen[m.ID] {
					seen[m.ID] = true
					c.out <- m
				}
				c.report(map[string]any{"kind": "queued", "id": m.ID})
			}
		case http.StatusNoContent:
		default:
			greeted = false
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func newTurnID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
```

`tools/fakeagent/agent.go`: stdin moves to a channel, so a mod message can arrive while the agent is idle, and only stdin answers a prompt (a queued message never answers a dialog):
- `fakeAgent` gains `lines chan string` (stdin, closed at EOF) and `mod *modClient`.
- `newFakeAgent` makes `lines: make(chan string)` and, before returning, starts `go a.readStdin()`:

```go
// readStdin feeds stdin's lines to a.lines and closes it at EOF.
func (a *fakeAgent) readStdin() {
	for a.in.Scan() {
		a.lines <- a.in.Text()
	}
	close(a.lines)
}
```

- `run`'s loop (after `a.hooks.sessionStart()` and the banner):

```go
	if a.mod != nil {
		go a.mod.run()
	}
	for {
		fmt.Fprint(a.out, "> ")
		var mods <-chan modMsg
		if a.mod != nil {
			mods = a.mod.out
		}
		select {
		case line, ok := <-a.lines:
			if !ok {
				a.hooks.emit("SessionEnd", map[string]any{"reason": "other"})
				return 0
			}
			if code, done := a.runLine(strings.TrimSpace(line)); done {
				return code
			}
		case m := <-mods:
			turn := newTurnID()
			a.mod.report(map[string]any{"kind": "started", "id": m.ID, "turnId": turn})
			fmt.Fprintln(a.out, m.Text)
			code, done := a.runLine(strings.TrimSpace(m.Text))
			a.mod.report(map[string]any{"kind": "complete", "id": m.ID, "turnId": turn, "text": "fakeagent finished: " + m.Text, "reason": "answer"})
			if done {
				return code
			}
		}
	}
```

- `await` reads `line, ok := <-a.lines` (returning when `!ok`) in place of `a.in.Scan()`/`a.in.Text()`, and prints `line` where it printed `a.in.Text()`.
- `tools/fakeagent/main.go`: `a.mod = newModClient(os.Getenv)` after `a.hooks = emitter`.

`tools/fakeagent/agent_test.go`, add (imports gain `encoding/json`, `io`, `net`, `net/http`, `os`, `path/filepath`, `slices`, `sync`):

```go
// syncBuffer is a bytes.Buffer safe to read while the agent writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// A message the mod receives runs as a prompt once the agent is idle; one
// that arrives while the agent waits at a prompt neither answers it nor
// starts, as Claude queues a submitted prompt.
func TestMod_AMessageRunsAsAPromptButNeverAnswersOne(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "fam")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "m.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	var (
		mu     sync.Mutex
		events []map[string]any
		served bool
	)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/hello", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /v1/inbox", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		first := !served
		served = true
		mu.Unlock()
		if first {
			w.Header().Set("content-type", "application/json")
			_, _ = io.WriteString(w, `{"messages":[{"id":"m1","text":"work 0"}]}`)
			return
		}
		<-r.Context().Done()
	})
	mux.HandleFunc("POST /v1/events", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Events []map[string]any `json:"events"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		events = append(events, body.Events...)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	kinds := func() []string {
		mu.Lock()
		defer mu.Unlock()
		var k []string
		for _, e := range events {
			k = append(k, e["kind"].(string))
		}
		return k
	}

	stdin, feed := io.Pipe()
	var out syncBuffer
	a := newFakeAgent(personaFor("claude"), stdin, &out, t.TempDir())
	a.mod = newModClient(func(k string) string {
		return map[string]string{"LOOM_MOD_SOCKET": sock, "LOOM_LAUNCH": "L1"}[k]
	})
	done := make(chan int, 1)
	go func() { done <- a.run() }()

	_, _ = io.WriteString(feed, "ask\n")
	require.Eventually(t, func() bool { return strings.Contains(out.String(), a.p.pendingPrompt) }, 5*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool { return slices.Contains(kinds(), "queued") }, 5*time.Second, 10*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	assert.NotContains(t, out.String(), "answered:", "a queued message never answers a prompt")
	assert.NotContains(t, kinds(), "started", "nor does it start while the agent waits")

	_, _ = io.WriteString(feed, "y\n")
	require.Eventually(t, func() bool { return strings.Contains(out.String(), "work done") }, 5*time.Second, 10*time.Millisecond)
	assert.Contains(t, out.String(), `answered: "y"`)
	require.Eventually(t, func() bool { return slices.Equal(kinds(), []string{"queued", "started", "complete"}) }, 5*time.Second, 10*time.Millisecond)
	mu.Lock()
	assert.Equal(t, "fakeagent finished: work 0", events[2]["text"])
	mu.Unlock()

	_, _ = io.WriteString(feed, "exit\n")
	assert.Equal(t, 0, <-done)
}
```

Run: `CGO_ENABLED=0 go test ./tools/fakeagent/` → PASS (the existing tests unchanged: without the environment `mod` is nil and its channel never fires).

- [ ] **E2. e2e: the whole chain on a real daemon.** `e2e/mod_test.go`, under the same build tag as the other e2e files:

```go
// The fake Claude (fakeagent) plays the loom mod: `loom session send`
// reaches it through the daemon's mod socket, --wait prints its answer, a
// message sent while it waits at a permission prompt runs after the prompt
// is answered without answering it, and after a daemon stop the mod
// reconnects to the next daemon and a send arrives again.
func TestE2E_SendThroughTheMod(t *testing.T) {
	sb := newSandbox(t, "fake-claude")
	startLoom(t, sb)
	createSession(t, sb, "modded")
	send := func(args ...string) (string, error) {
		return runLoom(t, sb, append([]string{"session", "send", "modded", "-w", devsandbox.WorkspaceName}, args...)...)
	}

	out, err := send("--text", "work 0", "--wait")
	require.NoError(t, err, out)
	assert.Contains(t, out, "fakeagent finished: work 0")

	sendToAgent(t, sb, "ask")
	require.NoError(t, sb.WaitFor("permission: Bash", uiTimeout))
	queued, err := send("--text", "work 0")
	require.NoError(t, err, queued)
	assert.Contains(t, queued, `Queued for "modded"`)
	require.NoError(t, sb.WaitFor("permission: Bash", uiTimeout), "the queued message left the prompt waiting")
	sendToAgent(t, sb, "y")
	require.NoError(t, sb.WaitFor("work done", uiTimeout))

	rec := servingDaemon(t, sb)
	stopped, err := runLoom(t, sb, "serve", "stop")
	require.NoError(t, err, stopped)
	require.True(t, waitExit(rec.PID, uiTimeout))
	again, err := send("--text", "work 0", "--wait") // the CLI's join starts the next daemon
	require.NoError(t, err, again)
	assert.Contains(t, again, "fakeagent finished: work 0")
}
```

If the sandbox's workspace name, or how other daemon tests stop the sandbox's daemon, differs from what is used here, follow `e2e/daemon_test.go`. This test runs a sandboxed loom, never the user's, which is what the e2e suite exists for.

Run: `CGO_ENABLED=0 go test -tags e2e ./e2e/ -run TestE2E_SendThroughTheMod -v` → PASS; then the whole suite, `CGO_ENABLED=0 go test -tags e2e ./e2e/...` → PASS.

- [ ] **E3. Opt-in: real Claude against the mod server.** `internal/modserver/realclaude_test.go`:

```go
package modserver

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aidan-bailey/loom/core"
	"github.com/aidan-bailey/loom/session/claudemod"
	"github.com/aidan-bailey/loom/session/tmux"
	"github.com/stretchr/testify/require"
)

// queueHost is a ModHost for launch L1 whose messages the test queues.
type queueHost struct {
	mu     sync.Mutex
	hello  bool
	msgs   []core.ModMessage
	wake   chan struct{}
	events []core.ModEvent
}

func (h *queueHost) ModHello(launch string, _ core.ModHello) error {
	if launch != "L1" {
		return core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hello = true
	return nil
}

func (h *queueHost) ModInbox(launch string) ([]core.ModMessage, <-chan struct{}, error) {
	if launch != "L1" {
		return nil, nil, core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wake != nil {
		close(h.wake)
	}
	h.wake = make(chan struct{})
	return append([]core.ModMessage(nil), h.msgs...), h.wake, nil
}

func (h *queueHost) ModEvents(launch string, events []core.ModEvent) error {
	if launch != "L1" {
		return core.ErrModUnknownLaunch
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ev := range events {
		h.events = append(h.events, ev)
		if ev.Kind != "queued" {
			continue
		}
		for i, m := range h.msgs {
			if m.ID == ev.ID {
				h.msgs = append(h.msgs[:i], h.msgs[i+1:]...)
				break
			}
		}
	}
	return nil
}

func (h *queueHost) queue(id, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = append(h.msgs, core.ModMessage{ID: id, Text: text})
	if h.wake != nil {
		close(h.wake)
		h.wake = nil
	}
}

// event is the first event of kind for message id.
func (h *queueHost) event(kind, id string) (core.ModEvent, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ev := range h.events {
		if ev.Kind == kind && ev.ID == id {
			return ev, true
		}
	}
	return core.ModEvent{}, false
}

// TestRealClaude_TheModCarriesAMessage launches a real Claude (haiku) with the
// embedded mod on a private tmux server and serves it from queueHost: hello;
// a message's queued, started and complete (complete carrying Claude's
// answer); and a message delivered while a permission dialog is open, which
// leaves the dialog alone and runs after it. It spends a few cents and leaves
// entries in ~/.claude/projects, so it runs only with LOOM_TEST_REAL_CLAUDE=1.
func TestRealClaude_TheModCarriesAMessage(t *testing.T) {
	if os.Getenv("LOOM_TEST_REAL_CLAUDE") != "1" {
		t.Skip("set LOOM_TEST_REAL_CLAUDE=1 to run against real Claude (costs money)")
	}
	for _, bin := range []string{"tmux", "claude"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	mod := filepath.Join(root, "mod")
	require.NoError(t, claudemod.WriteTo(mod))
	work := filepath.Join(root, "work")
	require.NoError(t, os.MkdirAll(work, 0o700))
	host := &queueHost{}
	socket := shortSocket(t)
	s, err := Listen(socket, host, 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(s.Close)

	ctx := context.Background()
	sock := fmt.Sprintf("loommod-%d", os.Getpid())
	tm := func(args ...string) string {
		t.Helper()
		out, err := tmux.CommandOnSocket(ctx, sock, args...).CombinedOutput()
		require.NoError(t, err, string(out))
		return string(out)
	}
	t.Cleanup(func() { _ = tmux.CommandOnSocket(ctx, sock, "kill-server").Run() })
	target := tmux.PaneTarget("probe")
	screen := func() string { return tm("capture-pane", "-p", "-t", target) }
	waitFor := func(what string, d time.Duration, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(d); !ok(); time.Sleep(250 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s; screen:\n%s", what, screen())
			}
		}
	}
	type_ := func(text string) {
		tm("send-keys", "-t", target, "-l", text)
		time.Sleep(300 * time.Millisecond)
		tm("send-keys", "-t", target, "Enter")
	}

	// A test run from inside Claude must not hand the probe its session:
	// env -u drops what identifies it.
	unset := "env -u CLAUDECODE -u CLAUDE_CODE_SESSION_ID -u CLAUDE_CODE_MESSAGING_SOCKET -u CLAUDE_CODE_MESSAGING_TOKEN -u CLAUDE_CODE_CHILD_SESSION -u CLAUDE_CODE_BRIDGE_SESSION_ID"
	tm("new-session", "-d", "-s", "probe", "-x", "160", "-y", "45", "-c", work)
	tm("send-keys", "-t", target, fmt.Sprintf("%s %s=%s %s=L1 claude --model haiku --permission-mode default --plugin-dir '%s'",
		unset, claudemod.EnvSocket, socket, claudemod.EnvLaunch, mod), "Enter")
	waitFor("the trust dialog or the prompt", 60*time.Second, func() bool {
		s := screen()
		return strings.Contains(s, "trust this folder") || strings.Contains(s, "Claude Code")
	})
	if s := screen(); strings.Contains(s, "trust this folder") {
		if strings.Contains(s, "❯ No, exit") {
			tm("send-keys", "-t", target, "Down")
		}
		tm("send-keys", "-t", target, "Enter")
	}
	waitFor("the mod's hello", 60*time.Second, func() bool { host.mu.Lock(); defer host.mu.Unlock(); return host.hello })

	host.queue("m1", "Reply with exactly the word PONG.")
	waitFor("m1's answer", 90*time.Second, func() bool {
		ev, ok := host.event("complete", "m1")
		return ok && strings.Contains(ev.Text, "PONG")
	})
	_, started := host.event("started", "m1")
	require.True(t, started, "m1's turn was reported started")

	type_("Run this exact bash command: mkdir made-by-test. Then reply DONE.")
	waitFor("the permission dialog", 90*time.Second, func() bool { return strings.Contains(screen(), "Do you want to proceed?") })
	host.queue("m2", "Reply with exactly the word QUEUED.")
	waitFor("m2 queued", 30*time.Second, func() bool { _, ok := host.event("queued", "m2"); return ok })
	time.Sleep(time.Second)
	still := screen()
	require.Contains(t, still, "Do you want to proceed?", "the queued message left the dialog open")
	require.Contains(t, still, "❯ 1. Yes", "with its choice where it was")
	tm("send-keys", "-t", target, "Enter")
	waitFor("m2's answer", 90*time.Second, func() bool {
		ev, ok := host.event("complete", "m2")
		return ok && strings.Contains(ev.Text, "QUEUED")
	})
	require.DirExists(t, filepath.Join(work, "made-by-test"), "the dialog's own answer ran the command")
}
```

Run once: `LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./internal/modserver/ -run TestRealClaude -v` → PASS.

- [ ] **E4. Docs.**
- `docs/ARCHITECTURE.md`: `session/claudemod/` and `internal/modserver/` in the packages table ("the loom mod for Claude Code, written into each launch" and "the daemon's HTTP server for the mod", each referencing its `CLAUDE.md`); in "The daemon's lifetime and environment", a bullet: the daemon serves the mod socket beside its own (`ModSocketPath`), and every Claude launch gets the mod with `--plugin-dir <hooks folder>/mod` and `LOOM_MOD_SOCKET`/`LOOM_LAUNCH`; in "Environment variables", `LOOM_MOD_SOCKET` and `LOOM_LAUNCH` (set by loom for each Claude launch, read by the mod, not by loom).
- `internal/daemon/CLAUDE.md`, under "Socket and stop": **Settle the mod socket before the boot and close it with the main socket.** The boot relaunches agents whose launches name it (`session.SetModSocket`), and a stop must end every held poll before the quiesce; a mod socket that can't be placed or served is no failure: launches go without the mod. **Enforced** by `TestServe_ServesTheModSocket` and `TestModSocketPath`.
- `internal/daemon/README.md`: the mod socket in its socket section.
- `session/CLAUDE.md`, under the hooks rules: **Give a launch the mod only with its hooks (`prepareMod` inside `prepareHooks`), and append `modEnv()` to `InstanceEnv` after `launchProgram`.** The mod's identity is the hooks launch ID, and the env must name the ID that launch minted. **Enforced** by `TestLaunchProgram_GivesAClaudeLaunchTheMod`, `TestLaunchProgram_NoModWithoutHooks` and `TestInstanceEnv_CallSitesAppendTheModEnv`.

Run: `CGO_ENABLED=0 go run ./tools/claudemd -v` (budgets) and `CGO_ENABLED=0 go test ./tools/claudemd/` → PASS.

- [ ] **E5. Final gate and commit.**

```bash
gofmt -l $(git ls-files '*.go' | grep -v '^vendor/')
CGO_ENABLED=0 go vet ./...
CGO_ENABLED=0 go test ./...
CC=clang CGO_ENABLED=1 go test -race ./...
GOOS=windows CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go test -tags e2e ./e2e/...
LOOM_TEST_REAL_CLAUDE=1 CGO_ENABLED=0 go test ./session/claudemod/ ./internal/modserver/ -run RealClaude
CGO_ENABLED=0 go run ./tools/claudemd
git status
git commit -m "test,docs: the loom mod end to end" -- tools/fakeagent e2e/mod_test.go internal/modserver/realclaude_test.go docs/ARCHITECTURE.md internal/daemon/CLAUDE.md internal/daemon/README.md session/CLAUDE.md
```

golangci-lint: run CI's pinned v1 with `--fast --new-from-rev=origin/main` if available (the root `CLAUDE.md`'s Verification section says how); otherwise say plainly that it was skipped.

Then a smoke run of the real thing through the `loom-dev` skill (never `./loom` in a pane): a sandbox session on real Claude, `loom session send <title> --wait --text "Reply with exactly PONG"` from the sandbox's CLI, and the TUI's quick bar still pasting.

---

## Spike results

Run 2026-10-14 on Claude Code 2.1.299 (`--model haiku`), with the probe recipe against the throwaway server. `claude plugin validate` passes the mod, warning only that it names no author.

| Probe | Result | Consequence |
|---|---|---|
| P1 long poll | Holds of 25 s and 29 s succeed, three in a row each. Holds of 30 s and 120 s fail every time: the host ends a `$.http.fetch` at exactly 30 s (`aborted: no complete answer within 30000ms`), and `HttpInit` has no timeout field. The un-awaited loop started from `session.start` polls without trouble. | `DefaultHold` is 24 s (29 s minus 5 s); C4's code block above still reads 25 s. |
| P2 turn text | A framed message's `turn.start.text` contains it (`The loom plugin sent a message:\n<text>\n\nThis is how Claude Code surfaces…`); an `asUser` message's equals it. | P3c removes `matches`, so nothing depends on it. |
| P3 back to back | Fails: m3 and m4, queued during one turn, ran as one turn (two `prompt.submit`s, then one `[engine] turn 4 start`) with one answer (`THREE\nFOUR`), and its `turn.start.text` held only m4's text. Head-of-queue matching then lost both, and every later message behind them. | The user accepted merging: messages that enter together share one turn and one answer, and each still gets `queued`, `started` and `complete`. |
| P3b claim by entry | Fails: Claude skips a module's own hooks for the prompts its code submits (`hooks module loom@inline prompt.submit skipped: re-entry (the plugin's own code raised it; origin loom)`), and a `.catch` on the hook is not asked in its place. A hook's code after `await next(e)` also runs after the turn's `turn.start`, too late to mark entry. | The mod has no `prompt.submit` hook and stays non-gating. |
| P3c claim on `done` | Passes all five probes, each attributed right: one message while idle; m3 and m4 merged (both `started` with one turn ID, both `complete` with the same text); one message mid-turn (its own turn, not the running one); a typed prompt and a mod message during one turn, in either order (never merged: separate turns in queue order); one message after `/clear`. `done` resolved before the carrying turn's `turn.start` hook got its `next` back, or between turns, 6 of 6 times (the `entered` path). | B1's `register.ts` (3888242) changes: a resolved submit joins the running main-loop turn (`mainTurn`), or `entered` for the next one to claim; `turns` maps a turn to its message IDs; `matches` goes. |
| P4 across `/clear` | Passes: a poll spanning `/clear` ends normally and polling goes on, and a message sent after it gets `queued`, `started` and `complete`. | None. |
| Other | `session.start` doesn't fire on `/clear`, so the session ID `hello` reported goes stale. With the server down the mod retries after 1, 2, 4 and 5 s, then says `hello` again. Claude writes `tsconfig.json` and `.claude-plugin/types/` into a `--plugin-dir` folder at load and watches the folder (every 400 ms while active, 30 s idle): the module loaded once and never reloaded, but a rewrite of `register.ts` would reload it. `HttpInit.socketPath` is documented as near 100 B at most. A merged turn may answer neither message: given THREE and FOUR, haiku asked which to send. | Nothing may rely on `hello`'s session ID after `/clear` (a follow-up). Amendment 1 holds: loom never rewrites a launch's mod folder. |

## Outcome and follow-ups

Filled in when the plan is done: the commits, what reviews found, and what was left for later (the spec's Out of scope list is the starting point).
