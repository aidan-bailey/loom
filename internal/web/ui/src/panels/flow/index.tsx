// The flow panel: the fifth tab of a session that has flows, with an amber dot while any of them
// asks. Its body is a lazy chunk, so a first launch never loads the board. This module imports
// nothing from ./parts: that file would be pulled out of the lazy chunk into the entry script.
import { lazy, type ReactElement } from "react";

import type { Session } from "@/api/gen";
import { definePanel } from "@/app/panels";
import { SessionHead } from "@/session/SessionHead";

// FlowMark is the tab's mark: a dot while any of the session's flows asks.
export function FlowMark({ session }: { session: Session }): ReactElement | null {
  if (!(session.flows ?? []).some((f) => f.asks !== "")) return null;
  return (
    <span
      role="img"
      aria-label="asks"
      className="ml-1.5 inline-block size-2 rounded-full bg-attention"
    />
  );
}

const FlowBody = lazy(() => import("./FlowBody"));
const FlowStoryBody = lazy(() => import("./FlowStoryBody"));
const FlowDocBody = lazy(() => import("./FlowDocBody"));

export const flowPanel = definePanel({
  id: "flow",
  title: () => "Flow",
  appliesTo: (session) => (session.flows ?? []).length > 0,
  tab: { order: 5, label: "flow", Mark: FlowMark },
  Head: SessionHead,
  Body: FlowBody,
});

export const flowStoryPanel = definePanel({
  id: "flowStory",
  title: () => "Flow row",
  Head: SessionHead,
  Body: FlowStoryBody,
});

export const flowDocPanel = definePanel({
  id: "flowDoc",
  title: () => "Flow document",
  Head: SessionHead,
  Body: FlowDocBody,
});
