// The screens app.mjs renders by route name: each a Screen (the body, in
// <main>) and a Head (its context row and, for a session, its tabs, in the
// pinned header; the status row is the footer's), both with the props
// {snap, stale, link, route, go, act, toast, pending}. Pairing is app.mjs's
// own screen.
import { Fleet, FleetHead } from "./fleet.mjs";
import { Info, SessionHead } from "./session.mjs";
import { NewSession, NewHead } from "./newsession.mjs";
import { Settings, SettingsHead } from "./settings.mjs";
import { Turns } from "./turns.mjs";
import { Changes } from "./changes.mjs";
import { Mockup } from "./mockup.mjs";
import { Agent, AgentHead } from "./agent.mjs";
import { Job, JobHead } from "./job.mjs";
import { Flow, FlowStory } from "./flow.mjs";
import { FlowDoc } from "./flowdoc.mjs";
import { Flows, FlowsHead } from "./flows.mjs";

export const screens = {
  fleet: { Screen: Fleet, Head: FleetHead },
  info: { Screen: Info, Head: SessionHead },
  new: { Screen: NewSession, Head: NewHead },
  settings: { Screen: Settings, Head: SettingsHead },
  turns: { Screen: Turns, Head: SessionHead },
  changes: { Screen: Changes, Head: SessionHead },
  mockup: { Screen: Mockup, Head: SessionHead },
  agent: { Screen: Agent, Head: AgentHead },
  job: { Screen: Job, Head: JobHead },
  flow: { Screen: Flow, Head: SessionHead },
  flowstory: { Screen: FlowStory, Head: SessionHead },
  flowdoc: { Screen: FlowDoc, Head: SessionHead },
  flows: { Screen: Flows, Head: FlowsHead },
};
