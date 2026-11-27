import { describe, expect, it } from "vitest";

import type { PanelId } from "@/app/panels";

import { inspectorTab } from "./inspector";

// Every tab panel in order, and those that apply to the session under test (no mockup or flow).
const ORDER: PanelId[] = ["turns", "changes", "mockup", "info", "flow"];
const TABS: PanelId[] = ["turns", "changes", "info"];

// The same with the terminal, which applies to the session under test (spec §58).
const ORDER_T: PanelId[] = ["turns", "changes", "terminal", "info", "mockup", "flow"];
const TABS_T: PanelId[] = ["turns", "changes", "terminal", "info"];

describe("inspectorTab", () => {
  it("keeps the chosen tab when it applies and is not the middle's", () => {
    expect(inspectorTab(ORDER, TABS, "turns", "changes")).toBe("changes");
    expect(inspectorTab(ORDER, TABS, "turns", "info")).toBe("info");
  });

  it("takes the next applicable tab after the chosen one when it is the middle's", () => {
    expect(inspectorTab(ORDER, TABS, "turns", "turns")).toBe("changes");
    expect(inspectorTab(ORDER, TABS, "changes", "changes")).toBe("info");
  });

  it("wraps past the end of the order", () => {
    expect(inspectorTab(ORDER, TABS, "info", "info")).toBe("turns");
  });

  it("skips the middle's tab on the walk", () => {
    expect(inspectorTab(ORDER, TABS, "info", "mockup")).toBe("turns");
    expect(inspectorTab(ORDER, TABS, "turns", "flow")).toBe("changes");
  });

  it("takes the next applicable tab after a chosen one that does not apply", () => {
    expect(inspectorTab(ORDER, TABS, "turns", "mockup")).toBe("info");
  });

  it("falls to the first applicable tab that is not the middle's with no choice", () => {
    expect(inspectorTab(ORDER, TABS, "turns", undefined)).toBe("changes");
    expect(inspectorTab(ORDER, TABS, "changes", undefined)).toBe("turns");
  });

  it("falls to the first applicable tab when the choice names no panel", () => {
    expect(inspectorTab(ORDER, TABS, "turns", "bogus")).toBe("changes");
    expect(inspectorTab(ORDER, TABS, "turns", "")).toBe("changes");
  });

  it("is undefined when nothing but the middle's tab applies", () => {
    expect(inspectorTab(ORDER, ["info"], "info", "info")).toBeUndefined();
    expect(inspectorTab(ORDER, [], "turns", undefined)).toBeUndefined();
  });

  it("draws the terminal when it is the stored choice and applies", () => {
    expect(inspectorTab(ORDER_T, TABS_T, "turns", "terminal")).toBe("terminal");
    expect(inspectorTab(ORDER_T, TABS_T, "info", "terminal")).toBe("terminal");
  });

  it("never falls back to the terminal: no choice, a choice that does not apply, the middle's choice", () => {
    // No stored choice.
    expect(inspectorTab(ORDER_T, TABS_T, "turns", undefined)).toBe("changes");
    expect(inspectorTab(ORDER_T, TABS_T, "changes", undefined)).toBe("turns");
    // A stored tab no panel has.
    expect(inspectorTab(ORDER_T, TABS_T, "turns", "bogus")).toBe("changes");
    // The walk after the middle's own choice passes the terminal.
    expect(inspectorTab(ORDER_T, TABS_T, "changes", "changes")).toBe("info");
    // The walk after a choice that does not apply passes it too (changes is not among the tabs).
    expect(inspectorTab(ORDER_T, ["turns", "terminal", "info"], "turns", "changes")).toBe("info");
    // The terminal and the middle's tab alone: nothing else to fall to.
    expect(inspectorTab(ORDER_T, ["changes", "terminal"], "changes", undefined)).toBeUndefined();
  });

  it("walks past the terminal when it is the middle's tab and the stored choice", () => {
    expect(inspectorTab(ORDER_T, TABS_T, "terminal", "terminal")).toBe("info");
  });

  it("walks past a stored terminal that does not apply", () => {
    expect(inspectorTab(ORDER_T, TABS, "turns", "terminal")).toBe("info");
  });

  it("draws nothing rather than the terminal when only it and the middle's tab apply", () => {
    expect(inspectorTab(ORDER_T, ["turns", "terminal"], "turns", undefined)).toBeUndefined();
    expect(inspectorTab(ORDER_T, ["turns", "terminal"], "turns", "terminal")).toBe("terminal");
  });

  it("leaves its arguments as they were", () => {
    const order = [...ORDER];
    const tabs = [...TABS];
    inspectorTab(ORDER, TABS, "turns", "turns");
    expect(ORDER).toEqual(order);
    expect(TABS).toEqual(tabs);
  });
});
