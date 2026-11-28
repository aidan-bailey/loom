// The desktop's fleet sidebar (spec §8): chrome, not a panel. A plain list of links to the sessions
// of the shown workspaces, in compact two-line rows (the first naming the session's repo, its
// workspace); sidebarLine decides each row's second line, and Sidebar draws the groups, the rows and
// the highlight of the session on screen.
import { useEffect, useMemo, useState, type ReactElement } from "react";

import type { Session, SessionGroup } from "@/api/gen";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { useFilter } from "@/fleet/filter";
import { GROUPS, listFleet, shownWorkspaces } from "@/fleet/groups";
import { sessionHref } from "@/fleet/links";
import { cn } from "@/lib/utils";
import { ago, DOT, flowChip, flowTone } from "@/panels/fleet/SessionRow";
import { useFleet } from "@/store/fleet";

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

const TONE: Record<SidebarLine["tone"], string> = {
  attention: "text-attention",
  docs: "text-docs",
  error: "text-error",
  muted: "text-muted-foreground",
};

const HEADING: Record<SessionGroup, string> = {
  needs_you: "text-attention",
  your_turn: "text-turn",
  working: "text-working",
  paused: "text-muted-foreground",
};

function Row({
  session,
  repo,
  current,
}: {
  session: Session;
  repo: string;
  current: boolean;
}): ReactElement {
  const line = sidebarLine(session);
  return (
    <a
      data-slot="sidebar-row"
      href={sessionHref(session)}
      aria-current={current ? "page" : undefined}
      className={cn("block border-b border-border px-3 py-1.5", current && "bg-secondary")}
    >
      <div className="flex items-center gap-2">
        <span
          data-slot="sidebar-dot"
          aria-hidden="true"
          className={cn(
            "size-2 flex-none rounded-full",
            DOT[session.group] ?? "bg-muted-foreground",
          )}
        />
        <span className="min-w-0 flex-1 truncate text-[13px] font-medium">{session.title}</span>
        {repo === "" ? null : (
          <span
            data-slot="sidebar-repo"
            className="max-w-[40%] flex-none truncate text-[11px] text-muted-foreground"
          >
            {repo}
          </span>
        )}
        <span className="flex-none font-mono text-[11px] text-muted-foreground">
          {ago(session.since)}
        </span>
      </div>
      {line.text === "" ? null : (
        <div
          data-slot="sidebar-line"
          data-tone={line.tone}
          className={cn("block truncate pl-4 text-xs", TONE[line.tone])}
        >
          {line.text}
        </div>
      )}
    </a>
  );
}

function Heading({ group, title, count }: { group: SessionGroup; title: string; count: number }) {
  return (
    <span
      data-slot="sidebar-heading"
      className={cn(
        "flex items-center gap-2 px-3 py-1.5 text-xs font-semibold uppercase",
        HEADING[group],
      )}
    >
      {title}
      <span>{count}</span>
    </span>
  );
}

export function Sidebar({ current }: { current: number | undefined }): ReactElement {
  const snapshot = useFleet((s) => s.snapshot);
  const stored = useFilter((s) => s.stored);
  const [, setTick] = useState(0);

  // Ages move with the clock, not only with the stream.
  useEffect(() => {
    const t = setInterval(() => setTick((n) => n + 1), 30_000);
    return () => clearInterval(t);
  }, []);

  const listing = useMemo(() => {
    if (snapshot === null) return null;
    return listFleet(snapshot, shownWorkspaces(snapshot), stored);
  }, [snapshot, stored]);

  // The repo a row names: its session's workspace, by its label, else its name.
  const repos = useMemo(
    () => new Map((snapshot?.workspaces ?? []).map((w) => [w.id, w.label || w.name])),
    [snapshot],
  );

  const total =
    listing === null ? 0 : GROUPS.reduce((n, g) => n + listing.grouped[g.key].length, 0);

  const rows = (list: Session[]): ReactElement[] =>
    list.map((s) => (
      <Row key={s.id} session={s} repo={repos.get(s.workspace) ?? ""} current={s.id === current} />
    ));

  return (
    <nav
      data-slot="sidebar"
      aria-label="Sessions"
      className="w-[250px] flex-none overflow-y-auto border-r border-border"
    >
      {listing === null ? null : total === 0 ? (
        <p className="p-3 text-sm text-muted-foreground">
          {listing.active === "" ? "No sessions." : "No sessions in this workspace."}
        </p>
      ) : (
        GROUPS.map(({ key, title }) => {
          const list = listing.grouped[key];
          if (list.length === 0) return null;
          if (key === "paused") {
            return (
              <section key={key} data-slot="sidebar-group" data-group={key}>
                <Collapsible>
                  <CollapsibleTrigger className="flex w-full items-center">
                    <Heading group={key} title={title} count={list.length} />
                  </CollapsibleTrigger>
                  <CollapsibleContent>{rows(list)}</CollapsibleContent>
                </Collapsible>
              </section>
            );
          }
          return (
            <section key={key} data-slot="sidebar-group" data-group={key}>
              <h2>
                <Heading group={key} title={title} count={list.length} />
              </h2>
              {rows(list)}
            </section>
          );
        })
      )}
    </nav>
  );
}
