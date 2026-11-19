import { ArrowLeft, type LucideIcon } from "lucide-react";
import { useMemo, useSyncExternalStore } from "react";

import type { Session } from "@/api/gen";
import type { LinkState } from "@/api/stream";
import { allSessions } from "@/fleet/groups";
import { useFleet, useLink, useSession } from "@/store/fleet";

import { askConfirm, type ConfirmSpec } from "./ConfirmSheet";
import type { PanelId } from "./panels";
import { panelProps, useRoute } from "./routes";
import { switchApp } from "./switchApp";
import { showMessage } from "./toasts";

export type { ConfirmSpec };

// CommandContext is what a command's `when`, `title` and `confirm` read: the
// route's session (undefined on a route without one), the link, whether the
// snapshot is stale, and the panel on screen. `when` reads only these, never
// a copy of the model's preconditions: the model decides, and a wrong guess
// is its 409 (spec §23).
export interface CommandContext {
  session: Session | undefined;
  link: LinkState;
  stale: boolean;
  panel: PanelId;
}

// Command is one action. `offline` marks a command that runs while the
// snapshot is stale (a cold start, the daemon down, unpaired): only
// app.backToClassic, since the switch works offline (spec §4).
export interface Command<A = void> {
  id: string;
  title: string | ((ctx: CommandContext) => string);
  icon?: LucideIcon;
  offline?: boolean;
  when: (ctx: CommandContext) => boolean;
  confirm?: (ctx: CommandContext, args: A) => ConfirmSpec;
  run: (ctx: CommandContext, args: A) => Promise<void>;
}

const registry = new Map<string, Command<unknown>>();

// defineCommand registers a command (a second definition of an id replaces
// the first, as a hot reload needs) and returns it.
export function defineCommand<A = void>(command: Command<A>): Command<A> {
  registry.set(command.id, command as unknown as Command<unknown>);
  return command;
}

// commandById is the command registered under id, or undefined.
export function commandById(id: string): Command<unknown> | undefined {
  return registry.get(id);
}

// useCommandContext is the context of the route on screen.
export function useCommandContext(): CommandContext {
  const route = useRoute();
  const { state, stale } = useLink();
  const session = useSession(panelProps(route).sessionId);
  return useMemo(
    () => ({ session, link: state, stale, panel: route.panel }),
    [session, state, stale, route.panel],
  );
}

// commandEnabled reports whether the command may run in ctx: every command is
// off while the snapshot is stale, except an offline one, and off when its own
// `when` says so.
export function commandEnabled<A>(command: Command<A>, ctx: CommandContext): boolean {
  return (command.offline === true || !ctx.stale) && command.when(ctx);
}

// inflight is the set of commands being run, each keyed by its id and the
// session it acts on: a second call while one is in flight does nothing, so a
// command drawn twice, sibling buttons of one command or a re-tap after the
// request was accepted send one request. CommandButton reads it through
// useCommandBusy.
const inflight = new Set<string>();
const busyListeners = new Set<() => void>();

function emitBusy(): void {
  for (const listener of busyListeners) listener();
}

function subscribeBusy(listener: () => void): () => void {
  busyListeners.add(listener);
  return () => {
    busyListeners.delete(listener);
  };
}

function busyKey(command: { id: string }, ctx: CommandContext): string {
  return `${command.id}\u0000${ctx.session === undefined ? "" : ctx.session.id}`;
}

// useCommandBusy reports whether the command is being run for ctx's session.
export function useCommandBusy<A>(command: Command<A>, ctx: CommandContext): boolean {
  const key = busyKey(command, ctx);
  return useSyncExternalStore(subscribeBusy, () => inflight.has(key));
}

// freshContext is ctx with the store's link, staleness and copy of ctx's
// session as they are now: what a confirmation sheet's yes is checked against,
// since the stream may have dropped, or another device paused or killed the
// session, while the sheet was open.
function freshContext(ctx: CommandContext): CommandContext {
  const { snapshot, stale, link } = useFleet.getState();
  const id = ctx.session?.id;
  const session =
    id === undefined || snapshot === null
      ? undefined
      : allSessions(snapshot).find((s) => s.id === id);
  return { ...ctx, session, stale, link };
}

// runCommand runs a command in ctx with args: nothing when it is not enabled
// or when the same command is already running for the same session; else,
// when it has a `confirm`, the confirmation sheet first (a declined sheet runs
// nothing), then the enablement again against the store's current state (a
// yes given after the view went stale, or the session changed, toasts that
// nothing was done), then its `run` with that fresh context. A `run` that
// throws is an error toast, never an unhandled rejection.
export async function runCommand<A>(
  command: Command<A>,
  ctx: CommandContext,
  args: A,
): Promise<void> {
  if (!commandEnabled(command, ctx)) return;
  const key = busyKey(command, ctx);
  if (inflight.has(key)) return;
  inflight.add(key);
  emitBusy();
  try {
    let at = ctx;
    if (command.confirm !== undefined) {
      if (!(await askConfirm(command.confirm(ctx, args)))) return;
      at = freshContext(ctx);
      if (!commandEnabled(command, at)) {
        showMessage("loom changed while you were deciding, so nothing was done.", true);
        return;
      }
    }
    await command.run(at, args);
  } catch (err) {
    showMessage(err instanceof Error ? err.message : String(err), true);
  } finally {
    inflight.delete(key);
    emitBusy();
  }
}

// app.backToClassic is the switch back to the classic app, drawn by every
// placeholder. It is offline: the cookie is set and the worker's update may
// fail, in which case the switch comes with the next launch.
defineCommand({
  id: "app.backToClassic",
  title: "Back to classic",
  icon: ArrowLeft,
  offline: true,
  when: () => true,
  run: async () => {
    if ((await switchApp("classic")) === "deferred") {
      showMessage("You're offline. loom switches to the classic app the next time it opens.");
    }
  },
});
