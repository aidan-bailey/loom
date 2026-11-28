// The desktop's top bar, spec §7: one dense row holding the link's pill, loom@host, the palette's
// box, the workspace filter, the three group counts and the fleet's links. It is chrome, not a
// panel, and reads nothing itself: each part reads the store, the link and the route as it does in
// the phone's footer and the fleet. The box only opens the palette (desktop/Palette.tsx).
import type { ReactElement } from "react";

import { GroupCounts, HostLabel, LinkPill } from "@/app/status";
import { WorkspaceFilter } from "@/fleet/WorkspaceFilter";
import { FleetLinks } from "@/panels/fleet/FleetHead";

import { paletteKeyLabel, usePalette } from "./paletteState";

// PaletteBox is the button that opens the palette, dense (no 44px minimum) and a fixed 300px wide
// (in px: `html` is 13px, so 16rem was 208px and clipped the prompt), `flex-none` so the host
// label's `flex-1` does not squeeze it. The chord's chip never shrinks or wraps.
export function PaletteBox(): ReactElement {
  return (
    <button
      type="button"
      data-slot="palette-box"
      onClick={() => usePalette.getState().setOpen(true)}
      className="flex w-[300px] flex-none items-center justify-between gap-2 rounded-md border border-border px-2 py-0.5 text-left text-muted-foreground hover:text-foreground"
    >
      <span className="block min-w-0 truncate">Search sessions, commands, places</span>
      <span className="flex-none rounded border border-border px-1 font-mono text-[10px] whitespace-nowrap">
        {paletteKeyLabel(navigator.platform)}
      </span>
    </button>
  );
}

export function TopBar(): ReactElement {
  return (
    <div
      data-slot="top-bar"
      className="flex h-9 flex-none items-center gap-2 border-b border-border bg-background px-3 text-xs"
    >
      <LinkPill dense />
      <HostLabel dense />
      <PaletteBox />
      <WorkspaceFilter dense />
      <GroupCounts dense />
      <FleetLinks dense />
    </div>
  );
}
