import type { ReactElement } from "react";

import { panelProps, useRoute } from "@/app/routes";
import { useIsPending } from "@/store/pending";

// NoSession is what a session's screen says for a session the snapshot does not list: "Loading…"
// while the route's session was created a moment ago, else that it is gone.
export function NoSession(): ReactElement {
  const pending = useIsPending(panelProps(useRoute()).sessionId);
  return (
    <p role="status" className="px-4 py-8 text-center text-muted-foreground">
      {pending ? "Loading…" : "This session is gone."}
    </p>
  );
}
