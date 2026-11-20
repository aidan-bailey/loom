import type { MouseEvent, ReactElement } from "react";

import { useHeldTurns } from "@/api/reads";
import type { PanelProps } from "@/app/panels";
import { hrefFor, routeOf } from "@/app/routes";
import { ContextRow } from "@/app/shell";
import { ago } from "@/panels/fleet/SessionRow";
import { useSession } from "@/store/fleet";

// The hash the app was at before the current one, from the hashchange events: when it is this
// session's conversation, the way back is history.back(), which returns to the conversation's
// scroll position. The module is imported when the app starts, so it sees every navigation.
let previousHash = "";

// hashOf is the text of a URL from its first "#" on, "" when there is none; no URL parsing, so a
// malformed oldURL cannot throw.
function hashOf(url: string): string {
  const at = url.indexOf("#");
  return at < 0 ? "" : url.slice(at);
}

window.addEventListener("hashchange", (e: HashChangeEvent) => {
  previousHash = hashOf(e.oldURL);
});

function cameFromConversation(session: number): boolean {
  const route = routeOf(previousHash);
  return route.panel === "turns" && route.params.id === String(session);
}

// AgentHead is the subagent screen's context row: its way back to the conversation, through
// history when the conversation was the previous page, and the agent's title from its page.
export function AgentHead(props: PanelProps): ReactElement {
  const id = props.sessionId ?? 0;
  const session = useSession(props.sessionId);
  const agent = useHeldTurns(id, props.params.agent)?.agent;
  if (session === undefined) return <ContextRow back={hrefFor("fleet")} title="" />;
  const title =
    [agent?.description, agent?.type].find((t) => t !== undefined && t !== "") ?? "subagent";
  const onBack = (e: MouseEvent<HTMLAnchorElement>): void => {
    if (!cameFromConversation(id)) return;
    e.preventDefault();
    window.history.back();
  };
  return <ContextRow back={hrefFor("turns", { id: String(id) })} onBack={onBack} title={title} />;
}

// AgentStrip is the line under the session's tabs: what the agent is doing by its state, and a
// link to the conversation while the session needs you.
export function AgentStrip(props: PanelProps): ReactElement {
  const id = props.sessionId ?? 0;
  const session = useSession(props.sessionId);
  const agent = useHeldTurns(id, props.params.agent)?.agent;
  if (session === undefined || agent === undefined) return <></>;
  const age = agent.state === "running" ? ago(agent.started) : "";
  let lead: ReactElement | string;
  switch (agent.state) {
    case "running":
      lead = (
        <span className="text-working">{`◐ subagent running${age === "" ? "" : ` ${age}`}`}</span>
      );
      break;
    case "finished":
      lead = agent.teammate ? <span className="text-muted-foreground">○ idle</span> : "✓ finished";
      break;
    default:
      lead = "subagent";
  }
  return (
    <div
      data-slot="agent-strip"
      className="flex min-h-11 items-center gap-3 px-4 text-xs text-muted-foreground"
    >
      <span className="min-w-0 flex-1 truncate">
        {lead}
        {agent.type !== "" ? ` · ${agent.type}` : ""}
      </span>
      {session.group === "needs_you" && (
        <a
          href={hrefFor("turns", { id: String(session.id) })}
          className="flex min-h-11 flex-none items-center text-attention"
        >
          ● session needs you ›
        </a>
      )}
    </div>
  );
}
