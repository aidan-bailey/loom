// TabStrip is a desktop pane's row of a session's tabs (chrome, dense: no 44px minimum), drawn by
// the inspector and the flow board's preview: one button per panel, the shown one marked
// `aria-current`.
import type { ReactElement } from "react";

import type { Panel, PanelId } from "@/app/panels";

export function TabStrip(props: {
  slot: string;
  label: string;
  panels: Panel[];
  current: PanelId | undefined;
  onPick: (id: PanelId) => void;
}): ReactElement {
  return (
    <nav
      data-slot={props.slot}
      aria-label={props.label}
      className="flex flex-none overflow-x-auto border-b border-border"
    >
      {props.panels.map((p) => (
        <button
          key={p.id}
          type="button"
          aria-current={p.id === props.current ? "true" : undefined}
          onClick={() => props.onPick(p.id)}
          className="h-8 flex-none px-3 text-xs text-muted-foreground aria-[current=true]:text-foreground"
        >
          {p.tab?.label}
        </button>
      ))}
    </nav>
  );
}
