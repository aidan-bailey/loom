import type { ComponentType } from "react";

import type { Session } from "@/api/gen";

// PanelId names every screen of the classic app (spec §22); a panel is
// registered under one of these, and a route no panel is registered for
// draws the placeholder.
export type PanelId =
  | "fleet"
  | "turns"
  | "info"
  | "changes"
  | "mockup"
  | "mockupFull"
  | "agent"
  | "job"
  | "flow"
  | "flowStory"
  | "flowDoc"
  | "flows"
  | "new"
  | "settings"
  | "pair";

// PanelProps is what the shell hands a panel's Head and Body: the session
// the route names (Number(params.id), undefined without one), the route's
// path values and its search, all strings (routeOf documents the keys).
export interface PanelProps {
  sessionId: number | undefined;
  params: Record<string, string>;
  search: Record<string, string>;
}

// PanelTab makes a panel one of a session's tabs: its place, its label and an
// optional Mark drawn after the label.
export interface PanelTab {
  order: number;
  label: string;
  Mark?: ComponentType<{ session: Session }>;
}

// Panel is one view. title names it in its error boundaries. Head is its context row in the pinned header, Body its
// screen; tab makes it one of a session's tabs (SessionTabs), shown for the
// sessions appliesTo accepts (every session without it). Strip is the line
// under the session's tabs; a tab's Mark is drawn after its label and is
// handed the session.
export interface Panel {
  id: PanelId;
  title: (props: PanelProps) => string;
  appliesTo?: (session: Session) => boolean;
  tab?: PanelTab;
  Head?: ComponentType<PanelProps>;
  Strip?: ComponentType<PanelProps>;
  Body: ComponentType<PanelProps>;
}

const registry = new Map<PanelId, Panel>();

// definePanel registers a panel (a second definition of an id replaces the
// first, as a hot reload needs) and returns it.
export function definePanel(panel: Panel): Panel {
  registry.set(panel.id, panel);
  return panel;
}

// panelFor is the panel registered under id, or undefined.
export function panelFor(id: PanelId): Panel | undefined {
  return registry.get(id);
}

// sessionTabs are the tab panels that apply to the session, in tab order.
export function sessionTabs(session: Session): Panel[] {
  const tabs: Panel[] = [];
  for (const panel of registry.values()) {
    if (panel.tab === undefined) continue;
    if (panel.appliesTo !== undefined && !panel.appliesTo(session)) continue;
    tabs.push(panel);
  }
  return tabs.sort((a, b) => (a.tab?.order ?? 0) - (b.tab?.order ?? 0));
}
