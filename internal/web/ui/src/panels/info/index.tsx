// The info panel: a session's sub-line, tiles, subagents, jobs, actions and reply box. The
// conversation panel draws this body for a session that does not run Claude.
import type { ReactElement } from "react";

import { definePanel, type PanelProps } from "@/app/panels";
import { NoSession } from "@/session/NoSession";
import { SessionHead } from "@/session/SessionHead";
import { useSession } from "@/store/fleet";

import { Info } from "./Info";

function InfoBody(props: PanelProps): ReactElement {
  const session = useSession(props.sessionId);
  if (session === undefined) return <NoSession sessionId={props.sessionId} />;
  return <Info session={session} />;
}

export const infoPanel = definePanel({
  id: "info",
  title: () => "Info",
  tab: { order: 4, label: "info" },
  Head: SessionHead,
  Body: InfoBody,
});
