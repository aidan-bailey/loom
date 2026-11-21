// The desktop's top bar, spec §7: one dense row holding the link's pill, loom@host, the workspace
// filter, the three group counts and the fleet's links. It is chrome, not a panel, and reads
// nothing itself: each part reads the store, the link and the route as it does in the phone's
// footer and the fleet. The palette's box comes with the palette, between the host and the filter.
import type { ReactElement } from "react";

import { GroupCounts, HostLabel, LinkPill } from "@/app/status";
import { WorkspaceFilter } from "@/fleet/WorkspaceFilter";
import { FleetLinks } from "@/panels/fleet/FleetHead";

export function TopBar(): ReactElement {
  return (
    <div
      data-slot="top-bar"
      className="flex h-9 flex-none items-center gap-2 border-b border-border bg-background px-3 text-xs"
    >
      <LinkPill dense />
      <HostLabel dense />
      <WorkspaceFilter dense />
      <GroupCounts dense />
      <FleetLinks dense />
    </div>
  );
}
