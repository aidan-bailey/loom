import { describe, expect, it } from "vitest";

import type { PanelId } from "@/app/panels";

import { inspectorTab } from "./inspector";

// Every tab panel in order, and those that apply to the session under test (no mockup or flow).
const ORDER: PanelId[] = ["turns", "changes", "mockup", "info", "flow"];
const TABS: PanelId[] = ["turns", "changes", "info"];

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

  it("leaves its arguments as they were", () => {
    const order = [...ORDER];
    const tabs = [...TABS];
    inspectorTab(ORDER, TABS, "turns", "turns");
    expect(ORDER).toEqual(order);
    expect(TABS).toEqual(tabs);
  });
});
