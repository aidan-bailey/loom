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
// snapshot is stale (a cold start, the daemon down, unpaired): app.backToClassic,
// since the switch works offline (spec §4), and device.pair, since pairing runs
// before any snapshot exists.
export interface Command<A = void> {
  id: string;
  title: string | ((ctx: CommandContext) => string);
  icon?: LucideIcon;
  offline?: boolean;
  // palette marks a command the palette lists: the palette shows exactly the
  // registered commands that set it, and a command sets it only when it takes
  // no args (a command whose args a view picks, such as an ask's answer, a
  // job's controls or a send, never sets it).
  palette?: boolean;
  // key names what the args pick (a job's id, an ask's id): it joins the
  // command's id and the session's id in the in-flight key, so Pause on one
  // job does not hold Pause on another. Without it the key is the command's
  // id and the session's, and so it is when `key` throws (a keyed command
  // drawn or run without its args): the throw never escapes, and `run` meets
  // the missing args itself.
  key?: (args: A) => string;
  when: (ctx: CommandContext) => boolean;
  // confirm is the sheet's spec; null runs `run` with no sheet and an empty input.
  confirm?: (ctx: CommandContext, args: A) => ConfirmSpec | null;
  // run gets the sheet's field value as `input` ("" with no field or no sheet).
  run: (ctx: CommandContext, args: A, input: string) => Promise<void>;
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

// allCommands is every registered command, as a new array in definition order
// (the Map's insertion order: a redefined id keeps its first place).
export function allCommands(): Command<unknown>[] {
  return [...registry.values()];
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

// inflight is the set of commands being run, each keyed by its id, the
// session it acts on and what its `key` returns for the args: a second call
// while one is in flight does nothing, so a
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

// ownKey is what the command's `key` gives for args: "" without a `key`, and
// "" when it throws, so neither runCommand (before its try) nor a button's
// render can be taken down by a `key` that was handed no args.
function ownKey<A>(command: Command<A>, args: A): string {
  if (command.key === undefined) return "";
  try {
    return command.key(args);
  } catch {
    return "";
  }
}

function busyKey<A>(command: Command<A>, ctx: CommandContext, args: A): string {
  const session = ctx.session === undefined ? "" : ctx.session.id;
  return `${command.id}\u0000${session}\u0000${ownKey(command, args)}`;
}

// useCommandBusy reports whether the command is being run for ctx's session
// and the args' key.
export function useCommandBusy<A>(command: Command<A>, ctx: CommandContext, args: A): boolean {
  const key = busyKey(command, ctx, args);
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
// or when the same command is already running for the same session and key;
// else, when its `confirm` gives a spec, the confirmation sheet first (a
// declined sheet runs nothing; a `confirm` that gives null runs with no sheet
// and an empty input), then the enablement again against the store's current
// state (a yes given after the view went stale, or the session changed, toasts
// that nothing was done), then its `run` with that fresh context and the
// sheet's value. A `run` that throws is an error toast, never an unhandled
// rejection.
export async function runCommand<A>(
  command: Command<A>,
  ctx: CommandContext,
  args: A,
): Promise<void> {
  if (!commandEnabled(command, ctx)) return;
  const key = busyKey(command, ctx, args);
  if (inflight.has(key)) return;
  inflight.add(key);
  emitBusy();
  try {
    let at = ctx;
    let input = "";
    const spec = command.confirm === undefined ? null : command.confirm(ctx, args);
    if (spec !== null) {
      const value = await askConfirm(spec);
      if (value === null) return;
      input = value;
      at = freshContext(ctx);
      if (!commandEnabled(command, at)) {
        showMessage("loom changed while you were deciding, so nothing was done.", true);
        return;
      }
    }
    await command.run(at, args, input);
  } catch (err) {
    showMessage(err instanceof Error ? err.message : String(err), true);
  } finally {
    inflight.delete(key);
    emitBusy();
  }
}

// app.backToClassic is the switch back to the classic app, drawn under the
// splash on every route (so it works with the daemon down and nothing saved)
// and in Settings. It is offline for that reason: the cookie is set and the
// worker's update may fail, in which case the switch comes with the next
// launch.
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
