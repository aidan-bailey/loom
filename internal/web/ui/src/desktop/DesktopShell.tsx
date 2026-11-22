// The desktop's frame (spec §4, §5, §9). It is a module of its own, loaded only at 1024px and
// wider, so the phone's first load does not grow. The row below holds the middle, the route's
// pane: the panel's head rows pinned above a scroller that scrolls on its own.
import { Outlet } from "@tanstack/react-router";
import { useMemo, useState, type ReactElement } from "react";

// Registers the two view toggles with the desktop chunk, not the phone's entry.
import "@/commands/view";
import { useDesktop } from "@/app/desktopState";
import type { PanelId } from "@/app/panels";
import { panelProps, useRoute } from "@/app/routes";
import { elementPane, ScrollPaneProvider } from "@/app/scroll";
import { PanelHead, tabOf } from "@/app/shell";
import { useLayout } from "@/app/viewport";
import { cn } from "@/lib/utils";
import { useSession } from "@/store/fleet";

import { Inspector } from "./Inspector";
import { Palette } from "./Palette";
import { usePaletteKey } from "./paletteState";
import { Sidebar } from "./Sidebar";
import { TopBar } from "./TopBar";

// READABLE are the panels whose text keeps a readable measure in a wide pane: a conversation's
// turns and its reply box are at most 48rem wide, centred.
const READABLE: ReadonlySet<PanelId> = new Set<PanelId>(["turns", "agent"]);

// Middle is the route's pane: exactly the phone's frame for the route, with its head rows pinned
// above the scroller (outside it, so the pane's own head is `null`) and the panel's screen inside
// the scroller under an elementPane. The screen is drawn only once the scroller has mounted, so no
// panel of the middle ever gets the window's pane and reads the window's scroll.
function Middle(): ReactElement {
  const route = useRoute();
  const session = useSession(panelProps(route).sessionId);
  const [scroller, setScroller] = useState<HTMLElement | null>(null);
  const pane = useMemo(() => (scroller === null ? null : elementPane(scroller, null)), [scroller]);

  return (
    <section data-slot="middle" className="flex min-w-0 flex-1 flex-col">
      <header
        data-slot="middle-head"
        className="flex-none border-b border-border bg-background pt-2 empty:hidden"
      >
        {route.panel === "fleet" ? null : <PanelHead route={route} session={session} />}
      </header>
      <div ref={setScroller} data-slot="middle-scroller" className="min-h-0 flex-1 overflow-y-auto">
        {pane === null ? null : (
          <ScrollPaneProvider pane={pane}>
            <main
              className={cn(
                "mx-auto flex min-h-full w-full flex-col",
                READABLE.has(route.panel) && "max-w-[48rem]",
              )}
            >
              <Outlet />
            </main>
          </ScrollPaneProvider>
        )}
      </div>
    </section>
  );
}

// DesktopShell is the frame at `desk2` and `desk3`: the whole window, one row holding the middle.
// The full mockup is the one screen that draws the whole window itself, with no chrome.
export default function DesktopShell(): ReactElement {
  const route = useRoute();
  const sidebar = useDesktop((s) => s.sidebar);
  const inspector = useDesktop((s) => s.inspector);
  const layout = useLayout();
  const session = useSession(panelProps(route).sessionId);
  // The chord's hook runs before the early return, so it works on every route; Palette is drawn on
  // both branches, so the store and the dialog never disagree.
  usePaletteKey();
  if (route.panel === "mockupFull") {
    return (
      <>
        <Outlet />
        <Palette />
      </>
    );
  }
  return (
    <div data-slot="desktop" className="flex h-dvh min-h-0 flex-col bg-background text-foreground">
      <TopBar />
      <div className="flex min-h-0 flex-1">
        {sidebar && route.panel !== "fleet" ? (
          <Sidebar current={panelProps(route).sessionId} />
        ) : null}
        <Middle />
        {layout === "desk3" && inspector && session !== undefined ? (
          <Inspector session={session} middle={tabOf(route.panel, session)} />
        ) : null}
      </div>
      <Palette />
    </div>
  );
}
