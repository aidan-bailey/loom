// The flow board's preview (desktop chrome, not a panel): a pane at the right of the middle that
// shows the session of the card the board's search names, as a GitHub Projects board previews an
// item. Its header names the session; below it are the session's tabs, opening on the card's flow,
// and the chosen tab's Body in a scroller of its own, so the pane sends no read of its own.
import { useMemo, useState, type ReactElement } from "react";

import { sessionTabs, type PanelId, type PanelProps } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { elementPane, ScrollPaneProvider } from "@/app/scroll";
import { Boundary, PanelHost, resetKey } from "@/app/shell";
import { SubLine } from "@/session/SubLine";
import { useHostFacts, useSession } from "@/store/fleet";

import { TabStrip } from "./TabStrip";

const ICON =
  "flex size-7 flex-none items-center justify-center rounded-md text-muted-foreground hover:bg-secondary hover:text-foreground";

// shownTab is the tab the pane draws: the one chosen while the session has it, else the flow,
// else the session's first tab. A session that is gone has no tabs and draws the flow, whose Body
// says the session is gone.
export function shownTab(tabs: PanelId[], chosen: PanelId): PanelId {
  if (tabs.includes(chosen)) return chosen;
  if (tabs.includes("flow") || tabs.length === 0) return "flow";
  return tabs[0] ?? "flow";
}

// FlowPreview opens on the card's flow; DesktopShell keys it by the card, so another card starts
// on its flow again.
export function FlowPreview(props: { sessionId: number; plan: string }): ReactElement {
  const { sessionId, plan } = props;
  const session = useSession(sessionId);
  const host = useHostFacts();
  const panels = session === undefined ? [] : sessionTabs(session, host);
  const [chosen, setChosen] = useState<PanelId>("flow");
  const tab = shownTab(
    panels.map((p) => p.id),
    chosen,
  );
  const [scroller, setScroller] = useState<HTMLElement | null>(null);
  const pane = useMemo(() => (scroller === null ? null : elementPane(scroller, null)), [scroller]);
  const panelProps = useMemo<PanelProps>(
    () => ({ sessionId, params: { id: String(sessionId) }, search: { plan } }),
    [sessionId, plan],
  );
  const fullHref = hrefFor(tab, { id: String(sessionId) }, tab === "flow" ? { plan } : {});

  return (
    <aside
      data-slot="flow-preview"
      aria-label="Flow preview"
      className="flex w-[480px] max-w-[45vw] flex-none flex-col border-l border-border bg-background"
    >
      <header data-slot="flow-preview-head" className="flex-none border-b border-border">
        <div className="flex items-center gap-1 px-3 pt-2">
          <h2 className="min-w-0 flex-1 truncate text-sm font-semibold">
            {session?.title ?? `Session ${sessionId}`}
          </h2>
          <a href={fullHref} aria-label="Open full page" title="Open full page" className={ICON}>
            ⤢
          </a>
          <a
            href={hrefFor("flowBoard")}
            aria-label="Close the preview"
            title="Close the preview"
            className={ICON}
          >
            ✕
          </a>
        </div>
        {session === undefined ? null : <SubLine session={session} />}
      </header>
      {panels.length === 0 ? null : (
        <TabStrip
          slot="flow-preview-tabs"
          label="Preview views"
          panels={panels}
          current={tab}
          onPick={setChosen}
        />
      )}
      <Boundary key={resetKey(tab, panelProps)} what="The preview">
        <div
          data-slot="flow-preview-scroller"
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
