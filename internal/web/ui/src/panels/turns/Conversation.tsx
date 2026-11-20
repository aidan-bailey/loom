import { useEffect, useRef, type ReactElement } from "react";

import type { Session } from "@/api/gen";
import { useTurnPages } from "@/api/reads";
import { Button } from "@/components/ui/button";
import { NeedsYou } from "@/conversation/NeedsYou";
import { ReplyBox } from "@/conversation/ReplyBox";
import { TurnList } from "@/conversation/TurnList";

// The conversation of a Claude session: its turn pages, the needs-you box and the reply box.
// A port of the classic Conversation.
export function Conversation(props: { session: Session }): ReactElement {
  const { session } = props;
  const working = session.status === "Running";
  const { held, error, load } = useTurnPages({
    session: session.id,
    agent: undefined,
    live: working,
  });
  // The stream's refresh: a change of the status or last message reads the newest turn again,
  // never on mount and never while the first page is still in flight.
  const signature = `${session.status}\n${session.last_message}`;
  const seenSignature = useRef(signature);
  useEffect(() => {
    if (signature === seenSignature.current) return;
    seenSignature.current = signature;
    if (held !== undefined) load("refresh");
  }, [signature, held, load]);
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
