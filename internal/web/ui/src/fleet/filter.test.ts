import { afterEach, describe, expect, it, vi } from "vitest";

import { fixtureSnapshot } from "@/fixtures";
import { shownWorkspaces } from "@/fleet/groups";

import { activeFilter, readFilter, useFilter } from "./filter";

afterEach(() => {
  vi.restoreAllMocks();
  window.localStorage.clear();
  useFilter.setState({ stored: readFilter() });
});

describe("readFilter", () => {
  it.each([
    [null, ""],
    ['""', ""],
    ['"2"', "2"],
    ["7", ""],
    ['["2"]', ""],
    ["not json", ""],
  ] as const)("reads the classic encoding: %s", (raw, expected) => {
    if (raw !== null) window.localStorage.setItem("loom.filter", raw);
    expect(readFilter()).toBe(expected);
  });

  it("reads an unreadable store as all", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("refused");
    });
    expect(readFilter()).toBe("");
  });
});

describe("useFilter", () => {
  it("starts from the stored choice", async () => {
    window.localStorage.setItem("loom.filter", '"2"');
    vi.resetModules();
    const fresh = await import("./filter");
    expect(fresh.useFilter.getState().stored).toBe("2");
  });

  it('pick keeps the choice and writes it as a JSON string, and "" writes all', () => {
    useFilter.getState().pick("2");
    expect(useFilter.getState().stored).toBe("2");
    expect(window.localStorage.getItem("loom.filter")).toBe('"2"');

    useFilter.getState().pick("");
    expect(useFilter.getState().stored).toBe("");
    expect(window.localStorage.getItem("loom.filter")).toBe('""');
  });

  it("pick keeps the choice for this launch when the store refuses", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("full");
    });
    expect(() => {
      useFilter.getState().pick("2");
    }).not.toThrow();
    expect(useFilter.getState().stored).toBe("2");
  });
});

describe("activeFilter", () => {
  const workspaces = shownWorkspaces(fixtureSnapshot());

  it("names a listed workspace and reads any other as all", () => {
    expect(workspaces.map((w) => String(w.id))).toEqual(expect.arrayContaining(["1", "2"]));
    expect(activeFilter("2", workspaces)).toBe("2");
    expect(activeFilter("9", workspaces)).toBe("");
    expect(activeFilter("", workspaces)).toBe("");
    expect(activeFilter("2", [])).toBe("");
  });
});
