import { describe, expect, it } from "vitest";
import type {
  AgentPageState,
  Client,
  FileOmitted,
  FileStatus,
  FlowCell,
  FlowQuestionKind,
  FlowStageName,
  FlowStageState,
  FlowStatus,
  FlowStepKind,
  ItemKind,
  JobControl,
  JobState,
  PrChecks,
  PrState,
  SessionGroup,
  SessionStatus,
  Snapshot,
  StepState,
  TurnFrom,
} from "@/api/gen";
import { fillTimes, fixtureSnapshot } from "./fixtures";
import agentTurns from "../../../../tools/webshot/fixture/agent-turns.json";
import diff from "../../../../tools/webshot/fixture/diff.json";
import flowDoc from "../../../../tools/webshot/fixture/flow-doc.json";
import flow from "../../../../tools/webshot/fixture/flow.json";
import flows from "../../../../tools/webshot/fixture/flows.json";
import jobLogAfter from "../../../../tools/webshot/fixture/job-log-after.json";
import jobLog from "../../../../tools/webshot/fixture/job-log.json";
import launchDefaults from "../../../../tools/webshot/fixture/launch-defaults.json";
import snapshot from "../../../../tools/webshot/fixture/snapshot.json";
import turns from "../../../../tools/webshot/fixture/turns.json";

// A JSON import's strings are `string`, not the union a field holds, so the typed comparison turns
// every string union into `string`; the value checks below hold each union-typed field to its
// union's values.
type Widen<T> = T extends string
  ? string
  : T extends readonly (infer U)[]
    ? Widen<U>[]
    : T extends object
      ? { [K in keyof T]: Widen<T[K]> }
      : T;

// The value a route's generated client function answers with.
type Resp<K extends keyof Client> = Extract<
  Awaited<ReturnType<Client[K]>>,
  { kind: "ok" }
>["value"];

// Each fixture that answers a route, held to that route's generated response type: a Go change
// that breaks a fixture fails the typecheck. A new fixture needs a line here (the next test).
const typed = {
  "snapshot.json": snapshot satisfies Widen<Snapshot>,
  "turns.json": turns satisfies Widen<Resp<"turns">>,
  "agent-turns.json": agentTurns satisfies Widen<Resp<"turns">>,
  "diff.json": diff satisfies Widen<Resp<"diff">>,
  "launch-defaults.json": launchDefaults satisfies Widen<Resp<"launchDefaults">>,
  "job-log.json": jobLog satisfies Widen<Resp<"jobLog">>,
  "job-log-after.json": jobLogAfter satisfies Widen<Resp<"jobLog">>,
  "flow.json": flow satisfies Widen<Resp<"flow">>,
  "flows.json": flows satisfies Widen<Resp<"flows">>,
  "flow-doc.json": flowDoc satisfies Widen<Resp<"flowDoc">>,
};

// Each union's values, as a record the compiler holds to the union both ways: a value missing or
// extra fails the typecheck.
const sessionGroup: Record<SessionGroup, true> = {
  needs_you: true,
  your_turn: true,
  working: true,
  paused: true,
};
const sessionStatus: Record<SessionStatus, true> = {
  Running: true,
  Ready: true,
  Loading: true,
  Paused: true,
  Prompting: true,
  Deleting: true,
  Recoverable: true,
};
const prState: Record<PrState, true> = { draft: true, open: true, merged: true, closed: true };
const prChecks: Record<PrChecks, true> = {
  none: true,
  pending: true,
  passing: true,
  failing: true,
};
const jobState: Record<JobState, true> = {
  held: true,
  queued: true,
  running: true,
  done: true,
  failed: true,
  cancelled: true,
  lost: true,
};
const jobControl: Record<JobControl, true> = { pause: true, resume: true, "": true };
const turnFrom: Record<TurnFrom, true> = {
  you: true,
  plugin: true,
  peer: true,
  task: true,
  brief: true,
};
const itemKind: Record<ItemKind, true> = {
  text: true,
  thinking: true,
  steps: true,
  compacted: true,
  note: true,
};
const stepState: Record<StepState, true> = {
  pending: true,
  done: true,
  failed: true,
  interrupted: true,
};
const agentPageState: Record<AgentPageState, true> = {
  running: true,
  finished: true,
  unknown: true,
};
const fileStatus: Record<FileStatus, true> = { A: true, M: true, D: true, R: true, C: true };
const fileOmitted: Record<FileOmitted, true> = { "": true, lines: true, budget: true };
const flowStageName: Record<FlowStageName, true> = {
  design: true,
  spec: true,
  draft: true,
  run: true,
  record: true,
};
const flowStageState: Record<FlowStageState, true> = {
  done: true,
  now: true,
  todo: true,
  unreadable: true,
};
const flowStatus: Record<FlowStatus, true> = {
  designing: true,
  designed: true,
  drafting: true,
  drafted: true,
  supervised: true,
  running: true,
  parked: true,
  done: true,
  stopped: true,
  cancelled: true,
  lost: true,
  "": true,
};
const flowCell: Record<FlowCell, true> = {
  todo: true,
  running: true,
  passed: true,
  failed: true,
  asks: true,
  skipped: true,
};
const flowStepKind: Record<FlowStepKind, true> = {
  premise: true,
  dev: true,
  gates: true,
  review: true,
  fix: true,
  docs: true,
};
const flowQuestionKind: Record<FlowQuestionKind, true> = {
  "needs-context": true,
  "blocked-decision": true,
  "design-finding": true,
  "premise-design": true,
  "unresolved-findings": true,
  "gate-failure": true,
  "agent-failed": true,
  "tree-moved": true,
  "docs-strayed": true,
  "usage-limit": true,
  lane: true,
  "": true,
};

// collect gathers the values at a path: "/"-separated keys, "*" for every value of an object or
// every element of an array; a hop through null or a missing key gathers nothing.
function collect(root: unknown, path: string): unknown[] {
  let level: unknown[] = [root];
  for (const key of path.split("/")) {
    const next: unknown[] = [];
    for (const value of level) {
      if (value === null || typeof value !== "object") continue;
      if (key === "*") next.push(...Object.values(value as Record<string, unknown>));
      else if (key in value) next.push((value as Record<string, unknown>)[key]);
    }
    level = next;
  }
  return level;
}

const outside = (values: unknown[], union: Record<string, true>) =>
  values.filter((v) => typeof v !== "string" || !Object.keys(union).includes(v));

// Every union-typed field the fixtures carry, with the union it must hold a value of.
const unionFields: { fixture: keyof typeof typed; path: string; union: Record<string, true> }[] = [
  { fixture: "snapshot.json", path: "sessions/*/*/group", union: sessionGroup },
  { fixture: "snapshot.json", path: "sessions/*/*/status", union: sessionStatus },
  { fixture: "snapshot.json", path: "sessions/*/*/pr/state", union: prState },
  { fixture: "snapshot.json", path: "sessions/*/*/pr/checks", union: prChecks },
  { fixture: "snapshot.json", path: "sessions/*/*/jobs/*/state", union: jobState },
  { fixture: "snapshot.json", path: "sessions/*/*/jobs/*/control", union: jobControl },
  { fixture: "snapshot.json", path: "sessions/*/*/flows/*/stage", union: flowStageName },
  { fixture: "snapshot.json", path: "sessions/*/*/flows/*/status", union: flowStatus },
  { fixture: "snapshot.json", path: "sessions/*/*/flows/*/asks", union: flowQuestionKind },
  { fixture: "turns.json", path: "turns/*/from", union: turnFrom },
  { fixture: "turns.json", path: "turns/*/items/*/kind", union: itemKind },
  { fixture: "turns.json", path: "turns/*/items/*/steps/*/state", union: stepState },
  { fixture: "agent-turns.json", path: "turns/*/from", union: turnFrom },
  { fixture: "agent-turns.json", path: "turns/*/items/*/kind", union: itemKind },
  { fixture: "agent-turns.json", path: "turns/*/items/*/steps/*/state", union: stepState },
  { fixture: "agent-turns.json", path: "agent/state", union: agentPageState },
  { fixture: "diff.json", path: "files/*/status", union: fileStatus },
  { fixture: "diff.json", path: "files/*/omitted", union: fileOmitted },
  { fixture: "flow.json", path: "summary/stage", union: flowStageName },
  { fixture: "flow.json", path: "summary/status", union: flowStatus },
  { fixture: "flow.json", path: "summary/asks", union: flowQuestionKind },
  { fixture: "flow.json", path: "stages/*/name", union: flowStageName },
  { fixture: "flow.json", path: "stages/*/state", union: flowStageState },
  { fixture: "flow.json", path: "question/kind", union: flowQuestionKind },
  { fixture: "flow.json", path: "premise/cells/*", union: flowCell },
  { fixture: "flow.json", path: "stories/*/cells/*", union: flowCell },
  { fixture: "flow.json", path: "premise/steps/*/kind", union: flowStepKind },
  { fixture: "flow.json", path: "stories/*/steps/*/kind", union: flowStepKind },
  { fixture: "flows.json", path: "live/*/flow/stage", union: flowStageName },
  { fixture: "flows.json", path: "live/*/flow/status", union: flowStatus },
  { fixture: "flows.json", path: "live/*/flow/asks", union: flowQuestionKind },
];

describe("the typed fixtures", () => {
  it("covers every JSON file in the fixture directory", () => {
    const files = import.meta.glob("../../../../tools/webshot/fixture/*.json", { eager: true });
    const names = Object.keys(files).map((path) => path.slice(path.lastIndexOf("/") + 1));
    expect(names.sort()).toEqual(Object.keys(typed).sort());
  });

  it.each(unionFields)("$fixture holds only values of its union at $path", (field) => {
    const values = collect(typed[field.fixture], field.path);
    expect(values.length).toBeGreaterThan(0);
    expect(outside(values, field.union)).toEqual([]);
  });

  it("finds a value outside a union", () => {
    expect(outside(["working", "mystery", 3], sessionGroup)).toEqual(["mystery", 3]);
  });

  it("collects through a null and past a missing key without error", () => {
    expect(collect({ a: [{ b: null }, { c: 1 }, null] }, "a/*/b")).toEqual([null]);
  });
});

describe("fillTimes", () => {
  const now = Date.parse("2026-11-18T12:00:00Z");

  it("fills now, and now plus or minus a duration, as RFC 3339", () => {
    expect(fillTimes("now", now)).toBe("2026-11-18T12:00:00Z");
    expect(fillTimes("now-2m10s", now)).toBe("2026-11-18T11:57:50Z");
    expect(fillTimes("now+1h40m", now)).toBe("2026-11-18T13:40:00Z");
    expect(fillTimes("now-72h", now)).toBe("2026-11-15T12:00:00Z");
  });

  it("fills inside objects and arrays and leaves other values alone", () => {
    const value = { a: ["now-1m", "nowhere", 3, null, true], b: { c: "now-30s" } };
    expect(fillTimes(value, now)).toEqual({
      a: ["2026-11-18T11:59:00Z", "nowhere", 3, null, true],
      b: { c: "2026-11-18T11:59:30Z" },
    });
  });

  it("refuses a duration it cannot read", () => {
    expect(() => fillTimes("now-3x", now)).toThrow("bad duration");
  });
});

describe("fixtureSnapshot", () => {
  const now = Date.parse("2026-11-18T12:00:00Z");

  it("fills every relative time of the canned snapshot", () => {
    const filled = fixtureSnapshot(now);
    expect(JSON.stringify(filled)).not.toMatch(/"now[-+"]/);
    const session = Object.values(filled.sessions ?? {})
      .flatMap((list) => list ?? [])
      .find((s) => s.id === 1);
    expect(session?.since).toBe("2026-11-18T11:58:00Z");
  });

  it("is a copy, so a test may change it", () => {
    const first = fixtureSnapshot(now);
    first.host = "changed";
    expect(fixtureSnapshot(now).host).toBe("fresco");
  });
});
