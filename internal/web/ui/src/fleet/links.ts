// Where a fleet row links.
import type { Session } from "@/api/gen";
import { hrefFor } from "@/app/routes";

/**
 * Whether the session's row opens its mockup: its brainstorm companion's screen changed in the
 * latest turn and no dialog waits on the session view, where it is answered. The daemon's push
 * follows the same rule (opensMockup in internal/web/triggers.go).
 */
export function opensMockup(session: Session): boolean {
  return session.mockup !== null && session.mockup.fresh && (session.asks ?? []).length === 0;
}

/** The row's link: the mockup while it is fresh, else the conversation. */
export function sessionHref(session: Session): string {
  const params = { id: String(session.id) };
  return opensMockup(session) ? hrefFor("mockup", params) : hrefFor("turns", params);
}
