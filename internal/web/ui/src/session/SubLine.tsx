import type { ReactElement } from "react";

import { ago } from "@/panels/fleet/SessionRow";
import type { Session } from "@/api/gen";
import { useFleet } from "@/store/fleet";

import { groupGlyph, groupTone } from "./facts";

// SubLine is a session's second line: its status and how long it has held it, its workspace and its
// branch. A port of the classic session's sub-line.
export function SubLine({ session }: { session: Session }): ReactElement {
  const workspace = useFleet((s) =>
    s.snapshot?.workspaces?.find((w) => w.id === session.workspace),
  );
  const since = session.since === "" ? "" : ago(session.since);
  const name = workspace === undefined ? "" : workspace.label || workspace.name;
  return (
    <div className="flex flex-wrap gap-x-2 px-4 py-2 text-xs text-muted-foreground">
      <span>
        <span aria-hidden="true" className={groupTone(session.group)}>
          {groupGlyph(session.group)}
        </span>{" "}
        {session.label.toLowerCase()}
        {since === "" ? "" : ` · ${since}`}
      </span>
      {name === "" ? null : <span>{name}</span>}
      {session.branch === "" ? null : <span>{session.branch}</span>}
    </div>
  );
}
