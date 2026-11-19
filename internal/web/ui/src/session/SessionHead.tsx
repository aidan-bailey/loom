import type { ReactElement } from "react";

import type { PanelProps } from "@/app/panels";
import { hrefFor, useRoute } from "@/app/routes";
import { ContextRow } from "@/app/shell";
import { useSession } from "@/store/fleet";

// SessionHead is a session's context row: the way back to the fleet, its title and, when Remote
// Control is on, a link to the session in Claude. The link is a plain anchor, never a command: it
// acts on nothing in loom, and a window opened after a command's awaits is a popup the browser
// blocks.
export function SessionHead(props: PanelProps): ReactElement {
  const session = useSession(props.sessionId);
  const route = useRoute();
  if (session === undefined) return <ContextRow back={hrefFor("fleet")} title="" />;
  // The conversation's needs-you box has its own Open in Claude button.
  const boxed = route.panel === "turns" && session.claude && session.group === "needs_you";
  const actions =
    boxed || session.remote_url === "" ? null : (
      <a
        href={session.remote_url}
        target="_blank"
        rel="noopener noreferrer"
        className="flex min-h-11 flex-none items-center text-sm text-muted-foreground"
      >
        claude <span aria-hidden="true">↗</span>
      </a>
    );
  return <ContextRow back={hrefFor("fleet")} title={session.title} actions={actions} />;
}
