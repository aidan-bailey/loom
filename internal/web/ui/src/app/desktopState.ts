// The desktop's preferences (spec §6): whether the sidebar and the inspector show, and the
// inspector's tab, kept in localStorage `loom.desktop`. `slots` is a list so a later layout can add
// panes without a new key; this layout reads and writes only its first entry.
import { create } from "zustand";

import type { PanelId } from "./panels";

export const DESKTOP_KEY = "loom.desktop";

// DesktopSlot is one pane's choice: the tab it shows.
export interface DesktopSlot {
  tab: string;
}

export interface DesktopPrefs {
  sidebar: boolean;
  inspector: boolean;
  slots: DesktopSlot[];
}

function defaults(): DesktopPrefs {
  return { sidebar: true, inspector: true, slots: [] };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

// Kept is what the stored value holds that this layout does not own: the top-level keys other than
// the three it reads, and each slot object whole. A newer build's fields ride along in it and are
// written back unchanged, so a toggle here never drops them.
interface Kept {
  top: Record<string, unknown>;
  slots: Record<string, unknown>[];
}

interface Parsed {
  prefs: DesktopPrefs;
  kept: Kept;
}

function fallback(): Parsed {
  return { prefs: defaults(), kept: { top: {}, slots: [] } };
}

// parse reads the stored text: anything that is not exactly the shape (not JSON, not an object, a
// flag that is not a boolean, a slot with no string tab) is the default, so one bad part never
// keeps the good ones. A tab no panel has is kept: only the shape is read here, and the inspector
// skips a tab it does not show.
function parse(raw: string | null): Parsed {
  if (raw === null) return fallback();
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return fallback();
  }
  if (!isRecord(value)) return fallback();
  const { sidebar, inspector, slots, ...top } = value;
  if (typeof sidebar !== "boolean" || typeof inspector !== "boolean" || !Array.isArray(slots)) {
    return fallback();
  }
  const read: DesktopSlot[] = [];
  const keptSlots: Record<string, unknown>[] = [];
  for (const slot of slots as unknown[]) {
    if (!isRecord(slot) || typeof slot.tab !== "string") return fallback();
    read.push({ tab: slot.tab });
    keptSlots.push(slot);
  }
  return { prefs: { sidebar, inspector, slots: read }, kept: { top, slots: keptSlots } };
}

// readDesktopPrefs is parse's preferences alone.
export function readDesktopPrefs(raw: string | null): DesktopPrefs {
  return parse(raw).prefs;
}

function load(): Parsed {
  try {
    return parse(window.localStorage.getItem(DESKTOP_KEY));
  } catch {
    return fallback();
  }
}

function write(prefs: DesktopPrefs, kept: Kept): void {
  try {
    const slots = prefs.slots.map((slot, i) => ({ ...kept.slots[i], ...slot }));
    window.localStorage.setItem(DESKTOP_KEY, JSON.stringify({ ...kept.top, ...prefs, slots }));
  } catch {
    // Full or blocked storage: the choice holds for this page only.
  }
}

export interface DesktopState {
  sidebar: boolean;
  inspector: boolean;
  slots: DesktopSlot[];
  setSidebar: (shown: boolean) => void;
  setInspector: (shown: boolean) => void;
  // setTab sets the first slot's tab, adding the slot when there is none.
  setTab: (tab: PanelId) => void;
}

const initial = load();
let kept = initial.kept;

// useDesktop is read from DESKTOP_KEY when the module loads, and written back whole on each set
// (what the layout does not own, kept beside the state, goes back unchanged). Another window's
// write to the key replaces both, so a set here never undoes that window's choice.
export const useDesktop = create<DesktopState>()((set, get) => {
  const persist = (): void => {
    const { sidebar, inspector, slots } = get();
    write({ sidebar, inspector, slots }, kept);
  };
  return {
    ...initial.prefs,

    setSidebar(shown) {
      set({ sidebar: shown });
      persist();
    },

    setInspector(shown) {
      set({ inspector: shown });
      persist();
    },

    setTab(tab) {
      set({ slots: [{ tab }, ...get().slots.slice(1)] });
      persist();
    },
  };
});

// The browser fires `storage` here when another window of the origin writes the key.
window.addEventListener("storage", (event) => {
  if (event.key !== DESKTOP_KEY) return;
  const next = parse(event.newValue);
  kept = next.kept;
  useDesktop.setState(next.prefs);
});
