import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import type { PanelId } from "./panels";
import {
  goToPairing,
  hrefFor,
  normalizeHash,
  panelProps,
  routeOf,
  settleHash,
  useRoute,
  watchHash,
  type RouteState,
} from "./routes";

afterEach(() => {
  window.history.replaceState(null, "", window.location.pathname);
});

function route(
  panel: PanelId,
  params: Record<string, string> = {},
  search: Record<string, string> = {},
): RouteState {
  return { panel, params, search };
}

describe("normalizeHash", () => {
  it.each([
    ["#s/5", "#/s/5"],
    ["#s/5/info", "#/s/5/info"],
    ["#s/5/flow?plan=p.md", "#/s/5/flow?plan=p.md"],
    ["#flows", "#/flows"],
    ["#new", "#/new"],
    ["#s/5/turns", "#/s/5"],
    ["#/s/5/turns", "#/s/5"],
    ["#/s/50/turns", "#/s/50"],
    ["#/s/5", "#/s/5"],
    ["#/s/5/info", "#/s/5/info"],
    ["#/", "#/"],
    ["#", "#"],
    ["", ""],
    // Only the exact old address is rewritten.
    ["#/s/5/turns/full", "#/s/5/turns/full"],
    ["#/s/5/turnsx", "#/s/5/turnsx"],
  ])("%j becomes %j", (hash, want) => {
    expect(normalizeHash(hash)).toBe(want);
  });
});

describe("routeOf", () => {
  const cases: [string, RouteState][] = [
    ["", route("fleet")],
    ["#", route("fleet")],
    ["#/", route("fleet")],
    ["#s/5", route("turns", { id: "5" })],
    ["#/s/5", route("turns", { id: "5" })],
    // The conversation's old address, and a push link (`/#s/<id>`).
    ["#s/5/turns", route("turns", { id: "5" })],
    ["#/s/5/turns", route("turns", { id: "5" })],
    ["#s/5/info", route("info", { id: "5" })],
    ["#/s/5/info", route("info", { id: "5" })],
    ["#s/5/diff", route("changes", { id: "5" })],
    // A push link while the session's mockup is fresh (`/#s/<id>/mockup`).
    ["#s/5/mockup", route("mockup", { id: "5" })],
    ["#/s/5/mockup", route("mockup", { id: "5" })],
    ["#s/5/mockup/full", route("mockupFull", { id: "5" })],
    ["#/s/5/mockup/full", route("mockupFull", { id: "5" })],
    ["#s/5/agent/a1_b-2", route("agent", { id: "5", agent: "a1_b-2" })],
    ["#s/5/job/12", route("job", { id: "5", job: "12" })],
    ["#s/5/flow", route("flow", { id: "5" }, { plan: "" })],
    ["#s/5/flow?plan=docs%2Fp.md", route("flow", { id: "5" }, { plan: "docs/p.md" })],
    ["#/s/5/flow/3?plan=p", route("flowStory", { id: "5", story: "3" }, { plan: "p" })],
    [
      "#s/5/flow/doc/spec.md?plan=p&at=abc",
      route("flowDoc", { id: "5", doc: "spec.md" }, { plan: "p", at: "abc" }),
    ],
    ["#s/5/flow/doc/spec.md", route("flowDoc", { id: "5", doc: "spec.md" }, { plan: "", at: "" })],
    // `flow/doc` alone is a story called "doc", as in the classic parser.
    ["#s/5/flow/doc", route("flowStory", { id: "5", story: "doc" }, { plan: "" })],
    ["#flows", route("flows")],
    ["#/flows", route("flows")],
    ["#new", route("new")],
    ["#settings", route("settings")],
    ["#/settings", route("settings")],
    ["#pair", route("pair")],
    ["#/pair", route("pair")],
  ];

  it.each(cases)("%j opens %j", (hash, want) => {
    expect(routeOf(hash)).toEqual(want);
  });

  it.each([
    "#nonsense",
    "#constructor",
    "#s/x",
    "#s/5/agent/bad id",
    "#s/5/agent/a/b",
    "#s/5/job/0",
    "#s/5/job/x",
    // `full` belongs to the mockup alone.
    "#s/5/full",
    "#s/5/info/full",
    "#s/5/turns/full",
    "#/s/5/unknown",
  ])("%j is the fleet", (hash) => {
    expect(routeOf(hash)).toEqual(route("fleet"));
  });
});

describe("hrefFor", () => {
  it.each<[PanelId, Record<string, string>, Record<string, string>, string]>([
    ["fleet", {}, {}, "#/"],
    ["turns", { id: "5" }, {}, "#/s/5"],
    ["info", { id: "5" }, {}, "#/s/5/info"],
    ["changes", { id: "5" }, {}, "#/s/5/diff"],
    ["mockup", { id: "5" }, {}, "#/s/5/mockup"],
    ["mockupFull", { id: "5" }, {}, "#/s/5/mockup/full"],
    ["agent", { id: "5", agent: "a1" }, {}, "#/s/5/agent/a1"],
    ["job", { id: "5", job: "12" }, {}, "#/s/5/job/12"],
    ["flow", { id: "5" }, {}, "#/s/5/flow"],
    ["flow", { id: "5" }, { plan: "docs/p.md" }, "#/s/5/flow?plan=docs%2Fp.md"],
    ["flowStory", { id: "5", story: "3" }, { plan: "p" }, "#/s/5/flow/3?plan=p"],
    [
      "flowDoc",
      { id: "5", doc: "spec.md" },
      { plan: "p", at: "abc" },
      "#/s/5/flow/doc/spec.md?plan=p&at=abc",
    ],
    [
      "flowDoc",
      { id: "5", doc: "spec.md" },
      { plan: "p", at: "" },
      "#/s/5/flow/doc/spec.md?plan=p",
    ],
    ["flows", {}, {}, "#/flows"],
    ["new", {}, {}, "#/new"],
    ["settings", {}, {}, "#/settings"],
    ["pair", {}, {}, "#/pair"],
  ])("%s %j %j is %s", (panel, params, search, want) => {
    expect(hrefFor(panel, params, search)).toBe(want);
  });

  it("opens the panel and path values it was built from", () => {
    const panels: [PanelId, Record<string, string>, Record<string, string>][] = [
      ["fleet", {}, {}],
      ["turns", { id: "5" }, {}],
      ["info", { id: "5" }, {}],
      ["changes", { id: "5" }, {}],
      ["mockup", { id: "5" }, {}],
      ["mockupFull", { id: "5" }, {}],
      ["agent", { id: "5", agent: "a-1" }, {}],
      ["job", { id: "5", job: "7" }, {}],
      ["flow", { id: "5" }, { plan: "docs/p.md" }],
      ["flowStory", { id: "5", story: "A5" }, { plan: "docs/p.md" }],
      ["flowDoc", { id: "5", doc: "a b.md" }, { plan: "docs/p.md", at: "x y" }],
      ["flows", {}, {}],
      ["new", {}, {}],
      ["settings", {}, {}],
      ["pair", {}, {}],
    ];
    for (const [panel, params, search] of panels) {
      const got = routeOf(hrefFor(panel, params, search));
      expect(got.panel).toBe(panel);
      expect(got.params).toEqual(params);
      expect(got.search).toMatchObject(search);
    }
  });
});

describe("panelProps", () => {
  it("takes the session from the id, and none without one", () => {
    expect(panelProps(route("info", { id: "12" }))).toEqual({
      sessionId: 12,
      params: { id: "12" },
      search: {},
    });
    expect(panelProps(route("settings")).sessionId).toBeUndefined();
  });
});

describe("settleHash", () => {
  it("rewrites a slashless hash and the old conversation address in place", () => {
    window.history.replaceState(null, "", "#s/5/turns");
    settleHash();
    expect(window.location.hash).toBe("#/s/5");

    window.history.replaceState(null, "", "#flows");
    settleHash();
    expect(window.location.hash).toBe("#/flows");
  });

  it("leaves a hash already in the app's form, and no hash, alone", () => {
    window.history.replaceState(null, "", "#/s/5/info");
    settleHash();
    expect(window.location.hash).toBe("#/s/5/info");

    window.history.replaceState(null, "", window.location.pathname);
    settleHash();
    expect(window.location.hash).toBe("");
  });
});

describe("watchHash", () => {
  it("rewrites a hash set while it watches, and nothing after it stops", () => {
    const stop = watchHash();
    window.history.replaceState(null, "", "#s/5/turns");
    window.dispatchEvent(new HashChangeEvent("hashchange"));
    expect(window.location.hash).toBe("#/s/5");

    stop();
    window.history.replaceState(null, "", "#s/6/turns");
    window.dispatchEvent(new HashChangeEvent("hashchange"));
    expect(window.location.hash).toBe("#s/6/turns");
  });
});

describe("goToPairing", () => {
  it("opens the pairing route", () => {
    goToPairing();
    expect(window.location.hash).toBe("#/pair");
  });
});

describe("useRoute", () => {
  it("follows the hash", () => {
    window.history.replaceState(null, "", "#/s/5/info");
    const { result } = renderHook(() => useRoute());
    expect(result.current).toEqual({ panel: "info", params: { id: "5" }, search: {} });

    act(() => {
      window.history.replaceState(null, "", "#/s/6/diff");
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
    expect(result.current).toEqual({ panel: "changes", params: { id: "6" }, search: {} });
  });

  it("resolves a hash in the classic form", () => {
    window.history.replaceState(null, "", "#s/5/mockup");
    const { result } = renderHook(() => useRoute());
    expect(result.current.panel).toBe("mockup");
  });
});
