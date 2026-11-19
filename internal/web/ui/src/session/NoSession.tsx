import type { ReactElement } from "react";

// NoSession is what a session's screen says for a session the snapshot does not list.
export function NoSession(): ReactElement {
  return (
    <p role="status" className="px-4 py-8 text-center text-muted-foreground">
      This session is gone.
    </p>
  );
}
