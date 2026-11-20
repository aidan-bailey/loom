import type { ReactElement } from "react";

import type { Session } from "@/api/gen";
import { buttonVariants } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { groupGlyph } from "@/session/facts";

import { Asks } from "./Asks";

// NeedsYou is the box over the conversation of a session that waits: why it waits, its asks, and
// the way into Claude. Open in Claude is a plain anchor, never a command: it acts on nothing in
// loom, and a window opened after a command's awaits is a popup the browser blocks.
export function NeedsYou(props: { session: Session }): ReactElement {
  const { session } = props;
  const asks = (session.asks ?? []).length > 0;
  const reason =
    session.wait.reason !== ""
      ? session.wait.reason
      : session.gist
        ? `asks: ${session.gist}`
        : "Needs you";
  return (
    <section
      aria-label="Needs you"
      className="flex flex-col gap-3 rounded-xl border border-attention/30 bg-attention/10 p-3"
    >
      <div className="text-[12px] font-semibold text-attention">
        <span aria-hidden="true">{groupGlyph("needs_you")}</span> Needs you
      </div>
      <div className="text-[14px]">{reason}</div>
      {session.wait.detail !== "" && !asks ? (
        <pre className="overflow-x-auto whitespace-pre-wrap rounded-md bg-background p-2 font-mono text-[12.5px]">
          {session.wait.detail}
        </pre>
      ) : null}
      <Asks session={session} />
      {session.remote_url !== "" ? (
        <a
          href={session.remote_url}
          target="_blank"
          rel="noopener noreferrer"
          className={cn(
            buttonVariants({ variant: asks ? "secondary" : "default", size: "lg" }),
            "w-full",
          )}
        >
          Open in Claude
        </a>
      ) : (
        <p className="text-[12.5px] text-muted-foreground">
          Remote Control is off for this session.
        </p>
      )}
    </section>
  );
}
