import type { ReactElement } from "react";

import type { Session } from "@/api/gen";
import { CommandButton } from "@/app/CommandButton";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { sessionActions } from "@/commands/session";
import { MAX_MESSAGE_BYTES, utf8Length } from "@/lib/utf8";
import { useDrafts } from "@/store/drafts";
import { useFleet } from "@/store/fleet";

// ReplyBox is the box a reply is typed in: a textarea over the session's reply draft (kept across
// tab switches, so it holds no text of its own), quick-reply chips while it is empty, and a Send
// that is the `session.send` command. The trim, the 200 clearing the draft and the 202 keeping it
// are the command's; this draws the draft and the 64 KiB limit.
export function ReplyBox(props: { session: Session }): ReactElement {
  const { session } = props;
  const text = useDrafts((s) => s.replies[session.id] ?? "");
  const quick = useFleet((s) => s.snapshot?.quick_replies) ?? [];
  if (!sessionActions(session).send) {
    if (session.group !== "paused") return <></>;
    return <p className="text-[12.5px] text-muted-foreground">Resume the session to reply.</p>;
  }
  const setReply = (value: string): void => {
    useDrafts.getState().setReply(session.id, value);
  };
  const reply = text.trim();
  const tooLong = utf8Length(reply) > MAX_MESSAGE_BYTES;
  return (
    <div className="flex flex-col gap-2">
      {text === "" && quick.length > 0 ? (
        <div role="group" aria-label="Quick replies" className="flex flex-wrap gap-2">
          {quick.map((q) => (
            <Button
              key={q}
              variant="secondary"
              size="sm"
              onClick={() => {
                setReply(q);
              }}
            >
              {q}
            </Button>
          ))}
        </div>
      ) : null}
      <div className="flex items-end gap-2">
        <Textarea
          rows={1}
          aria-label="Reply"
          placeholder="reply, or /command…"
          value={text}
          onChange={(event) => {
            setReply(event.target.value);
          }}
        />
        <CommandButton
          id="session.send"
          args={{ text: reply }}
          label="send"
          disabled={reply === "" || tooLong}
        />
      </div>
      {tooLong ? (
        <p role="alert" className="text-[12px] text-error">
          Too long: a reply is at most 64 KiB.
        </p>
      ) : null}
    </div>
  );
}
