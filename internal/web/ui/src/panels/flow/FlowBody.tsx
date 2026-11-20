// The flow board (a chunk of its own): a port of the classic board over useFlow. It draws the
// gone, empty, loading and error states, the picker, the run's header lines and, for a held
// board, the boxes the next tasks add. A value this build does not know draws its raw text.
import { useId, type ReactElement } from "react";

import type { Flow, FlowStage, FlowStory, Session } from "@/api/gen";
import { flowKey, useFlow } from "@/api/reads";
import type { PanelProps } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { elapsed } from "@/session/jobs";
import { NoSession } from "@/session/NoSession";
import { useSession } from "@/store/fleet";

import { planOf, usd, useClock } from "./parts";

const STAGES = ["design", "spec", "draft", "run", "record"] as const;

// Each stage state's whole class string; a state this build does not know takes NEUTRAL_STAGE
// and draws its raw text.
const STAGE_TONE: Record<string, string> = {
  done: "text-working",
  now: "bg-secondary text-foreground",
  todo: "text-faint",
  unreadable: "text-error",
};
const NEUTRAL_STAGE = "text-muted-foreground";

// KINDS are the cells a row draws, by its id: the premise row, the final row, a story.
const KINDS: Record<"story" | "P" | "F", string[]> = {
  story: ["dev", "gates", "review", "fix", "docs"],
  P: ["premise"],
  F: ["gates", "review", "fix"],
};
const LABEL: Record<string, string> = {
  dev: "dev",
  gates: "gate",
  review: "rev",
  fix: "fix",
  docs: "doc",
  premise: "prem",
};

const CELL_TONE: Record<string, string> = {
  running: "bg-secondary text-foreground",
  passed: "bg-diff-add text-working",
  failed: "bg-diff-del text-error",
  asks: "bg-attention/15 text-attention",
  skipped: "text-faint",
  todo: "bg-secondary text-faint",
};
const NEUTRAL_CELL = "bg-secondary text-muted-foreground";

const BOX = "rounded-xl border border-border bg-card p-3";
const LEGEND = "text-[11px] tracking-wide text-muted-foreground uppercase";

// Strip is the five stages, each with its document when it has one.
function Strip({ flow, id, plan }: { flow: Flow; id: string; plan: string }): ReactElement {
  return (
    <nav aria-label="Stages" className="flex gap-0.5">
      {STAGES.map((name) => {
        const stage: FlowStage = (flow.stages ?? []).find((s) => s.name === name) ?? {
          name,
          state: "todo",
          detail: "",
          doc: "",
        };
        const known = STAGE_TONE[stage.state];
        let word: string = name;
        if (name === "run" && flow.summary.run !== "") {
          word = `run ${flow.summary.done}/${flow.summary.total}`;
        }
        if (stage.state === "done") word += " ✓";
        if (known === undefined) word += ` (${stage.state})`;
        const className = `flex min-h-11 min-w-0 flex-1 items-center justify-center px-1 text-center text-[11px] ${known ?? NEUTRAL_STAGE}`;
        const text = <span className="truncate">{word}</span>;
        return stage.doc !== "" ? (
          <a
            key={name}
            href={hrefFor("flowDoc", { id, doc: stage.doc }, { plan })}
            title={stage.detail}
            className={className}
          >
            {text}
          </a>
        ) : (
          <span key={name} title={stage.detail} className={className}>
            {text}
          </span>
        );
      })}
    </nav>
  );
}

// Question is the parked question: its kind and story, its text and the way to the manager.
function Question({ flow, id }: { flow: Flow; id: string }): ReactElement | null {
  const question = flow.question;
  if (question === null) return null;
  return (
    <div className={`${BOX} border-attention/40`}>
      <p className={LEGEND}>
        {`parked · ${question.kind}${question.story !== "" ? ` · story ${question.story}` : ""}`}
      </p>
      <p className="mt-1 break-words whitespace-pre-wrap">{question.text}</p>
      <a
        href={hrefFor("turns", { id })}
        className="mt-2 inline-flex min-h-11 items-center rounded-lg bg-secondary px-3 text-attention"
      >
        open manager ›
      </a>
    </div>
  );
}

// Cells are a row's steps, one per kind its id draws.
function Cells({ story }: { story: FlowStory }): ReactElement {
  const kinds = story.id === "P" ? KINDS.P : story.id === "F" ? KINDS.F : KINDS.story;
  return (
    <>
      {kinds.map((kind) => {
        const state: string = story.cells?.[kind] ?? "todo";
        const known = CELL_TONE[state];
        let text = LABEL[kind] ?? kind;
        if (known === undefined) text = state;
        else if (state === "asks") text += "?";
        else if (state === "skipped") text = "—";
        return (
          <span
            key={kind}
            aria-label={`${kind}: ${state}`}
            className={`min-w-[34px] rounded-sm px-1 text-center text-[10px] leading-[18px] ${known ?? NEUTRAL_CELL}`}
          >
            {text}
          </span>
        );
      })}
    </>
  );
}

// sideText is a row's right-hand text: what it holds by id, what a started story has cost, or
// what a story to come is planned as.
function sideText(story: FlowStory, flow: Flow): string {
  if (story.id === "P") return "premises";
  if (story.id === "F") return "final";
  if ((story.steps ?? []).length > 0) {
    const spend = flow.costs.by_story?.[story.id];
    const parts = [
      story.commits !== 0 ? `${story.commits}c` : "",
      typeof spend === "number" ? usd(spend) : "",
    ];
    return parts.filter((part) => part !== "").join(" · ");
  }
  return [story.tier, story.review].filter((part) => part !== "").join(" · ");
}

// StoryRow is one row of the stories box: its id, its cells and its right-hand text.
function StoryRow({
  story,
  flow,
  id,
  plan,
}: {
  story: FlowStory;
  flow: Flow;
  id: string;
  plan: string;
}): ReactElement {
  const started = (story.steps ?? []).length > 0;
  const mark = story.id === "P" || story.id === "F";
  return (
    <a
      href={hrefFor("flowStory", { id, story: story.id }, { plan })}
      className="flex min-h-11 items-center gap-2 border-b border-border last:border-b-0"
    >
      <span className={`w-6 flex-none font-bold ${mark ? "text-faint" : ""}`}>{story.id}</span>
      <Cells story={story} />
      <span
        className={`ml-auto min-w-0 truncate text-right text-xs ${started ? "text-muted-foreground" : "text-faint"}`}
      >
        {sideText(story, flow)}
      </span>
    </a>
  );
}

// Stories is the stories box: the premise row, the stories and the final row.
function Stories({ flow, id, plan }: { flow: Flow; id: string; plan: string }): ReactElement {
  const rows = [
    ...(flow.premise !== null ? [flow.premise] : []),
    ...(flow.stories ?? []),
    ...(flow.final !== null ? [flow.final] : []),
  ];
  return (
    <div className={BOX}>
      <p className={LEGEND}>stories</p>
      {rows.map((story) => (
        <StoryRow key={story.id} story={story} flow={flow} id={id} plan={plan} />
      ))}
    </div>
  );
}

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
  const id = String(session.id);
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
      <Strip flow={flow} id={id} plan={plan} />
      <Lines flow={flow} now={now} />
      <Question flow={flow} id={id} />
      <Stories flow={flow} id={id} plan={plan} />
    </section>
  );
}
