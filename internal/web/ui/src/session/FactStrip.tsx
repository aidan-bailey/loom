import type { ReactElement, ReactNode } from "react";

import type { PanelProps } from "@/app/panels";
import { hrefFor, useRoute } from "@/app/routes";
import { rowShare } from "@/fleet/usage";
import { ago, PrMark } from "@/panels/fleet/SessionRow";
import { useFleet, useSession } from "@/store/fleet";

import { groupGlyph, groupTone } from "./facts";

// FactStrip is the line under a Claude session's tabs on its conversation: its status, changes, PR
// and account share, one link to the info tab; a session's jobs badge is a link of its own. A port
// of the classic strip.
export function FactStrip(props: PanelProps): ReactElement {
  const session = useSession(props.sessionId);
  const route = useRoute();
  const snapshot = useFleet((s) => s.snapshot);
  if (route.panel !== "turns" || session === undefined || !session.claude) return <></>;

  const facts: ReactNode[] = [];
  const since = Date.parse(session.since) ? ago(session.since) : "";
  facts.push(
    <span key="status">
      <span aria-hidden="true" className={groupTone(session.group)}>
        {groupGlyph(session.group)}
      </span>{" "}
      {session.label.toLowerCase()}
      {since === "" ? "" : ` ${since}`}
    </span>,
  );
  const { added, removed } = session.diff;
  if (added > 0 || removed > 0) {
    facts.push(
      <span key="diff">
        <span className="text-working">+{added}</span>{" "}
        <span className="text-error">−{removed}</span>
      </span>,
    );
  }
  if (session.pr !== null) {
    facts.push(
      <span key="pr">
        PR #{session.pr.number} <PrMark pr={session.pr} />
      </span>,
    );
  }
  const share = rowShare(session, snapshot?.usage ?? [], snapshot?.usage_stale_after_s);
  if (share !== null) {
    facts.push(
      <span key="share" className={share.amber ? "text-attention" : undefined}>
        {share.text}
      </span>,
    );
  }

  const id = String(session.id);
  const jobsHref =
    session.jobs_focus > 0
      ? hrefFor("job", { id, job: String(session.jobs_focus) })
      : hrefFor("info", { id });
  return (
    <div className="flex min-h-11 items-center px-4 text-xs text-muted-foreground">
      <a href={hrefFor("info", { id })} className="min-w-0 flex-1 truncate">
        {facts.map((fact, i) => (
          <span key={i}>
            {i === 0 ? "" : " · "}
            {fact}
          </span>
        ))}
      </a>
      {session.jobs_label === "" ? null : (
        <a href={jobsHref} className="flex-none pl-3 font-mono">
          {session.jobs_label}
        </a>
      )}
    </div>
  );
}
