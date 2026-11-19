// The fleet's context row: its title, and links to the flows, the settings and a new session. They
// are plain links, not commands: they act on nothing.
import { Settings } from "lucide-react";
import type { MouseEvent, ReactElement } from "react";

import { hrefFor } from "@/app/routes";
import { ContextRow } from "@/app/shell";
import { allSessions, shownWorkspaces } from "@/fleet/groups";
import { cn } from "@/lib/utils";
import { useFleet, useLink } from "@/store/fleet";

const LINK = "flex min-h-11 min-w-11 flex-none items-center justify-center text-sm";

export function FleetHead(): ReactElement {
  const hasFlows = useFleet((s) => {
    if (s.snapshot === null) return false;
    const shown = new Set(shownWorkspaces(s.snapshot).map((w) => w.id));
    return allSessions(s.snapshot).some(
      (x) => shown.has(x.workspace) && (x.flows ?? []).length > 0,
    );
  });
  const { stale } = useLink();

  const actions = (
    <>
      {hasFlows ? (
        <a href={hrefFor("flows")} aria-label="Flows" className={cn(LINK, "text-muted-foreground")}>
          flows
        </a>
      ) : null}
      <a
        href={hrefFor("settings")}
        aria-label="Settings"
        className={cn(LINK, "text-muted-foreground")}
      >
        <Settings aria-hidden="true" className="size-5" />
      </a>
      <a
        href={hrefFor("new")}
        aria-disabled={stale ? "true" : undefined}
        onClick={(e: MouseEvent) => {
          if (stale) e.preventDefault();
        }}
        className={cn(
          LINK,
          "rounded-md bg-secondary px-3 font-medium text-foreground",
          stale && "opacity-50",
        )}
      >
        + New
      </a>
    </>
  );

  return <ContextRow title="Fleet" actions={actions} />;
}
