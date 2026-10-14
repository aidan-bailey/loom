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
