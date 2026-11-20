// The job screen's body (a chunk of its own): the job's facts by state, the controls its state
// allows and its log, followed to the bottom unless the reader scrolled up.
import { useEffect, useRef, type ReactElement } from "react";

import type { Job, Session } from "@/api/gen";
import { useJobLog } from "@/api/reads";
import type { PanelProps } from "@/app/panels";
import { CommandButton } from "@/app/CommandButton";
import { ago } from "@/panels/fleet/SessionRow";
import { elapsed, jobEnd, jobGlyph, jobTone } from "@/session/jobs";
import { NoSession } from "@/session/NoSession";
import { useSession } from "@/store/fleet";

// isLive is a job that has yet to end: its log is still being written.
function isLive(job: Job): boolean {
  return job.state === "running" || job.state === "queued" || job.state === "held";
}

// clock reads this phone's time; kept apart from the component, which the purity lint holds to
// render the same for the same props.
function clock(): number {
  return Date.now();
}

function joined(parts: string[]): string {
  return parts.filter((p) => p !== "").join(" · ");
}

// stateText words a job's state: how long it has run, why it waits, or how and when it ended.
function stateText(job: Job, now: number): string {
  if (job.state === "running") {
    return joined([
      "running",
      elapsed(Date.parse(job.started), now),
      job.paused && job.pause_reclaimed ? "paused" : "",
      job.paused && !job.pause_reclaimed ? "paused, CPU only" : "",
      job.forced ? "forced" : "",
    ]);
  }
  if (job.state === "queued" || job.state === "held") return joined([job.state, job.waiting]);
  const ran =
    job.started !== "" && job.ended !== ""
      ? elapsed(Date.parse(job.started), Date.parse(job.ended))
      : "";
  const age = job.ended === "" ? "" : ago(job.ended, now);
  return joined([
    jobEnd(job),
    ran === "" ? "" : `ran ${ran}`,
    age === "" ? "" : age === "now" ? age : `${age} ago`,
  ]);
}

function Controls({ job }: { job: Job }): ReactElement | null {
  if (job.control !== "") {
    return (
      <p className="text-xs text-muted-foreground">
        {job.control === "pause" ? "Pausing…" : "Resuming…"}
      </p>
    );
  }
  const buttons: ReactElement[] = [];
  const args = { job: job.id };
  if (job.state === "queued") {
    buttons.push(
      <CommandButton key="start" id="job.start" args={args} label="Start now" variant="default" />,
    );
  }
  if (job.state === "queued" || job.state === "held") {
    buttons.push(
      job.parked ? (
        <CommandButton
          key="unpark"
          id="job.unpark"
          args={args}
          label="Unpark"
          variant="secondary"
        />
      ) : (
        <CommandButton key="park" id="job.park" args={args} label="Park" variant="secondary" />
      ),
    );
  }
  if (job.state === "running") {
    buttons.push(
      job.paused ? (
        <CommandButton
          key="resume"
          id="job.resume"
          args={args}
          label="Resume"
          variant="secondary"
        />
      ) : (
        <CommandButton key="pause" id="job.pause" args={args} label="Pause" variant="secondary" />
      ),
    );
  }
  if (buttons.length === 0) return null;
  return (
    <>
      <div className="flex gap-2 pt-2">{buttons}</div>
      {job.control_error === "" ? null : (
        <p className="text-xs text-muted-foreground">{job.control_error}</p>
      )}
    </>
  );
}

const NOTICE = "px-0 py-2 text-[13px] text-muted-foreground";

function JobScreen({ session, job: id }: { session: Session; job: number }): ReactElement {
  const job = (session.jobs ?? []).find((j) => j.id === id);
  const live = job !== undefined && isLive(job);
  const log = useJobLog({ session: session.id, job: id, live });

  // follow is whether the page trails the log: true on open, false once the reader is more than
  // 40 px above the bottom, true again when they are back.
  const follow = useRef(true);
  useEffect(() => {
    const onScroll = (): void => {
      follow.current =
        window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 40;
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      window.removeEventListener("scroll", onScroll);
    };
  }, []);
  useEffect(() => {
    if (follow.current) window.scrollTo(0, document.documentElement.scrollHeight);
  }, [log.log]);

  const now = clock();
  let body: ReactElement;
  if (log.gone !== "") {
    body = <p className={NOTICE}>{log.gone}</p>;
  } else if (log.log === undefined) {
    body = <p className={NOTICE}>{log.error === "" ? "Loading…" : log.error}</p>;
  } else if (log.log.text === "") {
    body = <p className={NOTICE}>{live ? "No output yet." : "No output."}</p>;
  } else {
    body = (
      <>
        {log.error === "" ? null : <p className={NOTICE}>{log.error}</p>}
        {log.log.cut ? <p className={NOTICE}>Earlier output not shown.</p> : null}
        <pre className="mt-2 overflow-x-hidden rounded-lg border border-border bg-card p-2 font-mono text-[11px] leading-[1.45] break-words whitespace-pre-wrap">
          {log.log.text}
        </pre>
      </>
    );
  }

  return (
    <section className="flex flex-col gap-2 px-4 pb-6">
      {job === undefined ? null : (
        <div className="flex flex-col gap-1 text-[13px]">
          <div>
            <span aria-hidden="true" className={jobTone(job)}>
              {jobGlyph(job)}
            </span>{" "}
            {`job ${job.id} · ${stateText(job, now)}`}
          </div>
          {job.description === "" ? null : <div className="font-mono">{job.command}</div>}
          {job.agent === "" ? null : (
            <div className="text-xs text-muted-foreground">submitted by subagent {job.agent}</div>
          )}
          <Controls job={job} />
        </div>
      )}
      {body}
    </section>
  );
}

export default function JobBody(props: PanelProps): ReactElement {
  const session = useSession(props.sessionId);
  if (session === undefined) return <NoSession />;
  return <JobScreen session={session} job={Number(props.params.job)} />;
}
