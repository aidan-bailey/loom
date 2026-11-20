import { useEffect, useLayoutEffect, useRef, type ReactElement } from "react";

import type { Session } from "@/api/gen";
import { keepScroll, keptScroll, useTurnPages } from "@/api/reads";
import { Button } from "@/components/ui/button";
import { NeedsYou } from "@/conversation/NeedsYou";
import { ReplyBox } from "@/conversation/ReplyBox";
import { TurnList } from "@/conversation/TurnList";
import { useMarkSeen } from "@/fleet/seen";

// A reader this close to the bottom (px) follows a refreshed conversation down.
const BOTTOM_MARGIN = 48;

interface Metrics {
  atBottom: boolean;
  fromBottom: number;
}

function measure(): Metrics {
  const height = document.documentElement.scrollHeight;
  return {
    atBottom: window.innerHeight + window.scrollY >= height - BOTTOM_MARGIN,
    fromBottom: height - window.scrollY,
  };
}

// The conversation of a Claude session: its turn pages, the needs-you box and the reply box.
// A port of the classic Conversation.
export function Conversation(props: { session: Session }): ReactElement {
  const { session } = props;
  const working = session.status === "Running";
  const { held, landed, error, load } = useTurnPages({
    session: session.id,
    agent: undefined,
    live: working,
  });
  // The seen time is written on arrival and again on leaving.
  useMarkSeen(session.id);
  // The stream's refresh: a change of the status or last message reads the newest turn again,
  // never on mount and never while the first page is still in flight.
  const signature = `${session.status}\n${session.last_message}`;
  const seenSignature = useRef(signature);
  useEffect(() => {
    if (signature === seenSignature.current) return;
    seenSignature.current = signature;
    if (held !== undefined) load("refresh");
  }, [signature, held, load]);

  // How the page stood before the content that is about to land.
  const metrics = useRef<Metrics>({ atBottom: true, fromBottom: 0 });

  // A conversation kept in the cache comes back where it was left, before the first paint.
  const heldNow = useRef(held);
  useLayoutEffect(() => {
    heldNow.current = held;
  });
  useLayoutEffect(() => {
    const kept = keptScroll(session.id);
    if (heldNow.current !== undefined && kept !== undefined) window.scrollTo(0, kept);
    metrics.current = measure();
  }, [session.id]);

  // A page that lands anchors the page: a first or reset page at the bottom, an earlier page
  // where the reader was, a refresh at the bottom only for a reader already there.
  const anchored = useRef(held);
  useLayoutEffect(() => {
    if (held === anchored.current) return;
    anchored.current = held;
    const height = document.documentElement.scrollHeight;
    if (landed === "newest" || (landed === "refresh" && metrics.current.atBottom)) {
      window.scrollTo(0, height);
    } else if (landed === "earlier") {
      window.scrollTo(0, height - metrics.current.fromBottom);
    }
    metrics.current = measure();
  }, [held, landed]);

  useEffect(() => {
    const onScroll = (): void => {
      keepScroll(session.id, window.scrollY);
      metrics.current = measure();
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      window.removeEventListener("scroll", onScroll);
    };
  }, [session.id]);
  return (
    <section className="flex flex-col gap-3 px-4 py-3">
      {held !== undefined && held.skipped > 0 && (
        <p className="text-xs text-muted-foreground">Some records could not be read.</p>
      )}
      {held !== undefined && held.earlier ? (
        <Button
          variant="ghost"
          size="sm"
          onClick={() => {
            load("earlier");
          }}
        >
          ↑ earlier turns
        </Button>
      ) : null}
      {held === undefined && (
        <div>
          {error !== "" ? (
            <p className="text-sm text-error">{error}</p>
          ) : (
            <p className="text-sm text-muted-foreground">Loading…</p>
          )}
          {error !== "" && (
            <Button
              variant="outline"
              onClick={() => {
                load("newest");
              }}
            >
              retry
            </Button>
          )}
        </div>
      )}
      {held !== undefined && held.turns.length === 0 && (
        <p className="text-sm text-muted-foreground">No turns yet.</p>
      )}
      <TurnList
        turns={held?.turns ?? []}
        sessionId={session.id}
        live={working}
        remote={session.remote_url}
      />
      {held !== undefined && error !== "" && (
        <p className="text-xs text-muted-foreground">
          Could not refresh: {error} ·{" "}
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              load("refresh");
            }}
          >
            retry
          </Button>
        </p>
      )}
      {session.group === "needs_you" && <NeedsYou session={session} />}
      <ReplyBox session={session} />
    </section>
  );
}
