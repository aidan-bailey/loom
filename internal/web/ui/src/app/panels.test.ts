import { describe, expect, it, vi } from "vitest";

import type { Session } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";

import {
  definePanel,
  panelFor,
  sessionTabs,
  tabOrder,
  type HostFacts,
  type PanelId,
  type PanelProps,
} from "./panels";

const Body = (): null => null;
const props: PanelProps = { sessionId: undefined, params: {}, search: {} };
const host: HostFacts = { terminal: false };

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

    expect(sessionTabs(aSession({ claude: true }), host).map((p) => p.id)).toEqual([
      "turns",
      "changes",
      "info",
    ]);
    expect(sessionTabs(aSession({ claude: false }), host).map((p) => p.id)).toEqual(["info"]);
  });

  it("hands appliesTo the host's facts as its second argument", () => {
    definePanel({ id: "info", title: () => "Info", tab: { order: 3, label: "info" }, Body });
    definePanel({
      id: "mockup",
      title: () => "Mockup",
      tab: { order: 4, label: "mockup" },
      appliesTo: (session, facts) => facts.terminal && session.claude,
      Body,
    });
    const at = (facts: HostFacts, claude: boolean): string[] =>
      sessionTabs(aSession({ claude }), facts).map((p) => p.id);

    expect(at({ terminal: true }, true)).toContain("mockup");
    expect(at({ terminal: false }, true)).not.toContain("mockup");
    expect(at({ terminal: true }, false)).not.toContain("mockup");
  });

  it("lists every tab panel in tab order, whether or not it applies", () => {
    // Defined out of order, so the sort is what orders them.
    definePanel({
      id: "flow",
      title: () => "Flow",
      tab: { order: 5, label: "flow" },
      appliesTo: () => false,
      Body,
    });
    definePanel({ id: "info", title: () => "Info", tab: { order: 3, label: "info" }, Body });
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
    definePanel({
      id: "changes",
      title: () => "Changes",
      tab: { order: 2, label: "changes" },
      Body,
    });
    // A panel with no tab is not in it.
    definePanel({ id: "settings", title: () => "Settings", Body });

    expect(tabOrder()).toEqual(["turns", "changes", "info", "mockup", "flow"]);
  });

  it("has a panel for every PanelId once @/panels is imported", async () => {
    // A fresh registry that only the real panel modules fill: they import
    // @/app/panels, which resolves to the same fresh module as ./panels.
    vi.resetModules();
    const registry = await import("./panels");
    expect(registry.panelFor("fleet")).toBeUndefined();
    await import("@/panels");

    // Typed so a PanelId added later without a line here fails the typecheck.
    const ids: Record<PanelId, true> = {
      fleet: true,
      turns: true,
      info: true,
      changes: true,
      terminal: true,
      mockup: true,
      mockupFull: true,
      agent: true,
      job: true,
      flow: true,
      flowStory: true,
      flowDoc: true,
      flows: true,
      flowBoard: true,
      new: true,
      settings: true,
      pair: true,
    };
    for (const id of Object.keys(ids) as PanelId[]) {
      expect(registry.panelFor(id)?.id).toBe(id);
    }

    // The terminal is the third tab, after changes; info, mockup and flow follow it.
    expect(registry.tabOrder()).toEqual(["turns", "changes", "terminal", "info", "mockup", "flow"]);
    // It applies by the host's switch and the session's state.
    const tabsOf = (session: Session, host: HostFacts): string[] =>
      registry.sessionTabs(session, host).map((p) => p.id);
    const running = aSession({ claude: true, status: "Running", group: "working" });
    expect(tabsOf(running, { terminal: true })).toContain("terminal");
    expect(tabsOf(running, { terminal: false })).not.toContain("terminal");
    const paused = aSession({ status: "Paused", group: "paused" });
    expect(tabsOf(paused, { terminal: true })).not.toContain("terminal");
  }, 30_000);
});
