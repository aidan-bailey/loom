// The desktop's fleet sidebar (spec §8): chrome, not a panel. A plain list of links to the sessions
// of the shown workspaces, in compact two-line rows; sidebarLine decides each row's second line.
import type { Session } from "@/api/gen";
import { flowChip, flowTone } from "@/panels/fleet/SessionRow";

export interface SidebarLine {
  text: string;
  tone: "attention" | "docs" | "error" | "muted";
}

// sidebarLine is the row's second line: why a waiting session waits, else its newest flow's chip,
// else what it said last.
export function sidebarLine(session: Session): SidebarLine {
  if (session.group === "needs_you") {
    const why = [session.wait.reason, session.wait.detail].filter((p) => p !== "").join(": ");
    if (why !== "") return { text: why, tone: "attention" };
  }
  const flows = session.flows ?? [];
  if (flows.length > 0) {
    const time = (f: (typeof flows)[number]): number => Date.parse(f.updated) || 0;
    let newest = flows[0];
    for (const f of flows) if (time(f) > time(newest)) newest = f;
    return { text: flowChip(newest), tone: flowTone(newest) };
  }
  return { text: session.last_message, tone: "muted" };
}
