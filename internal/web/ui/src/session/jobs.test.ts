import { describe, expect, it } from "vitest";

import type { Job } from "@/api/gen";
import { makeJob } from "@/commands/testkit";

import { elapsed, jobEnd, jobGlyph, jobLine, jobTone } from "./jobs";

const NOW = Date.parse("2026-11-19T12:00:00Z");
const ago = (ms: number): string => new Date(NOW - ms).toISOString();
const unknown = "paused" as Job["state"];

describe("jobGlyph", () => {
  it("marks each state", () => {
    expect(jobGlyph(makeJob({ state: "running" }))).toBe("◐");
    expect(jobGlyph(makeJob({ state: "queued" }))).toBe("○");
    expect(jobGlyph(makeJob({ state: "held" }))).toBe("○");
    expect(jobGlyph(makeJob({ state: "cancelled" }))).toBe("⊘");
    expect(jobGlyph(makeJob({ state: "done", exit: { code: 0 } }))).toBe("✓");
  });

  it("marks a done job that did not exit 0, a failed job and a lost job as failures", () => {
    expect(jobGlyph(makeJob({ state: "done", exit: { code: 2 } }))).toBe("✗");
    expect(jobGlyph(makeJob({ state: "done", exit: null }))).toBe("✗");
    expect(jobGlyph(makeJob({ state: "failed" }))).toBe("✗");
    expect(jobGlyph(makeJob({ state: "lost" }))).toBe("✗");
  });

  it("gives a state it does not know a neutral mark", () => {
    expect(jobGlyph(makeJob({ state: unknown }))).toBe("?");
  });
});

describe("jobTone", () => {
  it("colours a running job, a success and a failure", () => {
    expect(jobTone(makeJob({ state: "running" }))).toBe("text-turn");
    expect(jobTone(makeJob({ state: "done", exit: { code: 0 } }))).toBe("text-working");
    expect(jobTone(makeJob({ state: "done", exit: { code: 2 } }))).toBe("text-error");
    expect(jobTone(makeJob({ state: "failed" }))).toBe("text-error");
    expect(jobTone(makeJob({ state: "lost" }))).toBe("text-error");
  });

  it("is neutral for a state that is not running, finished or failed, and for one it does not know", () => {
    for (const state of ["queued", "held", "cancelled", unknown] as Job["state"][]) {
      expect(jobTone(makeJob({ state }))).toBe("text-muted-foreground");
    }
  });
});

describe("elapsed", () => {
  it("words seconds, minutes and hours", () => {
    expect(elapsed(0, 45_000)).toBe("45s");
    expect(elapsed(0, 130_000)).toBe("2m 10s");
    expect(elapsed(0, 3_840_000)).toBe("1h 4m");
    expect(elapsed(0, 59_500)).toBe("1m 0s");
    expect(elapsed(5_000, 1_000)).toBe("0s");
  });

  it("is empty for a time that does not parse", () => {
    expect(elapsed(Date.parse(""), NOW)).toBe("");
  });
});

describe("jobEnd", () => {
  it("names the failure, then the cancel reason, then the signal, then the exit code", () => {
    const job = makeJob({
      state: "failed",
      failure: "boom",
      cancel_reason: "x",
      exit: { code: 1 },
    });
    expect(jobEnd(job)).toBe("boom");
    expect(jobEnd({ ...job, failure: "" })).toBe("cancelled: x");
    expect(
      jobEnd({ ...job, failure: "", cancel_reason: "", exit: { code: 9, signal: "SIGKILL" } }),
    ).toBe("SIGKILL");
    expect(jobEnd({ ...job, failure: "", cancel_reason: "", exit: { code: 3 } })).toBe("exit 3");
  });

  it("of a job with no exit says its state, an unknown one included", () => {
    expect(jobEnd(makeJob({ state: "lost" }))).toBe("lost");
    expect(jobEnd(makeJob({ state: unknown }))).toBe("paused");
  });
});

describe("jobLine", () => {
  it("of a running job says how long it has run", () => {
    const job = makeJob({ id: 214, state: "running", started: ago(130_000) });
    expect(jobLine(job, NOW)).toBe("job 214 · 2m 10s");
    expect(jobLine({ ...job, started: "" }, NOW)).toBe("job 214");
  });

  it("of a queued or held job says its state and why it waits", () => {
    expect(jobLine(makeJob({ id: 216, state: "queued", waiting: "slot busy" }), NOW)).toBe(
      "job 216 · queued · slot busy",
    );
    expect(jobLine(makeJob({ id: 215, state: "held" }), NOW)).toBe("job 215 · held");
  });

  it("of a finished job says how it ended and when", () => {
    const done = makeJob({ id: 212, state: "done", exit: { code: 0 }, ended: ago(240_000) });
    expect(jobLine(done, NOW)).toBe("job 212 · exit 0 · 4m ago");
    const failed = makeJob({
      id: 209,
      state: "failed",
      failure: "out of memory",
      ended: ago(3 * 3_600_000),
    });
    expect(jobLine(failed, NOW)).toBe("job 209 · out of memory · 3h ago");
    expect(jobLine({ ...done, ended: ago(10_000) }, NOW)).toBe("job 212 · exit 0 · now");
    expect(jobLine({ ...done, ended: "" }, NOW)).toBe("job 212 · exit 0");
  });

  it("of a job whose state it does not know says the raw state", () => {
    expect(jobLine(makeJob({ id: 214, state: unknown, exit: null, ended: "" }), NOW)).toBe(
      "job 214 · paused",
    );
  });
});
