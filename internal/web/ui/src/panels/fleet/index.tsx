// The fleet panel (direction B): the sessions of the shown workspaces in cards under their group's
// heading, a workspace filter on top. A port of the classic fleet screen.
import { useEffect, useMemo, useState, type ReactElement } from "react";

import { definePanel } from "@/app/panels";
import { Badge } from "@/components/ui/badge";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { Session, SessionGroup, Usage } from "@/api/gen";
import { allSessions, groupSessions, GROUPS, shownWorkspaces } from "@/fleet/groups";
import { hasNewTurn, pruneSeen, useSeen } from "@/fleet/seen";
import { useFleet } from "@/store/fleet";

import { FleetHead } from "./FleetHead";
import { SessionRow } from "./SessionRow";

// The classic app keeps the workspace filter in localStorage under this key, as a JSON string
// holding a workspace ID or "", so the filter survives a switch between the apps.
export const FILTER_KEY = "loom.filter";
const ALL = "all";

function readFilter(): string {
  try {
    const value: unknown = JSON.parse(window.localStorage.getItem(FILTER_KEY) ?? '""');
    return typeof value === "string" ? value : "";
  } catch {
    return "";
  }
}

function writeFilter(id: string): void {
  try {
    window.localStorage.setItem(FILTER_KEY, JSON.stringify(id));
  } catch {
    // A full or refused store keeps the choice for this launch only.
  }
}

function Card({
  sessions,
  usage,
  chipOf,
  seen,
}: {
  sessions: Session[];
  usage: Usage[];
  chipOf: (s: Session) => string | undefined;
  seen: Record<string, number>;
}): ReactElement {
  return (
    <div className="overflow-hidden rounded-xl border border-border bg-card">
      {sessions.map((s) => (
        <SessionRow
          key={s.id}
          session={s}
          usage={usage}
          chip={chipOf(s)}
          fresh={hasNewTurn(s, seen)}
        />
      ))}
    </div>
  );
}

function Heading({
  title,
  count,
  attention,
}: {
  title: string;
  count: number;
  attention: boolean;
}): ReactElement {
  return (
    <span className="flex items-center gap-2 px-1 text-xs font-semibold tracking-wide text-muted-foreground uppercase">
      {title}
      <Badge variant={attention ? "attention" : "neutral"}>{count}</Badge>
    </span>
  );
}

function Fleet(): ReactElement {
  const snapshot = useFleet((s) => s.snapshot);
  const [stored, setStored] = useState(readFilter);
  const [, setTick] = useState(0);
  const seen = useSeen();

  // A session the daemon no longer lists keeps no time: every session of every workspace, shown or
  // not, is listed (an empty fleet prunes nothing).
  useEffect(() => {
    if (snapshot !== null) pruneSeen(allSessions(snapshot).map((s) => s.id));
  }, [snapshot]);

  // Ages and the shares' staleness move with the clock, not only with the stream.
  useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 30_000);
    return () => clearInterval(t);
  }, []);

  const workspaces = useMemo(
    () => (snapshot === null ? [] : shownWorkspaces(snapshot)),
    [snapshot],
  );
  const groups = useMemo(() => {
    if (snapshot === null) return null;
    const shown = new Set(workspaces.map((w) => w.id));
    const listed = allSessions(snapshot).filter((s) => shown.has(s.workspace));
    const active = workspaces.some((w) => String(w.id) === stored) ? stored : "";
    return {
      active,
      listed,
      grouped: groupSessions(listed, active === "" ? null : Number(active)),
    };
  }, [snapshot, workspaces, stored]);

  if (snapshot === null || groups === null) {
    return <p className="p-4 text-sm text-muted-foreground">Loading…</p>;
  }

  const { active, grouped } = groups;
  const names = new Map(workspaces.map((w) => [w.id, w.label || w.name]));
  const chipOf = (s: Session): string | undefined =>
    active === "" && workspaces.length > 1 ? names.get(s.workspace) : undefined;
  const usage = snapshot.usage ?? [];
  const total = GROUPS.reduce((n, g) => n + grouped[g.key].length, 0);

  const pick = (value: string[]): void => {
    const id = value[0] === undefined || value[0] === ALL ? "" : value[0];
    setStored(id);
    writeFilter(id);
  };

  const shownGroup = (key: SessionGroup, title: string): ReactElement | null => {
    const list = grouped[key];
    if (list.length === 0) return null;
    if (key === "paused") {
      return (
        <Collapsible key={key}>
          <CollapsibleTrigger className="mb-2 flex min-h-[44px] w-full items-center">
            <Heading title={title} count={list.length} attention={false} />
          </CollapsibleTrigger>
          <CollapsibleContent>
            <Card sessions={list} usage={usage} chipOf={chipOf} seen={seen} />
          </CollapsibleContent>
        </Collapsible>
      );
    }
    return (
      <section key={key}>
        <h2 className="mb-2 flex min-h-8 items-center">
          <Heading title={title} count={list.length} attention={key === "needs_you"} />
        </h2>
        <Card sessions={list} usage={usage} chipOf={chipOf} seen={seen} />
      </section>
    );
  };

  return (
    <div className="flex flex-col gap-4 p-4">
      {workspaces.length > 1 ? (
        <ToggleGroup
          aria-label="Workspaces"
          value={[active === "" ? ALL : active]}
          onValueChange={pick}
        >
          <ToggleGroupItem value={ALL}>All</ToggleGroupItem>
          {workspaces.map((w) => (
            <ToggleGroupItem key={w.id} value={String(w.id)}>
              {w.label || w.name}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      ) : null}
      {total === 0 ? (
        <p className="text-sm text-muted-foreground">
          {active === "" ? "No sessions." : "No sessions in this workspace."}
        </p>
      ) : null}
      {GROUPS.map((g) => shownGroup(g.key, g.title))}
    </div>
  );
}

export const fleetPanel = definePanel({
  id: "fleet",
  title: () => "Fleet",
  Head: FleetHead,
  Body: Fleet,
});
