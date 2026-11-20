// The flow board (a chunk of its own): a port of the classic board over useFlow. It draws the
// gone, empty, loading and error states, the picker, the run's header lines and, for a held
// board, the boxes the next tasks add. A value this build does not know draws its raw text.
import { useId, type ReactElement } from "react";

import type { Flow, Session } from "@/api/gen";
import { flowKey, useFlow } from "@/api/reads";
import type { PanelProps } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { elapsed } from "@/session/jobs";
import { NoSession } from "@/session/NoSession";
import { useSession } from "@/store/fleet";

import { planOf, useClock } from "./parts";

// Picker chooses among a session's flows; the board's plan is the one selected.
function Picker({ session, plan }: { session: Session; plan: string }): ReactElement | null {
  const id = useId();
  const flows = session.flows ?? [];
  if (flows.length < 2) return null;
  return (
    <div className="flex flex-col gap-1">
      <label htmlFor={id} className="text-xs text-muted-foreground">
        flow
      </label>
      <select
        id={id}
        value={plan}
        onChange={(e) => {
          window.location.assign(
            hrefFor("flow", { id: String(session.id) }, { plan: e.target.value }),
          );
        }}
        className="h-11 w-full rounded-lg border border-input bg-background px-3 text-[16px] text-foreground"
      >
        {flows.map((f) => (
          <option key={f.plan} value={f.plan}>
            {f.title !== "" ? f.title : f.plan}
          </option>
        ))}
      </select>
    </div>
  );
}

// Header is the run's lines above the stage strip: the title, the read's error and the
// unreadable line.
function Header({ flow, error }: { flow: Flow; error: string }): ReactElement {
  const { summary } = flow;
  return (
    <>
      <p className="font-semibold break-words">
        {summary.title !== "" ? summary.title : summary.plan}
        {summary.part !== "" && <span className="text-muted-foreground"> {summary.part}</span>}
      </p>
      {error !== "" && (
        <p role="alert" className="text-xs text-error">
          {error}
        </p>
      )}
      {summary.unreadable !== "" && (
        <p className="text-xs text-error">unreadable: {summary.unreadable}</p>
      )}
    </>
  );
}

// Lines are the lost and phase lines, which sit below the stage strip.
function Lines({ flow, now }: { flow: Flow; now: number }): ReactElement {
  const { summary } = flow;
  const end = Date.parse(flow.end);
  const since = elapsed(Date.parse(flow.start), Number.isNaN(end) ? now : end);
  return (
    <>
      {summary.status === "lost" && (
        <p className="text-xs text-error">
          The run's engine has ended without it: loom flow run resume {summary.run}
        </p>
      )}
      {summary.phase !== "" && flow.question === null && (
        <p className="text-xs text-muted-foreground">
          {`${summary.status} · ${summary.phase}${since !== "" ? ` · ${since}` : ""}`}
        </p>
      )}
    </>
  );
}

export default function FlowBody(props: PanelProps): ReactElement {
  const session = useSession(props.sessionId);
  const now = useClock(30_000);
  const plan = session === undefined ? "" : planOf(session, props.search.plan ?? "");
  const key = session === undefined ? "" : flowKey(session);
  const { flow, error } = useFlow({ session: props.sessionId ?? 0, plan, key });

  if (session === undefined) return <NoSession />;
  if (plan === "") {
    return (
      <p role="status" className="px-4 py-8 text-center text-muted-foreground">
        No flow in this session's worktree.
      </p>
    );
  }
  if (flow === undefined && error === "") {
    return (
      <p role="status" className="px-4 py-8 text-center text-muted-foreground">
        Loading…
      </p>
    );
  }
  if (flow === undefined) {
    return (
      <p role="alert" className="px-4 py-8 text-center text-error">
        {error}
      </p>
    );
  }
  return (
    <section className="flex flex-col gap-4 px-4 pb-6">
      <Picker session={session} plan={plan} />
      <Header flow={flow} error={error} />
      <Lines flow={flow} now={now} />
    </section>
  );
}
