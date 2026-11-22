// The fleet's context row: its title, and links to the flows, the settings and a new session. They
// are plain links, not commands: they act on nothing. FleetLinks is the links alone, for the
// desktop's top bar too.
import { Settings } from "lucide-react";
import type { MouseEvent, ReactElement } from "react";

import { hrefFor } from "@/app/routes";
import { ContextRow } from "@/app/shell";
import { hasShownFlows } from "@/fleet/groups";
import { cn } from "@/lib/utils";
import { useFleet, useLink } from "@/store/fleet";

const LINK = "flex min-h-[44px] min-w-[44px] flex-none items-center justify-center text-sm";
const LINK_DENSE = "flex h-7 min-w-7 flex-none items-center justify-center px-1.5 text-sm";

export function FleetLinks({ dense = false }: { dense?: boolean }): ReactElement {
  const link = dense ? LINK_DENSE : LINK;
  const hasFlows = useFleet((s) => s.snapshot !== null && hasShownFlows(s.snapshot));
  const { stale } = useLink();

  return (
    <>
      {hasFlows ? (
        <a href={hrefFor("flows")} aria-label="Flows" className={cn(link, "text-muted-foreground")}>
          flows
        </a>
      ) : null}
      <a
        href={hrefFor("settings")}
        aria-label="Settings"
        className={cn(link, "text-muted-foreground")}
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
          link,
          "rounded-md bg-secondary px-3 font-medium text-foreground",
          stale && "opacity-50",
        )}
      >
        + New
      </a>
    </>
  );
}

export function FleetHead(): ReactElement {
  return <ContextRow title="Fleet" actions={<FleetLinks />} />;
}
