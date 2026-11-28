// The desktop's inspector (spec §10): chrome, not a panel. A column at the right of the middle
// holding another view of the session on screen: a strip of the session's tabs but the middle's,
// above a panel host in a scroller of its own.
import { useLayoutEffect, useMemo, useState, type ReactElement } from "react";

import type { Session } from "@/api/gen";
import { useDesktop } from "@/app/desktopState";
import { sessionTabs, tabOrder, type PanelId, type PanelProps } from "@/app/panels";
import { elementPane, ScrollPaneProvider } from "@/app/scroll";
import { Boundary, PanelHost, resetKey } from "@/app/shell";
import { markInspectorPress, takeInspectorPress } from "@/panels/terminal/Terminal";
import { useHostFacts } from "@/store/fleet";

import { inspectorTab } from "./inspector";
import { TabStrip } from "./TabStrip";

// Inspector draws the session's tab that inspectorTab picks, or nothing when only the middle's
// tab applies. The strip sits above the scroller, outside the boundary, so a panel that throws
// leaves it and a click on another tab starts over.
export function Inspector(props: { session: Session; middle: PanelId }): ReactElement | null {
  const { session, middle } = props;
  const chosen = useDesktop((s) => s.slots[0]?.tab);
  const setTab = useDesktop((s) => s.setTab);
  const host = useHostFacts();
  const panels = sessionTabs(session, host);
  const tabs = panels.map((p) => p.id);
  const tab = inspectorTab(tabOrder(), tabs, middle, chosen);
  const [scroller, setScroller] = useState<HTMLElement | null>(null);
  const pane = useMemo(() => (scroller === null ? null : elementPane(scroller, null)), [scroller]);
  const panelProps = useMemo<PanelProps>(
    () => ({ sessionId: session.id, params: { id: String(session.id) }, search: {} }),
    [session.id],
  );
  // A press of the terminal's tab marks the terminal that the same commit mounts (spec §57). Once
  // the commit is done the mark is spent: layout effects run children first, so a terminal
  // mounted by the press has taken it already, and a press whose terminal did not mount (the
  // boundary's fallback, a session that does not run) leaves nothing for a terminal the
  // inspector mounts later from a stored choice.
  useLayoutEffect(() => {
    takeInspectorPress();
  });

  if (tab === undefined) return null;

  return (
    <aside
      data-slot="inspector"
      aria-label="Inspector"
      className="flex w-[330px] max-w-[33vw] flex-none flex-col border-l border-border bg-background"
    >
      <TabStrip
        slot="inspector-tabs"
        label="Inspector views"
        panels={panels.filter((p) => p.id !== middle)}
        current={tab}
        onPick={(id) => {
          // The terminal the press mounts takes the focus (spec §57); one mounted from a stored
          // choice takes none, and pressing the drawn tab mounts nothing.
          if (id === "terminal" && id !== tab) markInspectorPress();
          setTab(id);
        }}
      />
      <Boundary key={resetKey(tab, panelProps)} what="The inspector">
        <div
          data-slot="inspector-scroller"
          ref={setScroller}
          className="min-h-0 flex-1 overflow-y-auto"
        >
          {pane === null ? null : (
            <ScrollPaneProvider pane={pane}>
              <PanelHost panel={tab} props={panelProps} />
            </ScrollPaneProvider>
          )}
        </div>
      </Boundary>
    </aside>
  );
}
