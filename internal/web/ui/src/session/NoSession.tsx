import type { ReactElement } from "react";

import { useIsPending } from "@/store/pending";

// NoSession is what a session's screen says for a session the snapshot does not list: "Loading…"
// while the session the panel was given was created a moment ago, else that it is gone.
export function NoSession(props: { sessionId: number | undefined }): ReactElement {
  const pending = useIsPending(props.sessionId);
  return (
    <p role="status" className="px-4 py-8 text-center text-muted-foreground">
      {pending ? "Loading…" : "This session is gone."}
    </p>
  );
}
