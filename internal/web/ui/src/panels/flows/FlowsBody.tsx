// The host's flows (a chunk of its own): a port of the classic flows screen over useFlows. It
// draws the error and loading states, the live box and, below it, the past runs by repo.
import type { ReactElement } from "react";

import type { LiveFlow } from "@/api/gen";
import { useFlows } from "@/api/reads";
import { hrefFor } from "@/app/routes";
import { Badge } from "@/components/ui/badge";

import { flowChip, flowTone } from "../fleet/SessionRow";

const BOX = "rounded-xl border border-border bg-card p-3";
const LEGEND = "text-[11px] tracking-wide text-muted-foreground uppercase";

function Live({ live }: { live: LiveFlow }): ReactElement {
  const flow = live.flow;
  return (
    <a
      href={hrefFor("flow", { id: String(live.session) }, { plan: flow.plan })}
      className="flex min-h-11 items-center gap-2"
    >
      <span className="min-w-0 flex-1 truncate">
        <span className="font-semibold">{live.title}</span>
        {flow.part !== "" && <span className="text-muted-foreground"> {flow.part}</span>}
      </span>
      <Badge variant={flowTone(flow)} data-tone={flowTone(flow)}>
        {flowChip(flow)}
      </Badge>
    </a>
  );
}

export default function FlowsBody(): ReactElement {
  const { flows, error } = useFlows();
  if (flows === undefined && error !== "") {
    return (
      <p role="alert" className="px-4 py-6 text-center text-error">
        {error}
      </p>
    );
  }
  if (flows === undefined) {
    return (
      <p role="status" className="px-4 py-6 text-center text-muted-foreground">
        Loading…
      </p>
    );
  }
  const live = flows.live ?? [];
  return (
    <section className="flex flex-col gap-4 px-4 pb-6">
      {error !== "" && (
        <p role="alert" className="text-sm text-error">
          {error}
        </p>
      )}
      <div className={BOX}>
        <p className={LEGEND}>live</p>
        {live.length === 0 ? (
          <p className="text-sm text-faint">No session&apos;s worktree holds a flow.</p>
        ) : (
          live.map((l) => <Live key={l.session} live={l} />)
        )}
      </div>
    </section>
  );
}
