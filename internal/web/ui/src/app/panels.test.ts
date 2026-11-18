import { describe, expect, it } from "vitest";

import type { Session } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";

import { definePanel, panelFor, sessionTabs, type PanelProps } from "./panels";

const Body = (): null => null;
const props: PanelProps = { sessionId: undefined, params: {}, search: {} };

// aSession is the fixture's first session with the given fields changed.
function aSession(over: Partial<Session> = {}): Session {
  const base = allSessions(fixtureSnapshot())[0];
  if (base === undefined) throw new Error("the fixture holds no session");
  return { ...base, ...over };
}

describe("the panel registry", () => {
  it("finds a panel by its id once it is defined, and none before", () => {
    expect(panelFor("pair")).toBeUndefined();
    const panel = definePanel({ id: "pair", title: () => "Pairing", Body });
    expect(panelFor("pair")).toBe(panel);
  });

  it("replaces a panel defined again under the same id", () => {
    definePanel({ id: "new", title: () => "first", Body });
    definePanel({ id: "new", title: () => "second", Body });
    expect(panelFor("new")?.title(props)).toBe("second");
  });

  it("lists the tab panels that apply to a session, in tab order", () => {
    // Defined out of order, so the sort is what orders them.
    definePanel({ id: "info", title: () => "Info", tab: { order: 3, label: "info" }, Body });
    definePanel({
      id: "changes",
      title: () => "Changes",
      tab: { order: 2, label: "changes" },
      appliesTo: (s) => s.claude,
      Body,
    });
    definePanel({
      id: "turns",
      title: () => "Conversation",
      tab: { order: 1, label: "conversation" },
      appliesTo: (s) => s.claude,
      Body,
    });
    definePanel({
      id: "mockup",
      title: () => "Mockup",
      tab: { order: 4, label: "mockup" },
      appliesTo: () => false,
      Body,
    });
    // A panel with no tab is never a session tab.
    definePanel({ id: "flows", title: () => "Flows", Body });

    expect(sessionTabs(aSession({ claude: true })).map((p) => p.id)).toEqual([
      "turns",
      "changes",
      "info",
    ]);
    expect(sessionTabs(aSession({ claude: false })).map((p) => p.id)).toEqual(["info"]);
  });
});
