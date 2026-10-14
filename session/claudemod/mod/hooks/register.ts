// The loom mod: carries the messages loom's daemon has for this Claude into
// its prompt queue. Claude runs each once the session is idle (messages that
// waited through the same turn run together, as one turn), so a message
// never answers an open dialog or joins a running turn. Everything else stays
// in loom: the daemon composes the text and decides what to send, and this
// module only carries it and reports when each message is queued, when its
// turn starts and how the turn ended.
//
// loom writes this folder into each launch's hooks folder and passes it with
// --plugin-dir. LOOM_MOD_SOCKET names the daemon's mod socket and LOOM_LAUNCH
// this launch; without both the module does nothing.

import type { EngineInterface, PromptSubmitResult, Register } from 'claude-code'

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

async function hello($: EngineInterface): Promise<boolean> {
  const version = await $.session.version()
  const session = await $.session.id()
  const r = await call($, 'POST', '/hello', { launch, mod: MOD_VERSION, claude: version.version, session })
  return r.status === 204
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
    if (entered.length > 0) await claim($, r.turnId, entered.splice(0))
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
      await reportInOrder(
        $,
        ids.map(id => ({ kind: 'complete' as const, id, turnId: e.turnId, text: r.text, reason: e.reason })),
      )
    }
    return r
  })
}
