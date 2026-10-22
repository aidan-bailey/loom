// The loom mod: carries the messages loom's daemon has for this Claude into
// its prompt queue. Claude runs each once the session is idle (messages that
// waited through the same turn run together, as one turn), so a message
// never answers an open dialog or joins a running turn. Everything else stays
// in loom: the daemon composes the text and decides what to send, and this
// module only carries it and reports when each message is queued, when its
// turn starts and how the turn ended. A message the daemon marks as a slash
// command runs through Claude's command queue instead ($.command.run), and
// the hello lists this Claude's commands for the daemon to judge one by.
//
// loom writes this folder into each launch's hooks folder and passes it with
// --plugin-dir. LOOM_MOD_SOCKET names the daemon's mod socket and LOOM_LAUNCH
// this launch; without both the module does nothing.

import type { CommandRunResult, EngineInterface, PromptSubmitResult, Register } from 'claude-code'

const MOD_VERSION = 1
const BASE = 'http://loom/v1'
const MAX_BACKOFF_MS = 5000
const KEEP_SUBMITTED = 200

type Message = { id: string; text: string; asUser: boolean; command?: string; args?: string }
type ModEvent = {
  kind: 'queued' | 'started' | 'complete' | 'failed'
  id: string
  turnId?: string
  text?: string
  reason?: string
  error?: string
}

// POLLER names this module instance to the daemon, in its hello and every
// poll: minted once as the module loads, so a reloaded module has a new one.
// The daemon lets only the latest hello's poller poll (409 for any other), so
// an old pump that outlived a reload stops rather than wake the new one's
// held poll with each of its own.
const POLLER = Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2)
// LOADED is when this module instance loaded, sent in its hello: after a
// daemon restart every instance still pumping says hello again on its own
// backoff, and the daemon keeps the one loaded last whichever hello lands
// last, answering a stale instance's hello 409 too.
const LOADED = Date.now()

let socket = ''
let launch = ''
// pumping is set once the pump starts, and stays set: a pump a 409 stopped is
// never started again in this module instance.
let pumping = false
// submitted are the IDs this process has handed to Claude, newest last, so a
// message the daemon offers again (its queued report was lost) is reported
// again, not submitted twice.
const submitted: string[] = []
// entered are messages whose submit resolved while no main-loop turn ran; the
// next main-loop turn carries them.
const entered: Message[] = []
// mainTurn is the running main-loop turn, if any: set once its turn.start
// has its turn ID back from next, cleared as its turn.complete or the next
// main-loop turn.start begins.
let mainTurn: string | undefined
// turns maps a started turn to the messages it carries: Claude runs every
// prompt queued while it was busy in one turn.
const turns = new Map<string, string[]>()
// reports holds each submitted message's latest report until its complete or
// failed: the next report for it waits on it, so the daemon hears queued,
// started and complete in that order (it ignores what it hears about a
// message it has not seen queued).
const reports = new Map<string, Promise<void>>()

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

// hello says the module is here and returns the daemon's answer: 204, or 409
// when a module instance loaded later holds the launch.
async function hello($: EngineInterface): Promise<number> {
  const version = await $.session.version()
  const session = await $.session.id()
  const r = await call($, 'POST', '/hello', {
    launch,
    mod: MOD_VERSION,
    claude: version.version,
    session,
    poller: POLLER,
    loaded: LOADED,
    commands: await commandList($),
  })
  return r.status
}

// commandList is the slash commands this Claude lists, with their sources,
// for the daemon to judge a command message by; none when the list fails, so
// the daemon runs only the built-ins it allows.
async function commandList($: EngineInterface): Promise<{ name: string; source: string }[]> {
  try {
    return (await $.command.list()).map(c => ({ name: c.name, source: c.source }))
  } catch (err) {
    $.ui.log(`loom: list commands: ${String(err)}`, { to: 'debug' })
    return []
  }
}

// superseded logs that a newer instance of this module holds the launch; the
// pump then stops for good. Reports stay: what this one submitted still
// reports its turn.
function superseded($: EngineInterface) {
  $.ui.log('loom: superseded by a newer loom mod', { to: 'debug' })
}

// reportInOrder reports events once each message's earlier reports are
// through. The caller's bookkeeping stays synchronous; only the post waits.
function reportInOrder($: EngineInterface, events: ModEvent[]): Promise<void> {
  const earlier = events.map(ev => reports.get(ev.id))
  const posted = Promise.all(earlier).then(() => report($, events))
  for (const ev of events) {
    if (ev.kind === 'complete' || ev.kind === 'failed') reports.delete(ev.id)
    else reports.set(ev.id, posted)
  }
  return posted
}

// claim records that a turn carries ms and reports each as started.
async function claim($: EngineInterface, turnId: string, ms: Message[]) {
  turns.set(turnId, [...(turns.get(turnId) ?? []), ...ms.map(m => m.id)])
  await reportInOrder($, ms.map(m => ({ kind: 'started' as const, id: m.id, turnId })))
}

// A submit resolves as the turn carrying its prompt starts (on Claude 2.1.299,
// while that turn's turn.start hook is still in next), so a resolved message
// belongs to the running main-loop turn, or to the next one when none has
// started yet. The claim is made as the submit resolves, never after the
// queued report's round trip, which can outlast the turn.
async function submit($: EngineInterface, m: Message) {
  if (submitted.includes(m.id)) {
    await report($, [{ kind: 'queued', id: m.id }])
    return
  }
  let done: Promise<PromptSubmitResult>
  try {
    done = $.prompt.submit(m.asUser ? { text: m.text, asUser: true } : { text: m.text })
  } catch (err) {
    // Nothing entered Claude's queue, so no queued: the daemon falls back.
    await report($, [{ kind: 'failed', id: m.id, error: String(err) }])
    return
  }
  submitted.push(m.id)
  if (submitted.length > KEEP_SUBMITTED) submitted.shift()
  const queued = reportInOrder($, [{ kind: 'queued', id: m.id }])
  done.then(
    r => {
      if (r.drop !== undefined) void reportInOrder($, [{ kind: 'failed', id: m.id, error: `dropped: ${r.drop}` }])
      else if (mainTurn !== undefined) void claim($, mainTurn, [m])
      else entered.push(m)
    },
    err => {
      void reportInOrder($, [{ kind: 'failed', id: m.id, error: String(err) }])
    },
  )
  await queued
}

// run hands a command message to Claude's command queue ($.command.run),
// which runs it once the session is idle: queued as it is handed over, then
// complete with the command's output, or failed. A command starts no turn of
// its own to claim (a skill's run resolves as its turn starts), so it reports
// no started.
async function run($: EngineInterface, m: Message) {
  if (submitted.includes(m.id)) {
    await report($, [{ kind: 'queued', id: m.id }])
    return
  }
  let done: Promise<CommandRunResult>
  try {
    done = $.command.run({ command: m.command ?? '', args: m.args ?? '' })
  } catch (err) {
    // Nothing entered Claude's queue, so no queued: the daemon refuses it.
    await report($, [{ kind: 'failed', id: m.id, error: String(err) }])
    return
  }
  submitted.push(m.id)
  if (submitted.length > KEEP_SUBMITTED) submitted.shift()
  const queued = reportInOrder($, [{ kind: 'queued', id: m.id }])
  done.then(
    r => void reportInOrder($, [{ kind: 'complete', id: m.id, text: r.text ?? '' }]),
    err => void reportInOrder($, [{ kind: 'failed', id: m.id, error: String(err) }]),
  )
  await queued
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
        const status = await hello($)
        if (status === 409) {
          superseded($)
          return
        }
        greeted = status === 204
        if (!greeted) {
          delay = backoff(delay)
          continue
        }
      }
      const r = await call($, 'GET', `/inbox?launch=${encodeURIComponent(launch)}&poller=${POLLER}`)
      if (r.status === 409) {
        superseded($)
        return
      }
      if (r.status === 200) {
        const { messages } = JSON.parse(r.text) as { messages: Message[] }
        for (const m of messages) await (m.command ? run($, m) : submit($, m))
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

// ensurePump starts the pump once, when the launch names the daemon's socket
// and its launch ID. session.start starts it, and every turn hook makes sure
// of it: a reloaded module runs register again, with no session.start after.
// It never rejects, since the turn hooks don't await it.
async function ensurePump($: EngineInterface) {
  if (pumping) return
  try {
    socket = (await $.env.get('LOOM_MOD_SOCKET')) ?? ''
    launch = (await $.env.get('LOOM_LAUNCH')) ?? ''
  } catch (err) {
    $.ui.log(`loom: read the environment: ${String(err)}`, { to: 'debug' })
    return
  }
  if (socket !== '' && launch !== '' && !pumping) {
    pumping = true
    void pump($)
  }
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const started = await next(e)
    await ensurePump($)
    return started
  })

  // The turn hooks clear mainTurn before anything awaits: a done.then queued
  // before the hook ran would otherwise run in that gap and see the old turn.
  on('turn.start', async ($, e, next) => {
    if (e.agentId !== undefined) {
      void ensurePump($)
      return next(e)
    }
    // A new main-loop turn means the last one is over, even when its
    // turn.complete is still in next or never came. Cleared before next: this
    // turn's messages resolve while this hook is still in next, and must not
    // claim the old turn.
    mainTurn = undefined
    void ensurePump($)
    const r = await next(e)
    mainTurn = r.turnId
    // The posts are not awaited: a hook's budget runs while it waits on posts
    // made with another hook's $, and the bookkeeping is already done.
    if (entered.length > 0) void claim($, r.turnId, entered.splice(0))
    return r
  })

  on('turn.complete', async ($, e, next) => {
    if (e.agentId !== undefined) {
      void ensurePump($)
      return next(e)
    }
    // Cleared before next, for the same reason: the next turn's messages can
    // resolve while this hook is still in next.
    if (e.turnId === mainTurn) mainTurn = undefined
    void ensurePump($)
    const r = await next(e)
    const ids = turns.get(e.turnId)
    if (ids !== undefined) {
      turns.delete(e.turnId)
      void reportInOrder(
        $,
        ids.map(id => ({ kind: 'complete' as const, id, turnId: e.turnId, text: r.text, reason: e.reason })),
      )
    }
    return r
  })
}
