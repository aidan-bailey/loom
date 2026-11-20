// The new-session panel: the head (a way back to the fleet and the title) is in the entry; the
// body is a lazy chunk, so a first launch never loads it. It has no tab.
import { lazy, type ReactElement } from "react";

import { definePanel } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { ContextRow } from "@/app/shell";

// NewHead is the new-session screen's context row: a plain link back to the fleet, no command.
export function NewHead(): ReactElement {
  return <ContextRow back={hrefFor("fleet")} title="New session" />;
}

const NewSessionBody = lazy(() => import("./NewSessionBody"));

export const newPanel = definePanel({
  id: "new",
  title: () => "New session",
  Head: NewHead,
  Body: NewSessionBody,
});
