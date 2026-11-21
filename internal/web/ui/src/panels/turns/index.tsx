// The conversation panel, a port of the classic Turns: a Claude session's turns, the needs-you
// box and the reply box; any other program draws the info panel's body.
import type { ReactElement } from "react";

import type { Session } from "@/api/gen";
import { definePanel, panelFor, type PanelProps } from "@/app/panels";
import { FactStrip } from "@/session/FactStrip";
import { NoSession } from "@/session/NoSession";
import { SessionHead } from "@/session/SessionHead";
import { useSession } from "@/store/fleet";

import { Conversation } from "./Conversation";

function Body(props: PanelProps): ReactElement {
  const session = useSession(props.sessionId);
  if (session === undefined) return <NoSession sessionId={props.sessionId} />;
  if (!session.claude) {
    // Looked up at render: Story 9's info panel registers whenever its module is imported.
    const info = panelFor("info");
    return info === undefined ? (
      <NoSession sessionId={props.sessionId} />
    ) : (
      <info.Body {...props} />
    );
  }
  return <Conversation session={session} />;
}

export const turnsPanel = definePanel({
  id: "turns",
  title: () => "Conversation",
  appliesTo: (s: Session) => s.claude,
  tab: { order: 1, label: "conversation" },
  Head: SessionHead,
  Strip: FactStrip,
  Body,
});
