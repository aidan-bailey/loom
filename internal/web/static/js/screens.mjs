// The screens app.mjs renders by route name, each with the props
// {snap, stale, route, go, act, toast}. Pairing is app.mjs's own screen.
import { Fleet } from "./fleet.mjs";
import { Session } from "./session.mjs";
import { NewSession } from "./newsession.mjs";
import { Settings } from "./settings.mjs";

export const screens = {
  fleet: Fleet,
  session: Session,
  new: NewSession,
  settings: Settings,
};
