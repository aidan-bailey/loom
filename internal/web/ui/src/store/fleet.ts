// The live store (spec §24): the snapshot and the link state, changed by each stream event.
// Components read it through selectors, so an event re-renders only what read the changed data.
import { useMemo } from "react";
import { create } from "zustand";
import { useShallow } from "zustand/react/shallow";
import type { Session, Snapshot, StreamEvents } from "@/api/gen";
import type { LinkState } from "@/api/stream";
import { allSessions, emptyGroups, fleetGroups, type FleetGroups } from "@/fleet/groups";
import { clearSnapshot } from "./persist";

export interface FleetState {
  snapshot: Snapshot | null;
  stale: boolean;
  link: LinkState;
  updated: number;
  apply: <E extends keyof StreamEvents>(name: E, data: StreamEvents[E]) => void;
  setLink: (state: LinkState) => void;
  load: (saved: Snapshot, updated: number) => void;
  clear: () => Promise<void>;
}

type AnyEvent = {
  [E in keyof StreamEvents]: { name: E; data: StreamEvents[E] };
}[keyof StreamEvents];

// The snapshot keeps the last 20 notices.
const RECENT_NOTICES = 20;

// The timer of the save waiting to run (scheduled by startFleet). clear() must cancel it, so it
// lives beside the store.
let saveTimer: ReturnType<typeof setTimeout> | undefined;

function cancelSave(): void {
  if (saveTimer !== undefined) {
    clearTimeout(saveTimer);
    saveTimer = undefined;
  }
}

// applied is what one stream event changes. A snapshot installs the view and says it is live; a
// ping only stamps the time; an event that edits the snapshot is ignored before the first one.
function applied(state: FleetState, event: AnyEvent, now: number): Partial<FleetState> {
  const current = state.snapshot;
  const change = (edit: (snapshot: Snapshot) => Snapshot): Partial<FleetState> =>
    current === null ? {} : { snapshot: edit(current), updated: now };

  switch (event.name) {
    case "snapshot":
      return { snapshot: event.data, updated: now, link: "live", stale: false };
    case "ping":
      return { updated: now };
    case "bye":
      return {};
    case "sessions": {
      const { workspace, sessions } = event.data;
      return change((s) => ({
        ...s,
        sessions: { ...(s.sessions ?? {}), [String(workspace)]: sessions },
      }));
    }
    case "workspaces": {
      const { workspaces } = event.data;
      // A closed workspace sends no sessions event: drop its sessions here. The keys are strings of
      // numeric IDs.
      const open = new Set((workspaces ?? []).map((w) => String(w.id)));
      return change((s) => {
        const kept: Record<string, Session[] | null> = {};
        for (const [id, list] of Object.entries(s.sessions ?? {})) {
          if (open.has(id)) kept[id] = list;
        }
        return { ...s, workspaces, sessions: kept };
      });
    }
    case "usage": {
      const { usage } = event.data;
      return change((s) => ({ ...s, usage }));
    }
    case "quick_replies": {
      const { quick_replies } = event.data;
      return change((s) => ({ ...s, quick_replies }));
    }
    case "device": {
      const device = event.data;
      return change((s) => ({ ...s, device }));
    }
    case "notice": {
      const notice = event.data;
      return change((s) => ({
        ...s,
        notices: [...(s.notices ?? []), notice].slice(-RECENT_NOTICES),
      }));
    }
  }
}

export const useFleet = create<FleetState>()((set, get) => ({
  snapshot: null,
  stale: true,
  link: "connecting",
  updated: 0,

  apply(name, data) {
    const event = { name, data } as unknown as AnyEvent;
    const patch = applied(get(), event, Date.now());
    if (Object.keys(patch).length > 0) set(patch);
  },

  setLink(state) {
    set({ link: state, stale: state !== "live" });
  },

  // A cold start's saved snapshot: stale until the stream's first one, and never over a live view.
  load(saved, updated) {
    if (get().snapshot !== null) return;
    set({ snapshot: saved, updated });
  },

  // Forgets everything the phone holds. A pending save is cancelled first, so it can't write the
  // revoked device's fleet back after the clear.
  async clear() {
    cancelSave();
    set({ snapshot: null, stale: true, link: "connecting", updated: 0 });
    await clearSnapshot();
  },
}));

/** The session with this id, or undefined (for an undefined id, an unknown one, or no snapshot). */
export function useSession(id: number | undefined): Session | undefined {
  return useFleet((s) =>
    id === undefined || s.snapshot === null
      ? undefined
      : allSessions(s.snapshot).find((x) => x.id === id),
  );
}

/** The fleet's sessions of the shown workspaces in their four groups; empty before a snapshot. */
export function useFleetGroups(): FleetGroups {
  const snapshot = useFleet((s) => s.snapshot);
  return useMemo(() => (snapshot === null ? emptyGroups() : fleetGroups(snapshot)), [snapshot]);
}

/** The stream's link state and whether the view is stale (every state but live). */
export function useLink(): { state: LinkState; stale: boolean } {
  return useFleet(useShallow((s) => ({ state: s.link, stale: s.stale })));
}
