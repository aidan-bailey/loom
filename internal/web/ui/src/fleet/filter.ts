// The workspace filter's choice: one key and one in-memory copy, shared by the fleet, the new-session
// form and the desktop's top bar and sidebar.
import { create } from "zustand";

import type { Workspace } from "@/api/gen";

// The classic app keeps the workspace filter in localStorage under this key, as a JSON string
// holding a workspace ID or "", so the filter survives a switch between the apps.
export const FILTER_KEY = "loom.filter";

// readFilter is the stored workspace ID, "" for all; anything unreadable (a throw included) is all.
export function readFilter(): string {
  try {
    const value: unknown = JSON.parse(window.localStorage.getItem(FILTER_KEY) ?? '""');
    return typeof value === "string" ? value : "";
  } catch {
    return "";
  }
}

export interface FilterState {
  // stored is the choice: a workspace ID, or "" for all.
  stored: string;
  // pick keeps a choice and writes it under FILTER_KEY.
  pick: (id: string) => void;
}

// useFilter starts from the stored choice when the module loads; later changes come only through
// pick.
export const useFilter = create<FilterState>()((set) => ({
  stored: readFilter(),
  pick: (id) => {
    set({ stored: id });
    try {
      window.localStorage.setItem(FILTER_KEY, JSON.stringify(id));
    } catch {
      // A full or refused store keeps the choice for this launch only.
    }
  },
}));

// activeFilter is the stored choice when a listed workspace holds it, else "" (all): an unlisted
// stored ID is read as all and left stored.
export function activeFilter(stored: string, workspaces: Workspace[]): string {
  return workspaces.some((w) => String(w.id) === stored) ? stored : "";
}
