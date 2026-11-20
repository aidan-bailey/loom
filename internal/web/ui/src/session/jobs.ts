import type { Job } from "@/api/gen";
import { ago } from "@/panels/fleet/SessionRow";

// jobGlyph is the mark of a job's state; a state this build does not know gets a neutral "?",
// never a success or a failure.
export function jobGlyph(job: Job): string {
  switch (job.state) {
    case "running":
      return "◐";
    case "queued":
    case "held":
      return "○";
    case "cancelled":
      return "⊘";
    case "done":
      return job.exit !== null && job.exit.code === 0 ? "✓" : "✗";
    case "failed":
    case "lost":
      return "✗";
    default:
      return "?";
  }
}

// jobTone is the token class of a job's glyph, chosen through the glyph so a done job's tone
// follows its exit code and an unknown state stays neutral.
export function jobTone(job: Job): string {
  switch (jobGlyph(job)) {
    case "◐":
      return "text-turn";
    case "✓":
      return "text-working";
    case "✗":
      return "text-error";
    default:
      return "text-muted-foreground";
  }
}

// elapsed words the milliseconds between two times: "45s", "2m 10s", "1h 4m"; "" for a start that
// did not parse.
export function elapsed(from: number, to: number): string {
  if (Number.isNaN(from)) return "";
  const secs = Math.max(0, Math.round((to - from) / 1000));
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ${secs % 60}s`;
  return `${Math.floor(mins / 60)}h ${mins % 60}m`;
}

// jobEnd is how a finished job ended: its failure, its cancel reason, its signal or exit code,
// else the raw state (so a state this build does not know shows its own word).
export function jobEnd(job: Job): string {
  if (job.failure !== "") return job.failure;
  if (job.cancel_reason !== "") return `cancelled: ${job.cancel_reason}`;
  if (job.exit !== null) {
    return job.exit.signal !== undefined && job.exit.signal !== ""
      ? job.exit.signal
      : `exit ${job.exit.code}`;
  }
  return job.state;
}

// joinParts joins the parts of a job's line with " · ", leaving out the empty ones.
export function joinParts(parts: string[]): string {
  return parts.filter((p) => p !== "").join(" · ");
}

// waitingParts is a queued or held job's state and why it waits.
export function waitingParts(job: Job): string[] {
  return [job.state, job.waiting];
}

// endedParts is how a finished job ended and, once it is known, how long ago; `ran` (how long it
// ran) goes between them when given.
export function endedParts(job: Job, now: number, ran: string = ""): string[] {
  const age = job.ended === "" ? "" : ago(job.ended, now);
  return [jobEnd(job), ran, age === "now" ? age : age === "" ? "" : `${age} ago`];
}

// jobLine is a job's sub-line: its id, then how long it has run, why it waits, or how and when it
// ended.
export function jobLine(job: Job, now: number): string {
  const parts = [`job ${job.id}`];
  if (job.state === "running") {
    parts.push(elapsed(Date.parse(job.started), now));
  } else if (job.state === "queued" || job.state === "held") {
    parts.push(...waitingParts(job));
  } else {
    parts.push(...endedParts(job, now));
  }
  return joinParts(parts);
}
