// The host's flows panel: the head (a way back to the fleet, the title, a link to the flow board
// and a button that reads the flows again) is in the entry; the body is a lazy chunk, so a first launch never loads it. This
// module imports nothing from ../flow/parts and no FlowsBody statically: a static import would
// pull them out of their chunk into the entry script.
import { lazy, type ReactElement } from "react";

import { useFlows } from "@/api/reads";
import { definePanel } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { ContextRow } from "@/app/shell";

// FlowsHead is the flows screen's context row. Its ↻ reads through the same shared query as the
// body, so it sends no read of its own.
export function FlowsHead(): ReactElement {
  const { reload } = useFlows();
  return (
    <ContextRow
      back={hrefFor("fleet")}
      title="Flows"
      actions={
        <>
          <a
            href={hrefFor("flowBoard")}
            aria-label="Flow board"
            className="flex min-h-[44px] min-w-[44px] flex-none items-center justify-center text-sm text-muted-foreground"
          >
            board
          </a>
          <button
            type="button"
            aria-label="Read the flows again"
            className="min-h-[44px] min-w-[44px]"
            onClick={reload}
          >
            ↻
          </button>
        </>
      }
    />
  );
}

const FlowsBody = lazy(() => import("./FlowsBody"));

export const flowsPanel = definePanel({
  id: "flows",
  title: () => "Flows",
  Head: FlowsHead,
  Body: FlowsBody,
});
