// The session just created (spec Part 3 settlement 8). A 201 from POST /api/v1/sessions names the
// session before the snapshot lists it, so for PENDING_MS the views of that id draw "Loading…"
// rather than "This session is gone.". The wait ends sooner when the snapshot lists the session,
// and when the device is unpaired.
import { create } from "zustand";

import type { Snapshot } from "@/api/gen";
import { onUnpaired } from "@/api/request";
import { allSessions } from "@/fleet/groups";

import { useFleet } from "./fleet";

export const PENDING_MS = 5000;

export interface PendingState {
  id: number | null;
  mark: (id: number) => void;
  clear: () => void;
}

// timer ends the wait and unwatch closes the watch of the snapshot; both belong to the one id
// held, and live beside the store as the fleet's save timer does.
let timer: ReturnType<typeof setTimeout> | undefined;
let unwatch: (() => void) | undefined;

function listed(snapshot: Snapshot | null, id: number): boolean {
  return snapshot !== null && allSessions(snapshot).some((s) => s.id === id);
}

// release ends the wait for the id held: its timer and its watch of the snapshot.
function release(): void {
  clearTimeout(timer);
  timer = undefined;
  unwatch?.();
  unwatch = undefined;
}

export const usePending = create<PendingState>()((set, get) => ({
  id: null,

  // mark holds id for PENDING_MS, replacing any earlier id. It lets go at once for a session the
  // snapshot already lists, and later as soon as a snapshot does. The watch opens here, never at
  // module load: this module imports the store, and a subscription at the top level would run
  // while the store's own module may still be loading.
  mark(id) {
    release();
    if (listed(useFleet.getState().snapshot, id)) {
      set({ id: null });
      return;
    }
    set({ id });
    timer = setTimeout(() => {
      timer = undefined;
      get().clear();
    }, PENDING_MS);
    unwatch = useFleet.subscribe((state) => {
      if (listed(state.snapshot, id)) get().clear();
    });
  },

  clear() {
    release();
    set({ id: null });
  },
}));

// An unpairing ends the wait: the new session was the revoked device's.
onUnpaired(() => {
  usePending.getState().clear();
});

/** Whether the session with this id was created a moment ago and is not in the snapshot yet. */
export function useIsPending(id: number | undefined): boolean {
  return usePending((s) => id !== undefined && s.id === id);
}
