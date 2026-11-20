// The host's flows (a chunk of its own): a port of the classic flows screen over useFlows. It
// draws the error and loading states, the live box and, below it, the past runs by repo.
import type { ReactElement } from "react";

import type { LiveFlow, PastRun } from "@/api/gen";
import { useFlows } from "@/api/reads";
import { hrefFor } from "@/app/routes";
import { Badge } from "@/components/ui/badge";

import { ago, flowChip, flowTone } from "../fleet/SessionRow";
import { Bars, median, usd } from "../flow/parts";

const BOX = "rounded-xl border border-border bg-card p-3";
const LEGEND = "text-[11px] tracking-wide text-muted-foreground uppercase";

// SHOWN is the runs a repo's box lists; BARS the priced runs its cost bars draw.
const SHOWN = 8;
const BARS = 5;

// repoName is the part of a records key after its last -Source-, else the key without its
// leading dashes: a key is the main checkout's path with every other character made -.
function repoName(key: string): string {
  const at = key.lastIndexOf("-Source-");
  if (at >= 0) return key.slice(at + "-Source-".length);
  return key.replace(/^-+/, "");
}

// stemName is a run's stem without its leading YYYY-MM-DD-.
function stemName(stem: string): string {
  return stem.replace(/^\d{4}-\d{2}-\d{2}-/, "");
}

function RepoBars({ runs }: { runs: PastRun[] }): ReactElement | null {
  const per = runs
    .filter((r) => typeof r.usd === "number" && r.stories > 0)
    .slice(0, BARS)
    .map((r) => (r.usd ?? 0) / r.stories)
    .reverse();
  if (per.length < 2) return null;
  return (
    <div className="mt-2 flex flex-col gap-1">
      <Bars values={per} current={null} />
      <p className="text-xs text-faint">
        run cost per story, last {per.length} runs · median {usd(median(per))}
      </p>
    </div>
  );
}

function PastBox({ name, runs }: { name: string; runs: PastRun[] }): ReactElement {
  return (
    <div className={BOX}>
      <p className={LEGEND}>past · {name}</p>
      {runs.slice(0, SHOWN).map((r, i) => {
        const facts = [
          `${r.stories} st`,
          usd(r.usd),
          r.ended,
          r.end !== "" ? ago(r.end) : "",
        ].filter((f) => f !== "");
        return (
          <div key={`${r.file}-${i}`} className="flex min-h-11 items-center gap-2">
            <span className="min-w-0 flex-1 truncate">{stemName(r.stem)}</span>
            {r.unreadable !== "" ? (
              <span className="ml-auto text-right text-xs text-error">unreadable</span>
            ) : (
              <span className="ml-auto text-right text-xs text-muted-foreground">
                {facts.join(" · ")}
              </span>
            )}
          </div>
        );
      })}
      {runs.length > SHOWN && <p className="text-xs text-faint">and {runs.length - SHOWN} more</p>}
      <RepoBars runs={runs} />
    </div>
  );
}

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
  const byRepo = new Map<string, PastRun[]>();
  for (const r of flows.past ?? []) byRepo.set(r.key, [...(byRepo.get(r.key) ?? []), r]);
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
      {[...byRepo].map(([key, runs]) => (
        <PastBox key={key} name={repoName(key)} runs={runs} />
      ))}
      {byRepo.size === 0 && <p className="text-xs text-faint">No run records yet.</p>}
    </section>
  );
}
