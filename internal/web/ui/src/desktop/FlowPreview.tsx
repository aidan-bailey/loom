// The flow board's preview (desktop chrome, not a panel): a pane at the right of the middle that
// shows the flow of the card the board's search names, as a GitHub Projects board previews an
// item. Its header names the session and links to the flow and the conversation; below it, in a
// scroller of its own, is the flow panel's Body, so the pane sends no read of its own.
import { useMemo, useState, type ReactElement } from "react";

import type { PanelProps } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { elementPane, ScrollPaneProvider } from "@/app/scroll";
import { Boundary, PanelHost, resetKey } from "@/app/shell";
import { sessionHref } from "@/fleet/links";
import { SubLine } from "@/session/SubLine";
import { useSession } from "@/store/fleet";

const ICON =
  "flex size-7 flex-none items-center justify-center rounded-md text-muted-foreground hover:bg-secondary hover:text-foreground";
const LINK = "text-muted-foreground underline-offset-2 hover:text-foreground hover:underline";

export function FlowPreview(props: { sessionId: number; plan: string }): ReactElement {
  const { sessionId, plan } = props;
  const session = useSession(sessionId);
  const [scroller, setScroller] = useState<HTMLElement | null>(null);
  const pane = useMemo(() => (scroller === null ? null : elementPane(scroller, null)), [scroller]);
  const panelProps = useMemo<PanelProps>(
    () => ({ sessionId, params: { id: String(sessionId) }, search: { plan } }),
    [sessionId, plan],
  );
  const flowHref = hrefFor("flow", { id: String(sessionId) }, { plan });

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
          <a href={flowHref} aria-label="Open the flow" title="Open the flow" className={ICON}>
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
        {session === undefined ? null : (
          <>
            <SubLine session={session} />
            <nav aria-label="Open" className="flex gap-3 px-4 pb-2 text-xs">
              <a href={sessionHref(session)} className={LINK}>
                conversation
              </a>
              <a href={flowHref} className={LINK}>
                flow
              </a>
            </nav>
          </>
        )}
      </header>
      <Boundary key={resetKey("flow", panelProps)} what="The preview">
        <div
          data-slot="flow-preview-scroller"
          ref={setScroller}
          className="min-h-0 flex-1 overflow-y-auto"
        >
          {pane === null ? null : (
            <ScrollPaneProvider pane={pane}>
              <PanelHost panel="flow" props={panelProps} />
            </ScrollPaneProvider>
          )}
        </div>
      </Boundary>
    </aside>
  );
}
