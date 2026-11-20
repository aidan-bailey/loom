// The mockup screens' bodies (a chunk of their own): the companion's page in a sandboxed frame,
// with a strip and the reply box, or alone in the full-screen view.
import { useState, type ReactElement } from "react";

import type { Session } from "@/api/gen";
import { api } from "@/api/request";
import type { PanelProps } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { Button } from "@/components/ui/button";
import { ReplyBox } from "@/conversation/ReplyBox";
import { useMarkSeen } from "@/fleet/seen";
import { ago } from "@/panels/fleet/SessionRow";
import { NoSession } from "@/session/NoSession";
import { useSession } from "@/store/fleet";

// Frame draws the mockup. The page is HTML a session wrote: the sandbox is scripts only and never
// `allow-same-origin`, or it could call /api/v1 with the device cookie. `v` is the mockup's change
// time, so a new screen found at a turn's end reloads the frame, and `r` the reload count.
function Frame({ session, again }: { session: Session; again: number }): ReactElement {
  const mk = session.mockup;
  if (mk === null) {
    return (
      <p className="px-4 py-8 text-center text-muted-foreground">
        No mockup is showing; the companion may have stopped.
      </p>
    );
  }
  const src = `${api.mockupURL(session.id)}?v=${encodeURIComponent(mk.changed)}&r=${again}`;
  return (
    <iframe
      key={src}
      src={src}
      title={"Mockup: " + mk.screen}
      sandbox="allow-scripts"
      referrerPolicy="no-referrer"
      className="min-h-60 w-full flex-1 border-0 border-t border-border bg-background"
    />
  );
}

export default function MockupBody(props: PanelProps): ReactElement {
  const session = useSession(props.sessionId);
  const [again, setAgain] = useState(0);
  useMarkSeen(props.sessionId);
  if (session === undefined) return <NoSession />;
  const mk = session.mockup;
  return (
    <section className="flex flex-1 flex-col">
      <div className="flex min-h-11 items-center gap-1 px-4 text-xs text-muted-foreground">
        <span className="min-w-0 flex-1 truncate">
          {mk === null ? "no mockup" : `${mk.screen} · ${ago(mk.changed)}`}
        </span>
        {mk === null ? null : (
          <>
            <Button
              variant="ghost"
              size="icon"
              aria-label="Read the mockup again"
              className="min-h-11 min-w-11"
              onClick={() => {
                setAgain((n) => n + 1);
              }}
            >
              ↻
            </Button>
            <a
              href={hrefFor("mockupFull", { id: String(session.id) })}
              aria-label="Full screen"
              className="flex min-h-11 min-w-11 items-center justify-center"
            >
              ⤢
            </a>
          </>
        )}
      </div>
      <Frame session={session} again={again} />
      <div className="px-4 py-2">
        <ReplyBox session={session} />
      </div>
    </section>
  );
}

export function MockupFullBody(props: PanelProps): ReactElement {
  const session = useSession(props.sessionId);
  useMarkSeen(props.sessionId);
  if (session === undefined) return <NoSession />;
  return (
    <section className="fixed inset-0 z-40 flex flex-col bg-background pt-[env(safe-area-inset-top)] pb-[env(safe-area-inset-bottom)]">
      <Frame session={session} again={0} />
      <a
        href={hrefFor("mockup", { id: String(session.id) })}
        className="absolute top-[calc(env(safe-area-inset-top)+6px)] right-2 flex min-h-11 items-center rounded-lg border border-border bg-card px-3 text-[13px]"
      >
        ✕ done
      </a>
    </section>
  );
}
