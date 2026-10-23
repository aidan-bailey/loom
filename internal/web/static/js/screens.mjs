// The screens app.mjs renders by route name, each with the props
// {snap, stale, link, route, go, act, toast, pending}. Pairing is app.mjs's own screen.
import { Fleet } from "./fleet.mjs";
import { Session } from "./session.mjs";
import { NewSession } from "./newsession.mjs";
import { Settings } from "./settings.mjs";
import { Turns } from "./turns.mjs";
import { Changes } from "./changes.mjs";

export const screens = {
  fleet: Fleet,
  session: Session,
  new: NewSession,
  settings: Settings,
  turns: Turns,
  changes: Changes,
};
