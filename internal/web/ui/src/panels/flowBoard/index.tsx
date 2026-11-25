// The flow board's panel: the head (a way back to the fleet, the title and a link to the Flows
// screen) is in the entry; the body is a lazy chunk, so a first launch never loads it. This module
// imports no FlowBoardBody statically: a static import would pull it out of its chunk into the
// entry script.
import { lazy, type ReactElement } from "react";

import { definePanel } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { ContextRow } from "@/app/shell";

// FlowBoardHead is the board's context row. Its link is a plain link, not a command: it acts on
// nothing.
export function FlowBoardHead(): ReactElement {
  return (
    <ContextRow
      back={hrefFor("fleet")}
      title="Flow board"
      actions={
        <a
          href={hrefFor("flows")}
          aria-label="Flows"
          className="flex min-h-[44px] min-w-[44px] flex-none items-center justify-center text-sm text-muted-foreground"
        >
          flows
        </a>
      }
    />
  );
}

const FlowBoardBody = lazy(() => import("./FlowBoardBody"));

export const flowBoardPanel = definePanel({
  id: "flowBoard",
  title: () => "Flow board",
  Head: FlowBoardHead,
  Body: FlowBoardBody,
});
