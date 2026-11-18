// The canned loom's snapshot (tools/webshot/fixture/snapshot.json) for the tests: its relative
// times ("now-2m", "now+1h40m") filled as tools/webshot's fillTimes fills them, so the same
// fixture feeds Vitest and the screenshots.
import type { Snapshot } from "@/api/gen";
import snapshotJson from "../../../../tools/webshot/fixture/snapshot.json";

const RELATIVE = /^now(?:([+-])(\S+))?$/;
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

/** Replaces every string of the form now[+-duration] in value with the RFC 3339 time it names. */
export function fillTimes(value: unknown, now: number): unknown {
  if (typeof value === "string") {
    const m = value.match(RELATIVE);
    if (m === null) return value;
    const sign = m[1];
    const span = m[2];
    const at =
      sign === undefined || span === undefined
        ? now
        : now + (sign === "-" ? -1 : 1) * durationMs(span);
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
