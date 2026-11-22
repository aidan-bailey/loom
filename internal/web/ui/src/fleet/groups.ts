// The fleet's grouping, ported from the classic app's fleet.mjs: the four groups, their order, and
// the sessions a count in the footer cycles through.
import type { Session, SessionGroup, Snapshot, Workspace } from "@/api/gen";

import { activeFilter } from "./filter";

export type FleetGroups = Record<SessionGroup, Session[]>;

export const GROUPS: { key: SessionGroup; title: string }[] = [
  { key: "needs_you", title: "Needs you" },
  { key: "your_turn", title: "Your turn" },
  { key: "working", title: "Working" },
  { key: "paused", title: "Paused" },
];

export function emptyGroups(): FleetGroups {
  return { needs_you: [], your_turn: [], working: [], paused: [] };
}

/** Every session of every workspace, as the snapshot lists them. */
export function allSessions(snapshot: Snapshot): Session[] {
  return Object.values(snapshot.sessions ?? {}).flatMap((list) => list ?? []);
}

/**
 * The workspaces the fleet lists, in the daemon's order (the open tabs first). One without the
 * flag, from an older daemon, is shown.
 */
export function shownWorkspaces(snapshot: Snapshot): Workspace[] {
  return (snapshot.workspaces ?? []).filter((w) => w.shown !== false);
}

/**
 * Whether a session of a shown workspace has at least one flow: the rule of the top bar's and the
 * fleet header's Flows link and of the palette's Flows place.
 */
export function hasShownFlows(snapshot: Snapshot): boolean {
  const shown = new Set(shownWorkspaces(snapshot).map((w) => w.id));
  return allSessions(snapshot).some((x) => shown.has(x.workspace) && (x.flows ?? []).length > 0);
}

function isGroup(group: string): group is SessionGroup {
  return GROUPS.some((g) => g.key === group);
}

const since = (s: Session): number => Date.parse(s.since) || 0;

/**
 * Groups the sessions: Needs you with the longest wait first, the others newest first. A workspace
 * of null keeps every workspace; a group this build does not know is listed under Paused.
 */
export function groupSessions(sessions: Session[], workspace: number | null): FleetGroups {
  const out = emptyGroups();
  for (const s of sessions) {
    if (workspace !== null && s.workspace !== workspace) continue;
    out[isGroup(s.group) ? s.group : "paused"].push(s);
  }
  out.needs_you.sort((a, b) => since(a) - since(b));
  for (const key of ["your_turn", "working", "paused"] as const) {
    out[key].sort((a, b) => since(b) - since(a));
  }
  return out;
}

/**
 * Groups the sessions of the shown workspaces as the fleet does, whatever the fleet's workspace
 * filter: the footer's counts and the sessions a count's tap cycles through.
 */
export function fleetGroups(snapshot: Snapshot): FleetGroups {
  const shown = new Set(shownWorkspaces(snapshot).map((w) => w.id));
  return groupSessions(
    allSessions(snapshot).filter((s) => shown.has(s.workspace)),
    null,
  );
}

/**
 * The fleet's listing under a workspace filter (the stored one, as activeFilter reads it): the
 * sessions of the shown workspaces, grouped, with the filter in force ("" for every workspace). The
 * fleet panel and the desktop's sidebar both list this way, so they cannot drift apart.
 */
export function listFleet(
  snapshot: Snapshot,
  workspaces: Workspace[],
  stored: string,
): { active: string; grouped: FleetGroups } {
  const shown = new Set(workspaces.map((w) => w.id));
  const listed = allSessions(snapshot).filter((s) => shown.has(s.workspace));
  const active = activeFilter(stored, workspaces);
  return {
    active,
    grouped: groupSessions(listed, active === "" ? null : Number(active)),
  };
}

/**
 * The session a count's tap opens: the one after currentId in the group's order, wrapping to the
 * first, or the first when currentId is not in the group; undefined for an empty group.
 */
export function nextInGroup(
  groups: FleetGroups,
  group: SessionGroup,
  currentId: number | null,
): Session | undefined {
  const list = groups[group];
  if (list.length === 0) return undefined;
  const at = list.findIndex((s) => s.id === currentId);
  return list[(at + 1) % list.length];
}
