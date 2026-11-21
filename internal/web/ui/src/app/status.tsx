// The parts of the status row, shared by the phone's footer (StatusRow, app/shell.tsx) and the
// desktop's top bar: the link's pill, loom@host and a count per group.
import type { FunctionComponent, ReactElement } from "react";

import type { SessionGroup } from "@/api/gen";
import type { LinkState } from "@/api/stream";
import { fleetGroups, nextInGroup } from "@/fleet/groups";
import { cn } from "@/lib/utils";
import { useFleet, useFleetGroups, useLink } from "@/store/fleet";

import { hrefFor, panelProps, useRoute } from "./routes";

const PILL: Record<LinkState, string> = {
  live: "LIVE",
  connecting: "CONNECTING",
  restarting: "RESTARTING",
  offline: "OFFLINE",
};

const PILL_TONE: Record<LinkState, string> = {
  live: "bg-working",
  connecting: "bg-muted-foreground",
  restarting: "bg-attention",
  offline: "bg-error",
};

// COUNTED are the groups the status row counts, in its order.
const COUNTED: { key: SessionGroup; glyph: string; words: string; tone: string }[] = [
  { key: "needs_you", glyph: "●", words: "need you", tone: "text-attention" },
  { key: "your_turn", glyph: "◆", words: "your turn", tone: "text-turn" },
  { key: "working", glyph: "◐", words: "working", tone: "text-working" },
];

// clock is the time of day of a ms timestamp.
function clock(ms: number): string {
  return new Date(ms).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

// `dense` is the top bar's density: only GroupCounts reads it (it drops the 44px minimum); LinkPill and
// HostLabel draw the same either way and take the prop so the top bar can pass it to all three.
interface PartProps {
  dense?: boolean;
}

// LinkPill is the link's state as a coloured pill.
export const LinkPill: FunctionComponent<PartProps> = () => {
  const { state } = useLink();
  return (
    <span
      aria-live="polite"
      className={cn("mr-1 flex-none rounded-sm px-1.5 font-bold text-background", PILL_TONE[state])}
    >
      {PILL[state]}
    </span>
  );
};

// HostLabel is loom@host, with the time of the last update while offline.
export const HostLabel: FunctionComponent<PartProps> = () => {
  const { state } = useLink();
  const host = useFleet((s) => s.snapshot?.host ?? "");
  const updated = useFleet((s) => s.updated);
  const when = state === "offline" && updated > 0 ? ` · ${clock(updated)}` : "";
  return (
    <span className="min-w-0 flex-1 truncate font-mono">
      {host === "" ? (
        "loom"
      ) : (
        <>
          <span className="text-muted-foreground">loom@</span>
          {host}
        </>
      )}
      {when}
    </span>
  );
};

// GroupCounts is a count per group over the shown workspaces, as siblings in the row's flex. A
// count opens the group's next session: the one after the session on screen, in the group's order
// at the tap, wrapping, or the first. While the stream is down the counts are the cached
// snapshot's, drawn faint and still buttons; a zero is no button.
export function GroupCounts({ dense }: PartProps): ReactElement {
  const { state } = useLink();
  const groups = useFleetGroups();
  const route = useRoute();

  const open = (group: SessionGroup): void => {
    const snapshot = useFleet.getState().snapshot;
    if (snapshot === null) return;
    const current = panelProps(route).sessionId ?? null;
    const next = nextInGroup(fleetGroups(snapshot), group, current);
    if (next !== undefined) window.location.assign(hrefFor("turns", { id: String(next.id) }));
  };

  return (
    <>
      {COUNTED.map((g) => {
        const n = groups[g.key].length;
        const tone = cn(
          "flex flex-none items-center gap-1 px-2 tabular-nums",
          dense !== true && "min-h-[44px]",
          g.tone,
          state !== "live" && "opacity-60",
        );
        const inner = (
          <>
            <span aria-hidden="true">{g.glyph}</span>
            {n}
          </>
        );
        if (n === 0) {
          return (
            <span key={g.key} className={cn(tone, "opacity-40")}>
              {inner}
            </span>
          );
        }
        return (
          <button
            key={g.key}
            type="button"
            aria-label={`${n} ${g.words}: open the next`}
            className={tone}
            onClick={() => {
              open(g.key);
            }}
          >
            {inner}
          </button>
        );
      })}
    </>
  );
}
