// The job panel: the head names the job and goes back to info; the body is a lazy chunk, so a
// first launch never loads it. It has no tab: the shell lights the info tab for it.
import { lazy, type ReactElement } from "react";

import { definePanel, type PanelProps } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { ContextRow } from "@/app/shell";
import { useSession } from "@/store/fleet";

// JobHead is the job screen's context row: the way back to the session's info and the job's
// description, else its command, else `job <n>`.
export function JobHead(props: PanelProps): ReactElement {
  const session = useSession(props.sessionId);
  if (session === undefined) return <ContextRow back={hrefFor("fleet")} title="" />;
  const n = props.params.job ?? "";
  const job = (session.jobs ?? []).find((j) => j.id === Number(n));
  const title = [job?.description, job?.command].find((t) => t !== undefined && t !== "");
  return (
    <ContextRow back={hrefFor("info", { id: String(session.id) })} title={title ?? `job ${n}`} />
  );
}

const JobBody = lazy(() => import("./JobBody"));

export const jobPanel = definePanel({
  id: "job",
  title: (props) => "Job " + (props.params.job ?? ""),
  Head: JobHead,
  Body: JobBody,
});
