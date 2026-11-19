// What the command tests share: sessions and jobs built from the canned snapshot, the store
// seeded with them (a command with a sheet checks its session again against the store after a
// yes), the lookup of a registered command, the toast spy, the sheet a command asked for, and a
// promise the test settles itself. Each test file still mocks `@/app/ConfirmSheet` itself: a
// `vi.mock` is hoisted to the top of the file that holds it.
import { vi } from "vitest";

import type { Job, Notices, Outcome, Session } from "@/api/gen";
import { commandById, type Command, type CommandContext } from "@/app/commands";
import { askConfirm, type ConfirmSpec } from "@/app/ConfirmSheet";
import { toastManager } from "@/components/ui/toast";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";

// makeSession is a Ready session of workspace 1 with no asks and no jobs, over the canned
// snapshot's first session.
export function makeSession(over: Partial<Session> = {}): Session {
  const base = allSessions(fixtureSnapshot())[0];
  if (base === undefined) throw new Error("the canned snapshot holds no session");
  return {
    ...base,
    id: 1,
    workspace: 1,
    status: "Ready",
    group: "your_turn",
    asks: [],
    jobs: [],
    ...over,
  };
}

export function makeJob(over: Partial<Job> = {}): Job {
  return {
    id: 214,
    state: "queued",
    description: "cargo test",
    command: "cargo test",
    agent: "",
    submitted: "2026-11-19T10:00:00Z",
    started: "",
    ended: "",
    exit: null,
    failure: "",
    cancel_reason: "",
    waiting: "",
    parked: false,
    paused: false,
    pause_reclaimed: false,
    forced: false,
    control: "",
    control_error: "",
    ...over,
  };
}

// seed makes the store's live snapshot hold exactly these sessions, grouped by workspace in the
// order given.
export function seed(sessions: Session[]): void {
  const byWorkspace: Record<string, Session[]> = {};
  for (const session of sessions) (byWorkspace[String(session.workspace)] ??= []).push(session);
  useFleet.setState({
    snapshot: { ...fixtureSnapshot(), sessions: byWorkspace },
    stale: false,
    link: "live",
    updated: Date.now(),
  });
}

export function resetFleet(): void {
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
}

export function context(
  session: Session | undefined,
  over: Partial<CommandContext> = {},
): CommandContext {
  return { session, link: "live", stale: false, panel: "turns", ...over };
}

// command is the command registered under id; the test fails when there is none.
export function command(id: string): Command<unknown> {
  const found = commandById(id);
  if (found === undefined) throw new Error(`no command ${id} is registered`);
  return found;
}

export function noticesOk(): Outcome<Notices> {
  return { kind: "ok", status: 200, value: { notices: [] }, notices: [] };
}

// spyOnAdd keeps the toast manager from showing anything and records what it was asked to show;
// the test file restores it with `vi.restoreAllMocks` after each test.
export function spyOnAdd() {
  return vi.spyOn(toastManager, "add").mockImplementation(() => "toast");
}

// askedSheet is the spec the command asked the sheet to show, read from the file's mocked
// `askConfirm`; it fails the test when no sheet was asked for.
export function askedSheet(): ConfirmSpec {
  const call = vi.mocked(askConfirm).mock.calls[0];
  if (call === undefined) throw new Error("no sheet was asked for");
  return call[0];
}

export function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
