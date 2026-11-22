// The palette's open store and its chord, a module of the desktop chunk: nothing in the entry
// imports it. The chord is the app's only document key listener (spec decision 2, kept alone from
// the keys that left the epic), so the one `keydown` listener below is the only place a key is read.
import { useEffect } from "react";
import { create } from "zustand";

import { confirmOpen } from "@/app/ConfirmSheet";

export interface PaletteState {
  open: boolean;
  setOpen: (open: boolean) => void;
  toggle: () => void;
}

// usePalette holds whether the palette is open, in memory only.
export const usePalette = create<PaletteState>()((set) => ({
  open: false,
  setOpen: (open) => set({ open }),
  toggle: () => set((s) => ({ open: !s.open })),
}));

// isPaletteChord is Ctrl+K or Meta+K, with no Alt or Shift and not while composing. It reads `key`,
// not `code`, because spec decision 6 rejected a non-Latin layout's `code` fallback.
export function isPaletteChord(e: KeyboardEvent): boolean {
  return (
    (e.metaKey || e.ctrlKey) &&
    !e.altKey &&
    !e.shiftKey &&
    !e.isComposing &&
    (e.key === "k" || e.key === "K")
  );
}

// usePaletteKey toggles the palette on the chord from anywhere, a text field included. The chord is
// refused, after preventDefault, while a confirmation is open.
export function usePaletteKey(): void {
  useEffect(() => {
    const onKey = (event: KeyboardEvent): void => {
      if (!isPaletteChord(event)) return;
      event.preventDefault();
      if (confirmOpen()) return;
      usePalette.getState().toggle();
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      // A palette open when DesktopShell unmounts would otherwise come back open.
      usePalette.getState().setOpen(false);
    };
  }, []);
}

// paletteKeyLabel is the chord as the top bar's box shows it for a platform.
export function paletteKeyLabel(platform: string): string {
  return /mac|iphone|ipad/i.test(platform) ? "⌘K" : "Ctrl K";
}
