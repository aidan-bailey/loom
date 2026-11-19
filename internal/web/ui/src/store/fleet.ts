// The live store (spec §24): the snapshot and the link state, changed by each stream event.
// Components read it through selectors, so an event re-renders only what read the changed data.
import { useMemo } from "react";
import { create } from "zustand";
import { useShallow } from "zustand/react/shallow";
import type { Session, Snapshot, StreamEvents } from "@/api/gen";
import { queryClient } from "@/api/queries";
import { emitNotices, onStopping, onUnpaired } from "@/api/request";
import { connectStream, RESTART_GRACE, type LinkState } from "@/api/stream";
import { checkPushSubscription, shellBuild, upgradeShell } from "@/app/worker";
import { allSessions, emptyGroups, fleetGroups, type FleetGroups } from "@/fleet/groups";
import { setBadge } from "./badge";
import { useDrafts } from "./drafts";
import { clearSnapshot, loadSnapshot, saveSnapshot } from "./persist";

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

// How many times the store has been cleared: a load that began before a clear is the revoked
// device's snapshot and is dropped.
let cleared = 0;

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
    cleared += 1;
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

// The snapshot is saved at most once a second: sessions events come up to four a second.
const SAVE_EVERY = 1000;

// scheduleSave saves the store's latest state SAVE_EVERY after the first change since the last
// save: a throttle, not a debounce, which would starve while events keep coming. The timer reads
// the store when it fires, and writes nothing while the store holds no snapshot, so a save
// scheduled before a clear never lands.
function scheduleSave(): void {
  if (saveTimer !== undefined) return;
  saveTimer = setTimeout(() => {
    saveTimer = undefined;
    const { snapshot, updated } = useFleet.getState();
    if (snapshot !== null) saveSnapshot(snapshot, updated);
  }, SAVE_EVERY);
}

// afterEvent is what an event does besides changing the store: the badge, the save, the toast.
function afterEvent(event: AnyEvent): void {
  switch (event.name) {
    case "snapshot":
      // The launch-time checks that follow a snapshot run in startFleet's onEvent.
      setBadge(event.data.badge);
      scheduleSave();
      break;
    case "sessions":
    case "workspaces":
      setBadge(event.data.badge);
      scheduleSave();
      break;
    case "usage":
    case "quick_replies":
    case "device":
      scheduleSave();
      break;
    case "notice":
      scheduleSave();
      emitNotices([event.data]);
      break;
    case "ping":
    case "bye":
      break;
  }
}

let running: (() => void) | undefined;

/**
 * Starts the fleet: loads the saved snapshot (the view is stale until the stream's first one),
 * connects the stream into the store, saves at most once a second, sets the badge, and hands each
 * stream notice to the onNotices listeners. A request's 503 with the daemon's stopping text, while
 * the stream is down, keeps the link at "restarting" until the stream is live again or RESTART_GRACE
 * (30 s) has passed, then the link follows the stream. When the
 * device is no longer paired (any request's 401 or the stream's) it closes the stream and clears the
 * store, the saved snapshot, the cached queries and the badge; it never navigates. Starting again
 * stops the previous start. It returns its stop.
 */
export function startFleet(): () => void {
  running?.();
  let stopped = false;
  const epoch = cleared;
  // Set by a request's stopping 503 while the stream is down: the daemon is restarting, so the link
  // says so (not "offline") until the stream is live again, or RESTART_GRACE passes (the stream's
  // own bye gets no longer): then the footer follows the stream's state again.
  let stopping = false;
  let stoppingTimer: ReturnType<typeof setTimeout> | undefined;
  // The stream's last reported state, which the link falls back to once the grace is over.
  let streamState: LinkState = "connecting";

  void loadSnapshot().then((saved) => {
    // A clear since the load began is an unpairing: the saved fleet is the revoked device's.
    if (stopped || saved === null || epoch !== cleared) return;
    useFleet.getState().load(saved.snapshot, saved.updated);
  });

  // The push subscription is checked once per launch, after the first snapshot of this start.
  let launched = false;

  const closeStream = connectStream({
    onEvent(name, data) {
      useFleet.getState().apply(name, data);
      const event = { name, data } as unknown as AnyEvent;
      afterEvent(event);
      if (event.name === "snapshot") {
        // A snapshot from another build than the shell's asks the worker for the new shell (once
        // per build); a build unknown on either side asks for nothing.
        const shell = shellBuild();
        const differs = Boolean(event.data.build && shell && event.data.build !== shell);
        const upgrading = differs && upgradeShell(event.data.build);
        // A page that has just started a reload for a new shell leaves the check to its next
        // launch; one whose build was already asked for (and did not change) checks as usual.
        if (!launched && !upgrading) {
          launched = true;
          void checkPushSubscription(event.data.device);
        }
      }
    },
    onLink(state) {
      streamState = state;
      if (state === "live") {
        stopping = false;
        clearTimeout(stoppingTimer);
      }
      useFleet.getState().setLink(stopping && state === "offline" ? "restarting" : state);
    },
  });

  // A live stream is left alone: the daemon's bye already says "restarting" if it is stopping.
  const offStopping = onStopping(() => {
    if (useFleet.getState().link === "live") return;
    stopping = true;
    useFleet.getState().setLink("restarting");
    // A later stopping 503 starts the grace again.
    clearTimeout(stoppingTimer);
    stoppingTimer = setTimeout(() => {
      stopping = false;
      if (useFleet.getState().link === "restarting") useFleet.getState().setLink(streamState);
    }, RESTART_GRACE);
  });

  const offUnpaired = onUnpaired(() => {
    closeStream();
    void useFleet.getState().clear();
    // Nothing of the revoked pairing outlives it: the turns, diffs and logs the query client
    // cached would come back if the app were paired again before a reload, and so would the badge.
    queryClient.clear();
    // The unsent replies and answers are the revoked device's too.
    useDrafts.getState().clear();
    setBadge(0);
  });

  const stop = () => {
    if (stopped) return;
    stopped = true;
    closeStream();
    offUnpaired();
    offStopping();
    clearTimeout(stoppingTimer);
    cancelSave();
    if (running === stop) running = undefined;
  };
  running = stop;
  return stop;
}
