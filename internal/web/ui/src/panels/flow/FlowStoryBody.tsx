// The flow story view (a chunk of its own): one row of the board over useFlow, a port of the
// classic story view. It draws the gone, empty, loading and error states, the way back to the
// board, the row's name and facts, and the boxes the next task adds.
import type { ReactElement } from "react";

import type { Flow, FlowStep, FlowStory } from "@/api/gen";
import { flowKey, useFlow } from "@/api/reads";
import type { PanelProps } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { elapsed } from "@/session/jobs";
import { NoSession } from "@/session/NoSession";
import { useSession } from "@/store/fleet";

import { planOf, usd, useClock } from "./parts";

// rowOf is the board's row with the id, or undefined when it holds none.
function rowOf(flow: Flow, id: string): FlowStory | undefined {
  if (id === "P") return flow.premise ?? undefined;
  if (id === "F") return flow.final ?? undefined;
  return (flow.stories ?? []).find((story) => story?.id === id);
}

function isRunning(step: FlowStep): boolean {
  return step.status === "running" || step.status === "starting";
}

// took is how long the row's steps have taken: from the first that started to the last end, or
// to now while a step runs or none has ended.
function took(steps: FlowStep[], now: number): string {
  const first = steps.find((step) => step.started !== "");
  if (first === undefined) return "";
  const last = steps.findLast((step) => step.ended !== "");
  const to = steps.some(isRunning) || last === undefined ? now : Date.parse(last.ended);
  return elapsed(Date.parse(first.started), to);
}

// factsOf is the row's facts line: who, how it is checked, its commits, its time and its cost.
function factsOf(row: FlowStory, flow: Flow, now: number): string {
  const plain = row.id === "P" || row.id === "F";
  const checked = plain ? [] : [row.tier, row.review].filter((part) => part !== "");
  return [
    row.developer,
    checked.join(" · "),
    row.commits !== 0 ? `${row.commits} commits` : "",
    took(row.steps ?? [], now),
    usd(flow.costs.by_story?.[row.id]),
  ]
    .filter((part) => part !== "")
    .join(" · ");
}

function nameOf(id: string): string {
  if (id === "P") return "Premises";
  if (id === "F") return "Final round";
  return `Story ${id}`;
}

const NOTE = "px-4 py-8 text-center text-muted-foreground";

export default function FlowStoryBody(props: PanelProps): ReactElement {
  const session = useSession(props.sessionId);
  const now = useClock(30_000);
  const plan = session !== undefined ? planOf(session, props.search.plan ?? "") : "";
  const key = session !== undefined ? flowKey(session) : "";
  const { flow, error } = useFlow({ session: props.sessionId ?? 0, plan, key });
  const id = props.params.story ?? "";

  if (session === undefined) return <NoSession />;
  if (plan === "") {
    return (
      <p role="status" className={NOTE}>
        No flow in this session's worktree.
      </p>
    );
  }
  if (flow === undefined) {
    return error === "" ? (
      <p role="status" className={NOTE}>
        Loading…
      </p>
    ) : (
      <p role="alert" className="px-4 py-3 text-error">
        {error}
      </p>
    );
  }
  const row = rowOf(flow, id);
  if (row === undefined) {
    return (
      <p role="status" className={NOTE}>
        {`The board has no row ${id}.`}
      </p>
    );
  }
  const facts = factsOf(row, flow, now);
  return (
    <section className="flex flex-col gap-4 px-4 pb-6">
      <p className="flex items-center gap-2">
        <a
          href={hrefFor("flow", { id: String(session.id) }, { plan })}
          className="flex min-h-11 items-center text-muted-foreground"
        >
          ‹ board
        </a>
        <span className="font-semibold">{nameOf(row.id)}</span>
      </p>
      {error !== "" && (
        <p role="alert" className="text-xs text-error">
          {error}
        </p>
      )}
      {facts !== "" && (
        <p data-slot="facts" className="text-sm text-muted-foreground">
          {facts}
        </p>
      )}
    </section>
  );
}
