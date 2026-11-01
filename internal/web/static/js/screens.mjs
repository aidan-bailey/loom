// The screens app.mjs renders by route name: each a Screen (the body, in
// <main>) and a Head (its context row and, for a session, its tabs, in the
// header under the status row), both with the props {snap, stale, link,
// route, go, act, toast, pending}. Pairing is app.mjs's own screen.
import { Fleet, FleetHead } from "./fleet.mjs";
import { Session, SessionHead } from "./session.mjs";
import { NewSession, NewHead } from "./newsession.mjs";
import { Settings, SettingsHead } from "./settings.mjs";
import { Turns } from "./turns.mjs";
import { Changes } from "./changes.mjs";

export const screens = {
  fleet: { Screen: Fleet, Head: FleetHead },
  session: { Screen: Session, Head: SessionHead },
  new: { Screen: NewSession, Head: NewHead },
  settings: { Screen: Settings, Head: SettingsHead },
  turns: { Screen: Turns, Head: SessionHead },
  changes: { Screen: Changes, Head: SessionHead },
};
