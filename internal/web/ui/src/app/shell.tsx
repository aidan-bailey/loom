import { Outlet } from "@tanstack/react-router";
import {
  Component,
  lazy,
  Suspense,
  useEffect,
  useState,
  type ErrorInfo,
  type MouseEvent,
  type ReactElement,
  type ReactNode,
} from "react";

import type { Session } from "@/api/gen";
import type { LinkState } from "@/api/stream";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Toaster } from "@/components/ui/toast";
import { cn } from "@/lib/utils";
import { useFleet, useHostFacts, useLink, useSession } from "@/store/fleet";

import { CommandButton } from "./CommandButton";
import { ConfirmSheet } from "./ConfirmSheet";
import { panelFor, sessionTabs, type Panel, type PanelId, type PanelProps } from "./panels";
import { hrefFor, panelProps, routeOf, useRoute, type RouteState } from "./routes";
import { GroupCounts, HostLabel, LinkPill } from "./status";
import { useLayout } from "./viewport";

// resetKey changes with the route, so a boundary that caught an error starts
// over once the route is another one: another session, or another board or
// document (`?plan=`, `?at=`) of the same panel.
export function resetKey(panel: PanelId, props: PanelProps): string {
  return `${panel}/${JSON.stringify(props.params)}/${JSON.stringify(props.search)}`;
}

interface BoundaryProps {
  what: string;
  // fallback replaces the failed subtree; without it the boundary shows what
  // failed and a reload.
  fallback?: ReactNode;
  // resetOn, once it changes, clears a caught error and draws the children again: a boundary
  // that stays mounted across routes starts over on another route.
  resetOn?: string;
  children: ReactNode;
}

interface BoundaryState {
  error: Error | null;
}

// Boundary keeps one broken view from blanking the app (spec §29): the
// header, the footer and every other panel keep working.
export class Boundary extends Component<BoundaryProps, BoundaryState> {
  state: BoundaryState = { error: null };

  static getDerivedStateFromError(error: unknown): BoundaryState {
    return { error: error instanceof Error ? error : new Error(String(error)) };
  }

  componentDidUpdate(prev: BoundaryProps): void {
    if (this.state.error !== null && prev.resetOn !== this.props.resetOn) {
      this.setState({ error: null });
    }
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

// BodyLoading is the fallback of a lazy body: two skeletons under a named status.
function BodyLoading(): ReactElement {
  return (
    <div role="status" aria-label="Loading" className="flex flex-col gap-3 px-4 py-6">
      <Skeleton className="h-6 w-1/2" />
      <Skeleton className="h-32 w-full" />
    </div>
  );
}

// PanelHost draws the panel registered under `panel` inside an error boundary,
// or nothing when none is (every PanelId has one, which app/panels.test.ts
// pins). A body that is `React.lazy` suspends under a skeleton, and its chunk
// failing to load reaches the boundary, which offers a reload: the Suspense
// stays inside the Boundary, so a failed load is the boundary's and a pending
// one never is.
export function PanelHost({
  panel,
  props,
}: {
  panel: PanelId;
  props: PanelProps;
}): ReactElement | null {
  const entry = panelFor(panel);
  if (entry === undefined) return null;
  return (
    <Boundary key={resetKey(panel, props)} what={entry.title(props)}>
      <Suspense fallback={<BodyLoading />}>
        <entry.Body {...props} />
      </Suspense>
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

// PanelHead is a route's head rows: the panel's Head, then, for a session on a tabbed route, the
// session's tabs and the panel's Strip, each in a boundary of its own so a head that throws
// leaves the tabs, the strip and the screen. The phone pins it in its header and the desktop's
// middle pins it above its scroller.
export function PanelHead({
  route,
  session,
}: {
  route: RouteState;
  session: Session | undefined;
}): ReactElement {
  const props = panelProps(route);
  const entry = panelFor(route.panel);
  const title = entry === undefined ? route.panel : entry.title(props);
  const Head = entry?.Head;
  const Strip = entry?.Strip;
  return (
    <>
      {Head === undefined ? null : (
        <Boundary key={resetKey(route.panel, props)} what={`The ${title} header`} fallback={null}>
          <Head {...props} />
        </Boundary>
      )}
      {session !== undefined && TABBED.has(route.panel) ? (
        <>
          <SessionTabs session={session} />
          {Strip === undefined ? null : (
            <Boundary
              key={resetKey(route.panel, props)}
              what={`The ${title} strip`}
              fallback={null}
            >
              <Strip {...props} />
            </Boundary>
          )}
        </>
      ) : null}
    </>
  );
}

// PhoneShell is the phone's frame, one centred column: the pinned header (the panel's Head), main
// (the router's outlet, or the splash while the store holds no snapshot,
// except on pairing) and the pinned footer. Pairing and the full mockup
// have no header or footer, and neither has a screen with no snapshot. The
// splash draws the way back to the classic app on every route, so it works
// with the daemon down and nothing saved.
export function PhoneShell(): ReactElement {
  const route = useRoute();
  const props = panelProps(route);
  const hasSnapshot = useFleet((s) => s.snapshot !== null);
  const { state } = useLink();
  const session = useSession(props.sessionId);
  const typing = useTextFieldFocus(route);
  const chromed = hasSnapshot && route.panel !== "pair" && route.panel !== "mockupFull";
  const waiting = !hasSnapshot && route.panel !== "pair";

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
          <PanelHead route={route} session={session} />
        </header>
      ) : null}
      <main className="flex flex-1 flex-col">
        {waiting ? (
          <>
            <div role="status" className="px-4 py-12 text-center text-muted-foreground">
              {SPLASH[state]}
            </div>
            <div className="flex justify-center px-4">
              <CommandButton id="app.backToClassic" variant="secondary" />
            </div>
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
    </div>
  );
}

// DesktopShell is the desktop's frame, a module of its own (spec §4): the phone's first load does not
// carry it.
const DesktopShell = lazy(() => import("@/desktop/DesktopShell"));

// AppShell chooses the frame by the window's width, live as it resizes: the lazy DesktopShell at
// 1024px and wider, once there is a snapshot and off the pairing screen, else PhoneShell, which is
// also the pairing screen's and the splash's frame at every width (a centred column). The one
// confirmation sheet and the one toaster are mounted here, above both frames, told the layout, so a
// swap neither drops an open confirmation nor a toast (spec §11). A desktop that fails to draw
// reaches the boundary, which offers a reload and starts over on another route; a chunk that failed
// to load stays failed until the reload, since the lazy import keeps its rejection.
export function AppShell(): ReactElement {
  const layout = useLayout();
  const route = useRoute();
  const hasSnapshot = useFleet((s) => s.snapshot !== null);
  const desktop = layout !== "phone" && hasSnapshot && route.panel !== "pair";
  return (
    <>
      {desktop ? (
        <Boundary what="The desktop layout" resetOn={resetKey(route.panel, panelProps(route))}>
          <Suspense fallback={<BodyLoading />}>
            <DesktopShell />
          </Suspense>
        </Boundary>
      ) : (
        <PhoneShell />
      )}
      <ConfirmSheet variant={layout === "phone" ? "drawer" : "dialog"} />
      <Toaster placement={layout === "phone" ? "top" : "bottom-right"} />
    </>
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
export const TABBED: ReadonlySet<PanelId> = new Set<PanelId>([
  "turns",
  "info",
  "changes",
  "mockup",
  "flow",
  "flowStory",
  "flowDoc",
  "agent",
  "job",
]);

// ContextRow is the header's first row: an optional way back (an href, named
// by where it goes: "fleet", or the label of the tab it opens), the screen's
// title as its one h1, and the screen's actions.
export function ContextRow({
  title,
  back,
  onBack,
  actions,
}: {
  title: string;
  back?: string;
  onBack?: (e: MouseEvent<HTMLAnchorElement>) => void;
  actions?: ReactNode;
}): ReactElement {
  return (
    <div className="flex min-h-[44px] items-center gap-2 px-4">
      {back === undefined ? null : (
        <a
          href={back}
          onClick={onBack}
          className="flex min-h-[44px] flex-none items-center text-sm text-muted-foreground"
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
// tab, the full mockup the mockup tab, a subagent the conversation and a job
// the info tab; the conversation of a session that is not Claude's is its info.
export function tabOf(panel: PanelId, session: Session): PanelId {
  if (panel === "flowStory" || panel === "flowDoc") return "flow";
  if (panel === "mockupFull") return "mockup";
  const lit = panel === "agent" ? "turns" : panel === "job" ? "info" : panel;
  return lit === "turns" && !session.claude ? "info" : lit;
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
  const current = tabOf(route.panel, session);
  const host = useHostFacts();
  const tabs = sessionTabs(session, host);
  const here = panelFor(current);
  if (here?.tab !== undefined && !tabs.some((t) => t.id === current)) {
    tabs.push(here);
    tabs.sort(byTabOrder);
  }
  if (tabs.length < 2) return <></>;
  return (
    <nav aria-label="Session views" className="flex gap-1 overflow-x-auto px-2">
      {tabs.map((tab) => {
        const Mark = tab.tab?.Mark;
        return (
          <a
            key={tab.id}
            href={hrefFor(tab.id, { id: String(session.id) })}
            aria-current={tab.id === current ? "page" : undefined}
            className={cn(
              "flex min-h-[44px] items-center border-b-2 border-transparent px-3 text-sm text-muted-foreground",
              tab.id === current && "border-foreground text-foreground",
            )}
          >
            {tab.tab?.label ?? tab.id}
            {Mark === undefined ? null : (
              <Boundary what={`The ${tab.id} tab's mark`} fallback={null}>
                <Mark session={session} />
              </Boundary>
            )}
          </a>
        );
      })}
    </nav>
  );
}

// StatusRow is the footer's row: the link's pill, loom@host and a count per group (app/status.tsx).
export function StatusRow(): ReactElement {
  return (
    <div className="flex min-h-[44px] items-center gap-1 px-4 text-xs">
      <LinkPill />
      <HostLabel />
      <GroupCounts />
    </div>
  );
}
