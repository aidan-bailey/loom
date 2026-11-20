// The changes panel: a session's diff, the second tab, shown for a session whose diff can be read.
import type { ReactElement } from "react";

import { definePanel, type PanelProps } from "@/app/panels";
import { sessionActions } from "@/commands/session";
import { NoSession } from "@/session/NoSession";
import { SessionHead } from "@/session/SessionHead";
import { useSession } from "@/store/fleet";

import { Changes, ChangesMark } from "./Changes";

function ChangesBody(props: PanelProps): ReactElement {
  const session = useSession(props.sessionId);
  if (session === undefined) return <NoSession />;
  // The key gives each session its own open set and held patch state.
  return <Changes key={session.id} session={session} />;
}

export const changesPanel = definePanel({
  id: "changes",
  title: () => "Changes",
  appliesTo: (s) => sessionActions(s).diff,
  tab: { order: 2, label: "changes", Mark: ChangesMark },
  Head: SessionHead,
  Body: ChangesBody,
});
