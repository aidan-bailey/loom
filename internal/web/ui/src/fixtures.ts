// The canned loom's fixtures (tools/webshot/fixture: the snapshot, the turns, the diff, the job
// log, a flow's board and document, the host's flows and the launch defaults) for the tests: their
// relative times ("now-2m", "now+1h40m"; a bare "now" is not one) filled as tools/webshot's
// fillTimes fills them, so the same fixtures feed Vitest and the screenshots.
import type {
  Flow,
  FlowDoc,
  Flows,
  JobLog,
  LaunchDefaults,
  Patch,
  Snapshot,
  Turns,
} from "@/api/gen";
import agentTurnsJson from "../../../../tools/webshot/fixture/agent-turns.json";
import diffJson from "../../../../tools/webshot/fixture/diff.json";
import flowDocJson from "../../../../tools/webshot/fixture/flow-doc.json";
import flowJson from "../../../../tools/webshot/fixture/flow.json";
import flowsJson from "../../../../tools/webshot/fixture/flows.json";
import jobLogAfterJson from "../../../../tools/webshot/fixture/job-log-after.json";
import jobLogJson from "../../../../tools/webshot/fixture/job-log.json";
import launchDefaultsJson from "../../../../tools/webshot/fixture/launch-defaults.json";
import snapshotJson from "../../../../tools/webshot/fixture/snapshot.json";
import turnsJson from "../../../../tools/webshot/fixture/turns.json";

const RELATIVE = /^now([+-])(\S+)$/;
const DURATION = /^(?:\d+(?:\.\d+)?(?:ms|h|m|s))+$/;
const PART = /(\d+(?:\.\d+)?)(ms|h|m|s)/g;
const UNIT_MS: Record<string, number> = { ms: 1, s: 1000, m: 60_000, h: 3_600_000 };

// durationMs reads the duration forms the fixtures use, as Go's time.ParseDuration writes them:
// "30s", "2m10s", "1h40m", "430h".
function durationMs(text: string): number {
  if (!DURATION.test(text)) throw new Error(`fixture time: bad duration ${text}`);
  let total = 0;
  for (const m of text.matchAll(PART)) total += Number(m[1]) * (UNIT_MS[m[2] ?? ""] ?? 0);
  return total;
}

/**
 * Replaces every string of the form now+duration or now-duration in value with the RFC 3339 time
 * it names. A bare "now" is left alone: it is a contract value (a flow stage's state), not a time.
 */
export function fillTimes(value: unknown, now: number): unknown {
  if (typeof value === "string") {
    const m = value.match(RELATIVE);
    if (m === null) return value;
    const at = now + (m[1] === "-" ? -1 : 1) * durationMs(m[2] ?? "");
    return new Date(at).toISOString().replace(/\.\d{3}Z$/, "Z");
  }
  if (Array.isArray(value)) return (value as unknown[]).map((v) => fillTimes(v, now));
  if (typeof value === "object" && value !== null) {
    return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, fillTimes(v, now)]));
  }
  return value;
}

/** The canned snapshot with its times counted from now (ms), a fresh copy each call. */
export function fixtureSnapshot(now = Date.now()): Snapshot {
  return fillTimes(snapshotJson, now) as Snapshot;
}

/** The canned conversation (turns.json), or the subagent's page (agent-turns.json) when agent is true; a fresh copy. */
export function fixtureTurns(agent = false): Turns {
  return fillTimes(agent ? agentTurnsJson : turnsJson, Date.now()) as Turns;
}

/** The canned diff (diff.json), a fresh copy. */
export function fixturePatch(): Patch {
  return fillTimes(diffJson, Date.now()) as Patch;
}

/** The canned job log's tail (job-log.json), or what comes after it (job-log-after.json) when after is true; a fresh copy. */
export function fixtureJobLog(after = false): JobLog {
  return fillTimes(after ? jobLogAfterJson : jobLogJson, Date.now()) as JobLog;
}

/** The canned flow board (flow.json): session 6's run parked on story 3; a fresh copy. */
export function fixtureFlow(): Flow {
  return fillTimes(flowJson, Date.now()) as Flow;
}

/** The canned flow document (flow-doc.json): session 6's spec; a fresh copy. */
export function fixtureFlowDoc(): FlowDoc {
  return fillTimes(flowDocJson, Date.now()) as FlowDoc;
}

/** The canned host's flows (flows.json): the live ones and the past runs; a fresh copy. */
export function fixtureFlows(): Flows {
  return fillTimes(flowsJson, Date.now()) as Flows;
}

/** The canned launch defaults (launch-defaults.json); a fresh copy. */
export function fixtureLaunchDefaults(): LaunchDefaults {
  return fillTimes(launchDefaultsJson, Date.now()) as LaunchDefaults;
}
