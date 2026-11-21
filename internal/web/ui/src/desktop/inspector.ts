// What the desktop's inspector shows (spec §6, §10). The stored choice is the first slot of
// `loom.desktop`'s tab; inspectorTab decides which tab is drawn from it: the choice itself when it
// applies and is not the middle's, else the next applicable tab after it in tab order, else the
// first applicable one that is not the middle's.
import type { PanelId } from "@/app/panels";

// inspectorTab: `order` is every tab panel in tab order, `tabs` those that apply to the session in
// the same order, `middle` the tab the middle lights and `chosen` the stored choice. It is
// undefined when no tab but the middle's applies.
export function inspectorTab(
  order: PanelId[],
  tabs: PanelId[],
  middle: PanelId,
  chosen: string | undefined,
): PanelId | undefined {
  const usable = (id: PanelId): boolean => id !== middle && tabs.includes(id);
  const kept = tabs.find((id) => id === chosen);
  if (kept !== undefined && usable(kept)) return kept;
  const at = chosen === undefined ? -1 : order.findIndex((id) => id === chosen);
  if (at >= 0) {
    // Every other id of `order` once, starting after the choice and wrapping past the end.
    for (let step = 1; step < order.length; step++) {
      const id = order[(at + step) % order.length];
      if (usable(id)) return id;
    }
    return undefined;
  }
  return tabs.find((id) => id !== middle);
}
