import { Outlet } from "@tanstack/react-router";
import {
  Component,
  useEffect,
  useState,
  type ErrorInfo,
  type ReactElement,
  type ReactNode,
} from "react";

import type { Session, SessionGroup } from "@/api/gen";
import type { LinkState } from "@/api/stream";
import { Button } from "@/components/ui/button";
import { Toaster } from "@/components/ui/toast";
import { fleetGroups, nextInGroup } from "@/fleet/groups";
import { cn } from "@/lib/utils";
import { useFleet, useFleetGroups, useLink, useSession } from "@/store/fleet";

import { CommandButton } from "./CommandButton";
import { ConfirmSheet } from "./ConfirmSheet";
import { panelFor, sessionTabs, type Panel, type PanelId, type PanelProps } from "./panels";
import { hrefFor, panelProps, routeOf, useRoute, type RouteState } from "./routes";

// SCREEN_NAMES is what each screen is called by the placeholder, which draws
// where no panel is registered; a registered panel's own title names it in its
// error boundaries.
const SCREEN_NAMES: Record<PanelId, string> = {
  fleet: "Fleet",
  turns: "Conversation",
  info: "Info",
  changes: "Changes",
  mockup: "Mockup",
  mockupFull: "Mockup",
  agent: "Subagent",
  job: "Job",
  flow: "Flow",
  flowStory: "Flow story",
  flowDoc: "Flow document",
  flows: "Flows",
  new: "New session",
  settings: "Settings",
  pair: "Pairing",
};

// resetKey changes with the route, so a boundary that caught an error starts
// over once the route is another one: another session, or another board or
// document (`?plan=`, `?at=`) of the same panel.
function resetKey(panel: PanelId, props: PanelProps): string {
  return `${panel}/${JSON.stringify(props.params)}/${JSON.stringify(props.search)}`;
}

interface BoundaryProps {
  what: string;
  // fallback replaces the failed subtree; without it the boundary shows what
  // failed and a reload.
  fallback?: ReactNode;
  children: ReactNode;
}

interface BoundaryState {
  error: Error | null;
}

// Boundary keeps one broken view from blanking the app (spec §29): the
// header, the footer and every other panel keep working.
class Boundary extends Component<BoundaryProps, BoundaryState> {
  state: BoundaryState = { error: null };

  static getDerivedStateFromError(error: unknown): BoundaryState {
    return { error: error instanceof Error ? error : new Error(String(error)) };
  }

  componentDidCatch(error: unknown, info: ErrorInfo): void {
    console.error(`loom: ${this.props.what} failed to draw`, error, info.componentStack);
  }

  render(): ReactNode {
    const { error } = this.state;
    if (error === null) return this.props.children;
    if (this.props.fallback !== undefined) return this.props.fallback;
    return (
      <div
        role="alert"
        className="m-4 flex flex-col items-start gap-2 rounded-lg border border-error p-4"
      >
        <p className="font-semibold">{this.props.what} failed to draw.</p>
        <p className="font-mono text-xs break-words text-muted-foreground">{error.message}</p>
        <Button
          variant="secondary"
          onClick={() => {
            window.location.reload();
          }}
        >
          Reload
        </Button>
      </div>
    );
  }
}

// Placeholder draws a route no panel is registered for: it names the screen
// and offers the way back to the classic app (spec §7).
export function Placeholder({ panel }: { panel: PanelId }): ReactElement {
  const name = SCREEN_NAMES[panel];
  return (
    <section className="flex flex-col items-start gap-3 px-4 py-8">
      {panel === "fleet" ? null : (
        <a href={hrefFor("fleet")} className="text-sm text-muted-foreground">
          ‹ fleet
        </a>
      )}
      <h1 className="text-lg font-semibold">{name}</h1>
      <p className="text-muted-foreground">
        {name} is not in the new app yet. It is in the classic one.
      </p>
      <CommandButton id="app.backToClassic" variant="secondary" />
    </section>
  );
}

// PanelHost draws the panel registered under `panel` inside an error
// boundary, or the placeholder when none is.
export function PanelHost({ panel, props }: { panel: PanelId; props: PanelProps }): ReactElement {
  const entry = panelFor(panel);
  if (entry === undefined) return <Placeholder panel={panel} />;
  return (
    <Boundary key={resetKey(panel, props)} what={entry.title(props)}>
      <entry.Body {...props} />
    </Boundary>
  );
}

// SPLASH is what the screen says while the store holds no snapshot: the
// link's state, in the classic app's words.
const SPLASH: Record<LinkState, string> = {
  connecting: "Connecting…",
  live: "Connecting…",
  restarting: "loom is restarting…",
  offline: "Can't reach loom. Retrying…",
};

// AppShell is the app's frame: the pinned header (the panel's Head), main
// (the router's outlet, or the splash while the store holds no snapshot,
// except on pairing) and the pinned footer. Pairing and the full mockup
// have no header or footer, and neither has a screen with no snapshot. A route
// no panel is registered for still draws its placeholder under the splash, so
// the way back to the classic app works with the daemon down and nothing
// saved.
export function AppShell(): ReactElement {
  const route = useRoute();
  const props = panelProps(route);
  const hasSnapshot = useFleet((s) => s.snapshot !== null);
  const { state } = useLink();
  const session = useSession(props.sessionId);
  const typing = useTextFieldFocus(route);
  const chromed = hasSnapshot && route.panel !== "pair" && route.panel !== "mockupFull";
  const waiting = !hasSnapshot && route.panel !== "pair";
  const entry = panelFor(route.panel);
  const Head = entry?.Head;

  return (
    <div
      className={cn(
        "mx-auto flex min-h-dvh w-full max-w-[40rem] flex-col bg-background text-foreground",
        !chromed && "pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)]",
      )}
    >
      {chromed ? (
        <header
          data-slot="header"
          className="sticky top-0 z-30 flex-none border-b border-border bg-background pt-[calc(env(safe-area-inset-top)+8px)]"
        >
          {Head === undefined ? null : (
            <Boundary
              key={resetKey(route.panel, props)}
              what={`The ${entry?.title(props) ?? SCREEN_NAMES[route.panel]} header`}
              fallback={null}
            >
              <Head {...props} />
            </Boundary>
          )}
          {session !== undefined && TABBED.has(route.panel) ? (
            <SessionTabs session={session} />
          ) : null}
        </header>
      ) : null}
      <main className="flex flex-1 flex-col">
        {waiting ? (
          <>
            <div role="status" className="px-4 py-12 text-center text-muted-foreground">
              {SPLASH[state]}
            </div>
            {panelFor(route.panel) === undefined ? <Placeholder panel={route.panel} /> : null}
          </>
        ) : (
          <Outlet />
        )}
      </main>
      {chromed && !typing ? (
        <footer
          data-slot="footer"
          className={cn(
            "sticky bottom-0 z-30 flex-none border-t border-border bg-card pb-[env(safe-area-inset-bottom)]",
            state === "offline" && "border-t-error",
          )}
        >
          <StatusRow />
        </footer>
      ) : null}
      <ConfirmSheet />
      <Toaster />
    </div>
  );
}

// NON_TEXT_INPUTS are the input types that bring up no keyboard.
const NON_TEXT_INPUTS = new Set([
  "button",
  "checkbox",
  "color",
  "file",
  "hidden",
  "image",
  "radio",
  "range",
  "reset",
  "submit",
]);

function isTextField(target: EventTarget | null): boolean {
  if (target instanceof HTMLTextAreaElement) return true;
  if (target instanceof HTMLInputElement) return !NON_TEXT_INPUTS.has(target.type);
  return target instanceof HTMLElement && target.isContentEditable;
}

// useTextFieldFocus is true while a text field has focus: the keyboard is up,
// so the footer gives its room to the text (spec §34). A route change can
// remove the focused field without a focusout reaching the document, so the
// focus is read again whenever the route changes.
function useTextFieldFocus(route: RouteState): boolean {
  const [typing, setTyping] = useState(() => isTextField(document.activeElement));
  useEffect(() => {
    // Read the focus in a microtask, not in the effect body: a synchronous
    // setState in an effect is what react-hooks/set-state-in-effect refuses.
    // The passive effect already runs after the commit has removed or kept the
    // field.
    queueMicrotask(() => {
      setTyping(isTextField(document.activeElement));
    });
  }, [route]);
  useEffect(() => {
    const focusIn = (e: FocusEvent): void => {
      setTyping(isTextField(e.target));
    };
    const focusOut = (e: FocusEvent): void => {
      setTyping(isTextField(e.relatedTarget));
    };
    document.addEventListener("focusin", focusIn);
    document.addEventListener("focusout", focusOut);
    return () => {
      document.removeEventListener("focusin", focusIn);
      document.removeEventListener("focusout", focusOut);
    };
  }, []);
  return typing;
}

// TABBED are the routes that show a session's tabs under their head.
const TABBED: ReadonlySet<PanelId> = new Set<PanelId>([
  "turns",
  "info",
  "changes",
  "mockup",
  "flow",
  "flowStory",
  "flowDoc",
]);

// ContextRow is the header's first row: an optional way back (an href, named
// by where it goes: "fleet", or the label of the tab it opens), the screen's
// title as its one h1, and the screen's actions.
export function ContextRow({
  title,
  back,
  actions,
}: {
  title: string;
  back?: string;
  actions?: ReactNode;
}): ReactElement {
  return (
    <div className="flex min-h-11 items-center gap-2 px-4">
      {back === undefined ? null : (
        <a
          href={back}
          className="flex min-h-11 flex-none items-center text-sm text-muted-foreground"
        >
          ‹ {backLabel(back)}
        </a>
      )}
      <h1 className="min-w-0 flex-1 truncate text-base font-semibold">{title}</h1>
      {actions}
    </div>
  );
}

function backLabel(href: string): string {
  const { panel } = routeOf(href);
  return panel === "fleet" ? "fleet" : (panelFor(panel)?.tab?.label ?? "back");
}

// tabOf is the tab a route lights: a flow's row and document light the flow
// tab, the full mockup the mockup tab.
function tabOf(panel: PanelId): PanelId {
  if (panel === "flowStory" || panel === "flowDoc") return "flow";
  if (panel === "mockupFull") return "mockup";
  return panel;
}

function byTabOrder(a: Panel, b: Panel): number {
  return (a.tab?.order ?? 0) - (b.tab?.order ?? 0);
}

// SessionTabs are a session's views under its context row: the registry's tab
// panels that apply to it, plus the one on screen when it is not among them (a
// session that pauses on its changes keeps the tab under the user). Fewer
// than two draw no row.
export function SessionTabs({ session }: { session: Session }): ReactElement {
  const route = useRoute();
  const current = tabOf(route.panel);
  const tabs = sessionTabs(session);
  const here = panelFor(current);
  if (here?.tab !== undefined && !tabs.some((t) => t.id === current)) {
    tabs.push(here);
    tabs.sort(byTabOrder);
  }
  if (tabs.length < 2) return <></>;
  return (
    <nav aria-label="Session views" className="flex gap-1 overflow-x-auto px-2">
      {tabs.map((tab) => (
        <a
          key={tab.id}
          href={hrefFor(tab.id, { id: String(session.id) })}
          aria-current={tab.id === current ? "page" : undefined}
          className={cn(
            "flex min-h-11 items-center border-b-2 border-transparent px-3 text-sm text-muted-foreground",
            tab.id === current && "border-foreground text-foreground",
          )}
        >
          {tab.tab?.label ?? tab.id}
        </a>
      ))}
    </nav>
  );
}

const PILL: Record<LinkState, string> = {
  live: "LIVE",
  connecting: "CONNECTING",
  restarting: "RESTARTING",
  offline: "OFFLINE",
};

const PILL_TONE: Record<LinkState, string> = {
  live: "bg-working",
  connecting: "bg-muted-foreground",
  restarting: "bg-attention",
  offline: "bg-error",
};

// COUNTED are the groups the status row counts, in its order.
const COUNTED: { key: SessionGroup; glyph: string; words: string; tone: string }[] = [
  { key: "needs_you", glyph: "●", words: "need you", tone: "text-attention" },
  { key: "your_turn", glyph: "◆", words: "your turn", tone: "text-turn" },
  { key: "working", glyph: "◐", words: "working", tone: "text-working" },
];

// clock is the time of day of a ms timestamp.
function clock(ms: number): string {
  return new Date(ms).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

// StatusRow is the footer's row: the link's pill, loom@host (with the time of
// the last update while offline) and a count per group over the shown
// workspaces. A count opens the group's next session: the one after the
// session on screen, in the group's order at the tap, wrapping, or the first.
// While the stream is down the counts are the cached snapshot's, drawn faint
// and still buttons; a zero is no button.
export function StatusRow(): ReactElement {
  const { state } = useLink();
  const host = useFleet((s) => s.snapshot?.host ?? "");
  const updated = useFleet((s) => s.updated);
  const groups = useFleetGroups();
  const route = useRoute();
  const when = state === "offline" && updated > 0 ? ` · ${clock(updated)}` : "";

  const open = (group: SessionGroup): void => {
    const snapshot = useFleet.getState().snapshot;
    if (snapshot === null) return;
    const current = panelProps(route).sessionId ?? null;
    const next = nextInGroup(fleetGroups(snapshot), group, current);
    if (next !== undefined) window.location.assign(hrefFor("turns", { id: String(next.id) }));
  };

  return (
    <div className="flex min-h-11 items-center gap-1 px-4 text-xs">
      <span
        aria-live="polite"
        className={cn(
          "mr-1 flex-none rounded-sm px-1.5 font-bold text-background",
          PILL_TONE[state],
        )}
      >
        {PILL[state]}
      </span>
      <span className="min-w-0 flex-1 truncate font-mono">
        {host === "" ? (
          "loom"
        ) : (
          <>
            <span className="text-muted-foreground">loom@</span>
            {host}
          </>
        )}
        {when}
      </span>
      {COUNTED.map((g) => {
        const n = groups[g.key].length;
        const tone = cn(
          "flex min-h-11 flex-none items-center gap-1 px-2 tabular-nums",
          g.tone,
          state !== "live" && "opacity-60",
        );
        const inner = (
          <>
            <span aria-hidden="true">{g.glyph}</span>
            {n}
          </>
        );
        if (n === 0) {
          return (
            <span key={g.key} className={cn(tone, "opacity-40")}>
              {inner}
            </span>
          );
        }
        return (
          <button
            key={g.key}
            type="button"
            aria-label={`${n} ${g.words}: open the next`}
            className={tone}
            onClick={() => {
              open(g.key);
            }}
          >
            {inner}
          </button>
        );
      })}
    </div>
  );
}
