import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type MouseEvent,
  type ReactElement,
} from "react";

import type { Session } from "@/api/gen";
import { useHeldTurns, useTurnPages } from "@/api/reads";
import type { PanelProps } from "@/app/panels";
import { hrefFor, routeOf } from "@/app/routes";
import { ContextRow } from "@/app/shell";
import { Button } from "@/components/ui/button";
import { TurnList } from "@/conversation/TurnList";
import { ago } from "@/panels/fleet/SessionRow";
import { NoSession } from "@/session/NoSession";
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

// GONE_AFTER is how long (ms) an agent's file may stay unwritten before the screen says its
// record is gone.
const GONE_AFTER = 30_000;

// useGoneTimer is true once `missing` has stayed true for GONE_AFTER since the page first said so
// (or since `attempt` last changed); a page that has the file, or a new attempt, restarts the wait.
function useGoneTimer(missing: boolean, attempt: number): boolean {
  const [waited, setWaited] = useState(false);
  useEffect(() => {
    if (!missing) return;
    const timer = setTimeout(() => {
      setWaited(true);
    }, GONE_AFTER);
    return () => {
      clearTimeout(timer);
      setWaited(false);
    };
  }, [missing, attempt]);
  return missing && waited;
}

function AgentTurns(props: { session: Session; agent: string | undefined }): ReactElement {
  const { session, agent } = props;
  const [attempt, setAttempt] = useState(0);
  // The page's agent, held by the entry useTurnPages writes: the poll switch depends on it.
  const page = useHeldTurns(session.id, agent)?.agent;
  const state = page?.state ?? "";
  const missing = page?.missing === true;
  const waited = useGoneTimer(missing, attempt);
  const running = state === "running" || (state === "unknown" && session.status === "Running");
  const gone = missing && (state === "finished" || waited);
  const starting = missing && !gone;
  const { held, error, load } = useTurnPages({
    session: session.id,
    agent,
    live: running || starting,
  });

  // Once more when the agent finishes: the page that said so may have been read before its last
  // records. A first page that already says finished does not refresh.
  const lastState = useRef("");
  const refresh = useCallback(() => {
    load("refresh");
  }, [load]);
  useEffect(() => {
    const last = lastState.current;
    lastState.current = state;
    if (last !== "" && last !== "finished" && state === "finished") refresh();
  }, [state, refresh]);

  let body: ReactElement;
  if (held === undefined) {
    body = (
      <div>
        <p className={error === "" ? "text-sm text-muted-foreground" : "text-sm text-error"}>
          {error === "" ? "Loading…" : error}
        </p>
        {error !== "" && (
          <Button
            variant="secondary"
            onClick={() => {
              load("newest");
            }}
          >
            retry
          </Button>
        )}
      </div>
    );
  } else if (gone) {
    body = (
      <div className="flex flex-col items-start gap-2">
        <p className="text-sm text-muted-foreground">This subagent's record is gone.</p>
        <Button
          variant="secondary"
          onClick={() => {
            setAttempt((n) => n + 1);
            load("newest");
          }}
        >
          retry
        </Button>
        <a
          href={hrefFor("turns", { id: String(session.id) })}
          className="flex min-h-11 items-center text-xs text-muted-foreground"
        >
          ‹ conversation
        </a>
      </div>
    );
  } else if (starting) {
    body = <p className="text-sm text-muted-foreground">Starting…</p>;
  } else {
    body = (
      <>
        {held.skipped > 0 && (
          <p className="text-xs text-muted-foreground">Some records could not be read.</p>
        )}
        {held.earlier !== null && (
          <Button
            variant="secondary"
            onClick={() => {
              load("earlier");
            }}
          >
            ↑ earlier turns
          </Button>
        )}
        {held.turns.length === 0 && <p className="text-sm text-muted-foreground">No turns yet.</p>}
        <TurnList turns={held.turns} sessionId={session.id} live={running} remote="" />
        {error !== "" && (
          <p className="text-xs text-muted-foreground">
            Could not refresh: {error} ·{" "}
            <Button variant="ghost" size="sm" onClick={refresh}>
              retry
            </Button>
          </p>
        )}
      </>
    );
  }
  return <section className="flex flex-col gap-3 px-4 py-3">{body}</section>;
}

// Agent is the subagent panel's body: one subagent's turns from its own transcript, with no reply
// box and no needs-you box. It reads nothing for a session the snapshot does not list.
export function Agent(props: PanelProps): ReactElement {
  const session = useSession(props.sessionId);
  if (session === undefined) return <NoSession />;
  return <AgentTurns session={session} agent={props.params.agent} />;
}
