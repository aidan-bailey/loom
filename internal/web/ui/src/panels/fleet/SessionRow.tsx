// One session of the fleet (direction B): its group's dot, title, account share and age; why it
// waits, or what it said last; and what it holds. A port of the classic fleet's Row.
import type { ReactElement, ReactNode } from "react";

import type { FlowSummary, Pr, Session, Usage } from "@/api/gen";
import { Badge } from "@/components/ui/badge";
import { GROUPS } from "@/fleet/groups";
import { sessionHref } from "@/fleet/links";
import { rowShare } from "@/fleet/usage";
import { cn } from "@/lib/utils";
import { useFleet } from "@/store/fleet";

/**
 * A time as the fleet words it: "now" under a minute, "<n>m" under an hour, "<n>h" under 48 hours,
 * else "<n>d"; "" for a time that does not parse. Exported for the tests.
 */
export function ago(iso: string, now: number = Date.now()): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const secs = Math.max(0, Math.round((now - t) / 1000));
  if (secs < 60) return "now";
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 48) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

export const usd = (v: number): string => `$${v > 0 && v < 0.01 ? v.toFixed(4) : v.toFixed(2)}`;

/**
 * A flow's fleet chip: "flow 2/5 · reviewing · $4.71" during a run, "asks" while parked, "lost"
 * when its engine has ended; "flow · design" and "flow · draft 3/4" before a run. Exported for the
 * tests.
 */
export function flowChip(flow: FlowSummary): string {
  if (flow.stage === "design" || flow.stage === "spec") return "flow · design";
  if (flow.status === "drafting") return `flow · draft ${flow.drafted}/${flow.total}`;
  if (flow.status === "drafted" || flow.status === "designed") return `flow · ${flow.status}`;
  const parts = [`flow ${flow.done}/${flow.total}`];
  const state =
    flow.asks !== ""
      ? "asks"
      : flow.status === "lost"
        ? "lost"
        : flow.status === "running" && flow.phase !== ""
          ? flow.phase
          : flow.status;
  if (state !== "") parts.push(state);
  if (typeof flow.spent === "number") parts.push(usd(flow.spent));
  return parts.join(" · ");
}

function flowTone(flow: FlowSummary): "docs" | "attention" | "error" {
  if (flow.asks !== "") return "attention";
  if (flow.status === "lost" || flow.unreadable !== "") return "error";
  return "docs";
}

const DOT: Record<string, string> = {
  needs_you: "bg-attention",
  your_turn: "bg-turn",
  working: "bg-working",
};

function runningAgents(session: Session): number {
  return (session.agents ?? []).filter((a) => !a.idle).length;
}

// A PR's state: ✓, ✗ or … for an open PR's checks, else the state's own text.
export function PrMark({ pr }: { pr: Pr }): ReactElement | null {
  if (pr.state !== "open") return <span>{pr.state}</span>;
  switch (pr.checks) {
    case "passing":
      return <span className="text-working">✓</span>;
    case "failing":
      return <span className="text-error">✗</span>;
    case "pending":
      return <span>…</span>;
    default:
      return null;
  }
}

export function SessionRow({
  session,
  usage,
  chip,
  fresh = false,
}: {
  session: Session;
  usage: Usage[];
  chip?: string;
  fresh?: boolean;
}): ReactElement {
  const staleAfterS = useFleet((s) => s.snapshot?.usage_stale_after_s);
  const waiting = session.group === "needs_you";
  const share = rowShare(session, usage, staleAfterS);
  const groupTitle = GROUPS.find((g) => g.key === session.group)?.title;
  const agents = runningAgents(session);
  const { added, removed } = session.diff;
  const flows = session.flows ?? [];

  const meta: ReactNode[] = [];
  if (session.label !== "" && session.label !== groupTitle) {
    meta.push(<Badge key="label">{session.label}</Badge>);
  }
  if (agents > 0) {
    meta.push(<span key="agents">{agents === 1 ? "1 agent" : `${agents} agents`}</span>);
  }
  if (added !== 0 || removed !== 0) {
    meta.push(
      <span key="diff" className="font-mono">
        <span className="text-working">+{added}</span>{" "}
        <span className="text-error">−{removed}</span>
      </span>,
    );
  }
  if (session.pr !== null) {
    meta.push(
      <span key="pr" className="inline-flex gap-1">
        <span>PR #{session.pr.number}</span>
        <PrMark pr={session.pr} />
      </span>,
    );
  }
  flows.forEach((flow, i) => {
    meta.push(
      <Badge key={`flow${i}`} variant={flowTone(flow)} data-tone={flowTone(flow)}>
        {flowChip(flow)}
      </Badge>,
    );
  });
  if (session.jobs_label !== "") {
    meta.push(
      <Badge key="jobs" className="font-mono">
        {session.jobs_label}
      </Badge>,
    );
  }
  if (!waiting && chip) {
    meta.push(
      <Badge key="chip" variant="workspace">
        {chip}
      </Badge>,
    );
  }

  const reason = session.wait.reason;
  const detail = session.wait.detail;

  return (
    <a
      href={sessionHref(session)}
      className="block min-h-11 border-b border-border px-3.5 py-2.5 last:border-b-0"
    >
      <div className="flex items-center gap-2">
        <span
          aria-hidden="true"
          className={cn(
            "size-2 flex-none rounded-full",
            DOT[session.group] ?? "bg-muted-foreground",
          )}
        />
        <span className="min-w-0 flex-1 truncate text-sm font-medium">{session.title}</span>
        {fresh ? (
          <span className="flex-none rounded-sm bg-turn px-1 text-[10px] font-bold text-background">
            NEW
          </span>
        ) : null}
        {share === null ? null : (
          <span
            data-amber={share.amber ? "true" : undefined}
            className={cn(
              "flex-none font-mono text-xs text-muted-foreground",
              share.amber && "text-attention",
            )}
          >
            {share.text}
          </span>
        )}
        <span className="flex-none text-xs text-muted-foreground">{ago(session.since)}</span>
      </div>
      {waiting && (reason !== "" || chip) ? (
        <div className="mt-1 flex flex-wrap items-center gap-1.5">
          {reason === "" ? null : <Badge variant="attention">{reason}</Badge>}
          {chip ? <Badge variant="workspace">{chip}</Badge> : null}
        </div>
      ) : null}
      {waiting && detail !== "" ? (
        <div className="mt-1 truncate font-mono text-xs text-muted-foreground">{detail}</div>
      ) : null}
      {!waiting && session.last_message !== "" ? (
        <div className="mt-1 truncate text-xs text-muted-foreground">{session.last_message}</div>
      ) : null}
      {meta.length > 0 ? (
        <div className="mt-1.5 flex flex-wrap items-center gap-x-2.5 gap-y-1 text-xs text-muted-foreground">
          {meta}
        </div>
      ) : null}
    </a>
  );
}
