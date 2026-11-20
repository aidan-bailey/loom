// The info panel's body, a port of the classic Info: the sub-line, the Changes, PR and Account
// tiles, the session's subagents and jobs, its actions and the reply box.
import { ChevronRight, ExternalLink } from "lucide-react";
import { useEffect, useState, type ReactElement, type ReactNode } from "react";

import type { Agent, Job, Session } from "@/api/gen";
import { CommandButton } from "@/app/CommandButton";
import { AGENT_ID, hrefFor } from "@/app/routes";
import { ReplyBox } from "@/conversation/ReplyBox";
import { useMarkSeen } from "@/fleet/seen";
import { isStale, resetPassed, resetText, shareText, shownWindow, usageFor } from "@/fleet/usage";
import { sessionActions } from "@/commands/session";
import { cn } from "@/lib/utils";
import { checksText, parityText } from "@/session/facts";
import { jobGlyph, jobLine, jobTone } from "@/session/jobs";
import { SubLine } from "@/session/SubLine";
import { useFleet } from "@/store/fleet";

const BOX = "min-w-0 rounded-xl border border-border bg-card p-3";
const LEGEND = "truncate text-[11px] tracking-wide text-muted-foreground uppercase";
const SUB = "truncate text-[11px] text-muted-foreground";
const HEADING = "text-xs font-semibold tracking-wide text-muted-foreground uppercase";
const ROW =
  "flex min-h-11 min-w-0 items-center gap-2 rounded-xl border border-border bg-card px-3 py-2";

function Chevron(): ReactElement {
  return <ChevronRight aria-hidden="true" className="size-4 flex-none text-muted-foreground" />;
}

// Tile is one box of the grid: a legend, a figure and sub-lines. A box with an `href` is a link
// (a plain anchor, opened in a new tab when `external`).
function Tile(props: {
  legend: string;
  figure: ReactNode;
  figureClass?: string;
  subs: string[];
  href?: string;
  external?: boolean;
}): ReactElement {
  const { legend, figure, figureClass, subs, href, external } = props;
  const body = (
    <>
      <div className="flex items-center justify-between gap-1">
        <span className={LEGEND}>{legend}</span>
        {href === undefined ? null : external === true ? (
          <ExternalLink aria-hidden="true" className="size-3.5 flex-none text-muted-foreground" />
        ) : (
          <Chevron />
        )}
      </div>
      <div className={cn("text-[17px] font-bold tabular-nums", figureClass)}>{figure}</div>
      {subs.map((text) => (
        <div key={text} className={SUB}>
          {text}
        </div>
      ))}
    </>
  );
  if (href === undefined) return <div className={BOX}>{body}</div>;
  return (
    <a
      href={href}
      className={cn(BOX, "block")}
      {...(external === true ? { target: "_blank", rel: "noopener noreferrer" } : {})}
    >
      {body}
    </a>
  );
}

function Tiles({ session, now }: { session: Session; now: number }): ReactElement | null {
  const usage = useFleet((s) => s.snapshot?.usage) ?? [];
  const staleAfterS = useFleet((s) => s.snapshot?.usage_stale_after_s);
  const tiles: ReactElement[] = [];

  const { added, removed } = session.diff;
  if (added > 0 || removed > 0 || session.parity_known) {
    const parity = parityText(session);
    tiles.push(
      <Tile
        key="changes"
        legend="Changes"
        figure={
          <>
            <span className="text-working">{`+${added}`}</span>{" "}
            <span className="text-error">{`−${removed}`}</span>
          </>
        }
        subs={parity === "" ? [] : [parity]}
        href={
          sessionActions(session).diff ? hrefFor("changes", { id: String(session.id) }) : undefined
        }
      />,
    );
  }

  const pr = session.pr;
  if (pr !== null) {
    const url = pr.url ?? "";
    tiles.push(
      <Tile
        key="pr"
        legend="PR"
        figure={`#${pr.number}`}
        subs={[checksText(pr)]}
        href={url === "" ? undefined : url}
        external
      />,
    );
  }

  const entry = session.claude ? usageFor(usage, session.account) : undefined;
  const shown = entry === undefined ? null : shownWindow(entry);
  if (entry !== undefined && shown !== null) {
    const reset = (w: { resets_at: string; pct: number }): string =>
      resetPassed(w, now) ? "" : resetText(w.resets_at, now);
    const line = (...parts: string[]): string => parts.filter((p) => p !== "").join(" · ");
    const subs = [line(shown.label, reset(shown.w))];
    if (shown.other !== null) {
      subs.push(line(`${shown.otherLabel} ${shareText(shown.other, now)}`, reset(shown.other)));
    }
    tiles.push(
      <Tile
        key="account"
        legend={entry.account}
        figure={shareText(shown.w, now)}
        figureClass={
          isStale(entry.at, staleAfterS, now) ? "text-muted-foreground" : "text-foreground"
        }
        subs={subs}
      />,
    );
  }

  if (tiles.length === 0) return null;
  return <div className="grid grid-cols-[repeat(auto-fill,minmax(96px,1fr))] gap-2">{tiles}</div>;
}

function AgentRow({ session, agent }: { session: Session; agent: Agent }): ReactElement {
  const linked = agent.id !== "" && AGENT_ID.test(agent.id);
  const body = (
    <>
      <span aria-hidden="true" className="flex-none text-muted-foreground">
        {agent.idle ? "○" : "◐"}
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex items-baseline gap-2">
          <span className="min-w-0 flex-1 truncate text-sm font-medium">{agent.name}</span>
          <span className="flex-none text-xs text-muted-foreground">
            {agent.idle ? "idle" : "running"}
          </span>
        </span>
        {agent.description === "" ? null : (
          <span className="block truncate text-xs text-muted-foreground">{agent.description}</span>
        )}
      </span>
      {linked ? <Chevron /> : null}
    </>
  );
  return linked ? (
    <a href={hrefFor("agent", { id: String(session.id), agent: agent.id })} className={ROW}>
      {body}
    </a>
  ) : (
    <div className={ROW}>{body}</div>
  );
}

function Agents({ session }: { session: Session }): ReactElement | null {
  const agents = session.agents ?? [];
  if (agents.length === 0) return null;
  return (
    <section className="flex flex-col gap-2">
      <h2 className={HEADING}>{`Agents ${agents.length}`}</h2>
      <ul className="flex flex-col gap-2">
        {agents.map((a, i) => (
          <li key={`${a.id}:${i}`}>
            <AgentRow session={session} agent={a} />
          </li>
        ))}
      </ul>
    </section>
  );
}

function JobRow({ session, job, now }: { session: Session; job: Job; now: number }): ReactElement {
  const title = job.description === "" ? job.command : job.description;
  return (
    <a href={hrefFor("job", { id: String(session.id), job: String(job.id) })} className={ROW}>
      <span aria-hidden="true" className={cn("flex-none", jobTone(job))}>
        {jobGlyph(job)}
      </span>
      <span className="min-w-0 flex-1">
        <span
          className={cn(
            "block truncate text-sm font-medium",
            job.description === "" && "font-mono",
          )}
        >
          {title}
        </span>
        <span className="block truncate text-xs text-muted-foreground">{jobLine(job, now)}</span>
      </span>
      <Chevron />
    </a>
  );
}

function Jobs({ session, now }: { session: Session; now: number }): ReactElement | null {
  const jobs = session.jobs ?? [];
  if (jobs.length === 0) return null;
  return (
    <section className="flex flex-col gap-2">
      <h2 className={HEADING}>{`Jobs ${jobs.length}`}</h2>
      <ul className="flex flex-col gap-2">
        {jobs.map((j) => (
          <li key={j.id}>
            <JobRow session={session} job={j} now={now} />
          </li>
        ))}
      </ul>
    </section>
  );
}

// clock reads this phone's time; kept apart from the component, which the purity lint holds to
// render the same for the same props.
function clock(): number {
  return Date.now();
}

// Actions is the row of the session's actions `sessionActions` offers, as command buttons: the
// commands own their sheets, in-flight key, stale gate and toasts. No row when none is on offer.
function Actions({ session }: { session: Session }): ReactElement | null {
  const offered = sessionActions(session);
  const buttons: ReactElement[] = [];
  if (offered.pause)
    buttons.push(<CommandButton key="pause" id="session.pause" variant="secondary" />);
  if (offered.resume) buttons.push(<CommandButton key="resume" id="session.resume" />);
  if (offered.push)
    buttons.push(<CommandButton key="push" id="session.push" variant="secondary" />);
  if (offered.merge)
    buttons.push(<CommandButton key="merge" id="session.merge" variant="secondary" />);
  if (offered.kill)
    buttons.push(<CommandButton key="kill" id="session.kill" variant="destructive" />);
  if (buttons.length === 0) return null;
  return <div className="flex gap-2 [&>button]:flex-1">{buttons}</div>;
}

// Info is a session's info view. `now` is read at each render, so a running job's time moves
// whenever the view draws again: a running job redraws it every 5 s.
export function Info({ session }: { session: Session }): ReactElement {
  useMarkSeen(session.id);
  const [, setTick] = useState(0);
  const timing = (session.jobs ?? []).some((j) => j.state === "running");
  useEffect(() => {
    if (!timing) return;
    const timer = setInterval(() => {
      setTick((n) => n + 1);
    }, 5000);
    return () => {
      clearInterval(timer);
    };
  }, [timing]);
  const now = clock();
  return (
    <div className="flex flex-col gap-4 px-4 pb-6">
      <SubLine session={session} />
      <Tiles session={session} now={now} />
      <Agents session={session} />
      <Jobs session={session} now={now} />
      <Actions session={session} />
      <ReplyBox session={session} />
    </div>
  );
}
