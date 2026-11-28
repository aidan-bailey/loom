// The workspace filter: a toggle group of "All" and each shown workspace, over the one choice in
// fleet/filter.ts. The phone draws it atop the fleet and the desktop in its top bar and sidebar.
import type { ReactElement } from "react";

import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useFleet } from "@/store/fleet";

import { activeFilter, useFilter } from "./filter";
import { shownWorkspaces } from "./groups";

const ALL = "all";

// DENSE drops the toggle item's 44px touch minimum for the desktop's denser bars, and lets the
// group take only the room its items need (the group's w-full would crowd out HostLabel's flex-1).
// A `flex-none` item is only as wide as its text, so it carries its own padding and never wraps.
const DENSE = "after:min-h-0 after:min-w-0 flex-none px-2 whitespace-nowrap";
const DENSE_GROUP = "w-auto flex-none";

// WorkspaceFilter draws nothing with no snapshot or fewer than two shown workspaces. Choosing the
// chosen item again, which Base UI reports as an empty value, picks all.
export function WorkspaceFilter({ dense }: { dense?: boolean }): ReactElement | null {
  const snapshot = useFleet((s) => s.snapshot);
  const stored = useFilter((s) => s.stored);
  if (snapshot === null) return null;
  const workspaces = shownWorkspaces(snapshot);
  if (workspaces.length < 2) return null;
  const active = activeFilter(stored, workspaces);
  const className = dense === true ? DENSE : undefined;
  return (
    <ToggleGroup
      className={dense === true ? DENSE_GROUP : undefined}
      aria-label="Workspaces"
      value={[active === "" ? ALL : active]}
      onValueChange={(value: string[]) => {
        const id = value[0] === undefined || value[0] === ALL ? "" : value[0];
        useFilter.getState().pick(id);
      }}
    >
      <ToggleGroupItem value={ALL} className={className}>
        All
      </ToggleGroupItem>
      {workspaces.map((w) => (
        <ToggleGroupItem key={w.id} value={String(w.id)} className={className}>
          {w.label || w.name}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  );
}
