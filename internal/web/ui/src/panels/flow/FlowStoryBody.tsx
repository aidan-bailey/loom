// The flow story view (a chunk of its own): one row of the board over useFlow, a port of the
// classic story view. It draws the gone, empty, loading and error states, the way back to the
// board, the row's name and facts, its steps, its findings and what to read.
import type { ReactElement } from "react";

import type { Flow, FlowGate, FlowStep, FlowStory } from "@/api/gen";
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

type Outcome = "running" | "passed" | "failed" | "todo" | "unknown";

// stepOutcome sorts a step's status into the outcome its dot shows; a status this build does not
// know is unknown, and its raw text stays on the line.
function stepOutcome(step: FlowStep): Outcome {
  switch (step.status) {
    case "running":
    case "starting":
      return "running";
    case "DONE":
    case "DONE_WITH_CONCERNS":
    case "passed":
      return "passed";
    case "":
      return "todo";
    case "failed":
    case "BLOCKED":
    case "NEEDS_CONTEXT":
    case "cancelled":
    case "infra":
      return "failed";
    default:
      return "unknown";
  }
}

// Each outcome's whole dot class string.
const DOT: Record<Outcome, string> = {
  passed: "bg-working",
  running: "bg-turn",
  failed: "bg-error",
  todo: "bg-faint",
  unknown: "bg-muted-foreground",
};

// A gate's verdict: PASS is good, any other verdict is a failure, and none yet is neutral.
function verdictClass(verdict: string): string {
  if (verdict === "") return "text-muted-foreground";
  return verdict === "PASS" ? "text-working" : "text-error";
}

// specAts are the spec sections a reading names, each once in order of first appearance, as
// "§4" or "§3.2".
function specAts(reading: string): string[] {
  const found = (reading.match(/§\s*[\d.]+/g) ?? []).map((at) => at.replace(/\s+/g, ""));
  return found.filter((at, i) => found.indexOf(at) === i);
}

const BOX = "rounded-xl border border-border bg-card p-3";
const LEGEND = "text-[11px] tracking-wide text-muted-foreground uppercase";
const NOTE = "px-4 py-8 text-center text-muted-foreground";

// Gate is one gate line of a step: its verdict and text, a link to its job when it has one.
function Gate({ gate, id }: { gate: FlowGate; id: string }): ReactElement {
  const body = (
    <>
      <span className={verdictClass(gate.verdict)}>{gate.verdict !== "" ? gate.verdict : "…"}</span>{" "}
      <span>{gate.text}</span>
    </>
  );
  const className = "flex min-h-11 items-center text-xs";
  return gate.job !== 0 ? (
    <a data-slot="gate" href={hrefFor("job", { id, job: String(gate.job) })} className={className}>
      {body}
    </a>
  ) : (
    <div data-slot="gate" className={className}>
      {body}
    </div>
  );
}

// StepItem is one step: its outcome dot, its head, its unreadable and waiting lines and its gates.
function StepItem({
  step,
  flow,
  id,
  plan,
  now,
}: {
  step: FlowStep;
  flow: Flow;
  id: string;
  plan: string;
  now: number;
}): ReactElement {
  const outcome = stepOutcome(step);
  const spent = usd(flow.costs.by_agent?.[step.name]);
  const time =
    step.started === ""
      ? ""
      : elapsed(Date.parse(step.started), step.ended !== "" ? Date.parse(step.ended) : now);
  const side = [time, spent].filter((part) => part !== "").join(" ");
  const head = (
    <>
      <span className="min-w-0 flex-1">
        {`${step.kind}${step.k}`}
        <span className="text-muted-foreground">
          {` ${step.role !== "" ? step.role : "gates"} · ${step.status !== "" ? step.status : "unreadable"}`}
        </span>
      </span>
      {side !== "" && <span className="flex-none text-xs text-muted-foreground">{side}</span>}
    </>
  );
  const headClass = "flex min-h-11 min-w-0 flex-1 items-center gap-2 text-sm";
  return (
    <li
      data-slot="step"
      data-outcome={outcome}
      className="flex flex-col border-b border-border last:border-b-0"
    >
      <div className="flex items-center gap-2">
        <span aria-hidden="true" className={`size-2 flex-none rounded-full ${DOT[outcome]}`} />
        {step.report ? (
          <a
            href={hrefFor("flowDoc", { id, doc: `report:${step.name}` }, { plan })}
            className={headClass}
          >
            {head}
          </a>
        ) : (
          <div className={headClass}>{head}</div>
        )}
      </div>
      {step.unreadable !== "" && <p className="text-xs text-error">{step.unreadable}</p>}
      {step.waiting !== "" && <p className="text-xs text-muted-foreground">{step.waiting}</p>}
      {(step.gates ?? []).map((gate, i) => (
        <Gate key={i} gate={gate} id={id} />
      ))}
    </li>
  );
}

// Steps is the steps box.
function Steps({
  row,
  flow,
  id,
  plan,
  now,
}: {
  row: FlowStory;
  flow: Flow;
  id: string;
  plan: string;
  now: number;
}): ReactElement {
  const steps = row.steps ?? [];
  return (
    <div className={BOX}>
      <p className={LEGEND}>steps</p>
      {steps.length === 0 ? (
        <p className="text-sm text-faint">no step yet</p>
      ) : (
        <ol>
          {steps.map((step) => (
            <StepItem key={step.name} step={step} flow={flow} id={id} plan={plan} now={now} />
          ))}
        </ol>
      )}
    </div>
  );
}

// Findings is the findings box: the row's counts, and the kept lines of the final row.
function Findings({ row, flow }: { row: FlowStory; flow: Flow }): ReactElement {
  const [critical, important, minor] = row.findings;
  const counted = row.findings.length >= 3;
  const quiet = row.findings.every((n) => n === 0);
  return (
    <div className={BOX}>
      <p className={LEGEND}>findings</p>
      {counted ? (
        <p className={`text-sm ${quiet ? "text-muted-foreground" : "text-foreground"}`}>
          {`Critical ${critical} · Important ${important} · Minor ${minor}`}
        </p>
      ) : (
        <p className="text-sm text-muted-foreground">none yet</p>
      )}
      {row.id === "F" &&
        (flow.kept ?? []).map((line, i) => (
          <p key={i} className="text-xs text-faint">
            {line}
          </p>
        ))}
    </div>
  );
}

// Reads is the reads box: the plan's lines, each spec section the reading names and the changes.
function Reads({ row, id, plan }: { row: FlowStory; id: string; plan: string }): ReactElement {
  const link = "flex min-h-11 items-center text-turn";
  return (
    <div className={BOX}>
      <p className={LEGEND}>reads</p>
      <a
        href={hrefFor(
          "flowDoc",
          { id, doc: "plan" },
          { plan, at: row.line === 0 ? "" : String(row.line) },
        )}
        className={link}
      >
        {`plan · Story ${row.id}${row.line !== 0 ? ` (L${row.line}–${row.end})` : ""}`}
      </a>
      {specAts(row.reading).map((at) => (
        <a key={at} href={hrefFor("flowDoc", { id, doc: "spec" }, { plan, at })} className={link}>
          {`spec ${at}`}
        </a>
      ))}
      {row.commits !== 0 && (
        <a href={hrefFor("changes", { id })} className={link}>
          {`${row.commits} commits → changes`}
        </a>
      )}
    </div>
  );
}

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
      <Steps row={row} flow={flow} id={String(session.id)} plan={plan} now={now} />
      {row.id !== "P" && <Findings row={row} flow={flow} />}
      {row.id !== "P" && row.id !== "F" && <Reads row={row} id={String(session.id)} plan={plan} />}
    </section>
  );
}
