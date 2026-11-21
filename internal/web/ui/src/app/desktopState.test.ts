import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DESKTOP_KEY, readDesktopPrefs, useDesktop } from "./desktopState";

const DEFAULT = { sidebar: true, inspector: true, slots: [] };

// fromOtherWindow plays the event a browser fires in this window when another window of the same
// origin writes DESKTOP_KEY (it never fires in the window that wrote).
function fromOtherWindow(newValue: string | null): void {
  window.dispatchEvent(new StorageEvent("storage", { key: DESKTOP_KEY, newValue }));
}

beforeEach(() => {
  window.localStorage.clear();
  // The default, through the store's own listener, so what it kept from an earlier test goes too.
  fromOtherWindow(null);
});

afterEach(() => {
  vi.restoreAllMocks();
});

function stored(): unknown {
  return JSON.parse(window.localStorage.getItem(DESKTOP_KEY) ?? "null");
}

// blockStorage makes every read and write of the storage throw, as a browser that blocks storage
// does; the afterEach puts the real methods back.
function blockStorage(): void {
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
    throw new Error("blocked");
  });
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw new Error("blocked");
  });
}

describe("readDesktopPrefs", () => {
  it.each<[string, string | null]>([
    ["nothing stored", null],
    ["an empty string", ""],
    ["text that is not JSON", "{not json"],
    ["JSON null", "null"],
    ["a number", "7"],
    ["a string", '"hello"'],
    ["an array", "[]"],
    ["no sidebar", '{"inspector":true,"slots":[]}'],
    ["a sidebar that is not a boolean", '{"sidebar":"yes","inspector":true,"slots":[]}'],
    ["no inspector", '{"sidebar":false,"slots":[]}'],
    ["an inspector that is not a boolean", '{"sidebar":false,"inspector":1,"slots":[]}'],
    ["no slots", '{"sidebar":false,"inspector":false}'],
    ["slots that is not a list", '{"sidebar":false,"inspector":false,"slots":{"tab":"info"}}'],
    ["a slot that is a number", '{"sidebar":false,"inspector":false,"slots":[3]}'],
    ["a slot that is null", '{"sidebar":false,"inspector":false,"slots":[null]}'],
    ["a slot with no tab", '{"sidebar":false,"inspector":false,"slots":[{}]}'],
    ["a slot whose tab is not a string", '{"sidebar":false,"inspector":false,"slots":[{"tab":3}]}'],
  ])("%s reads as the default", (_name, raw) => {
    expect(readDesktopPrefs(raw)).toEqual(DEFAULT);
  });

  it("reads a stored shape back as it is", () => {
    expect(
      readDesktopPrefs(
        '{"sidebar":false,"inspector":true,"slots":[{"tab":"changes"},{"tab":"info"}]}',
      ),
    ).toEqual({
      sidebar: false,
      inspector: true,
      slots: [{ tab: "changes" }, { tab: "info" }],
    });
    expect(readDesktopPrefs('{"sidebar":true,"inspector":false,"slots":[]}')).toEqual({
      sidebar: true,
      inspector: false,
      slots: [],
    });
  });

  it("keeps a tab name no panel has: only the shape is read here, and the inspector skips it", () => {
    expect(
      readDesktopPrefs('{"sidebar":true,"inspector":true,"slots":[{"tab":"nothing"}]}'),
    ).toEqual({
      sidebar: true,
      inspector: true,
      slots: [{ tab: "nothing" }],
    });
  });

  it("hands out a default of its own each time", () => {
    const first = readDesktopPrefs(null);
    first.slots.push({ tab: "info" });
    expect(readDesktopPrefs(null).slots).toEqual([]);
  });
});

describe("useDesktop", () => {
  it("sets the sidebar and the inspector, and writes the whole shape back each time", () => {
    useDesktop.getState().setSidebar(false);
    expect(useDesktop.getState().sidebar).toBe(false);
    expect(stored()).toEqual({ sidebar: false, inspector: true, slots: [] });

    useDesktop.getState().setInspector(false);
    expect(useDesktop.getState().inspector).toBe(false);
    expect(stored()).toEqual({ sidebar: false, inspector: false, slots: [] });
  });

  it("setTab adds the first slot when there is none", () => {
    useDesktop.getState().setTab("changes");
    expect(useDesktop.getState().slots).toEqual([{ tab: "changes" }]);
    expect(stored()).toEqual({ sidebar: true, inspector: true, slots: [{ tab: "changes" }] });
  });

  it("setTab sets the first slot's tab and leaves the others", () => {
    useDesktop.setState({ slots: [{ tab: "info" }, { tab: "flow" }] });
    useDesktop.getState().setTab("changes");
    expect(useDesktop.getState().slots).toEqual([{ tab: "changes" }, { tab: "flow" }]);
    expect(stored()).toEqual({
      sidebar: true,
      inspector: true,
      slots: [{ tab: "changes" }, { tab: "flow" }],
    });
  });

  it("starts from what is stored, once, when the module loads", async () => {
    window.localStorage.setItem(
      DESKTOP_KEY,
      '{"sidebar":false,"inspector":true,"slots":[{"tab":"info"}]}',
    );
    vi.resetModules();
    const fresh = await import("./desktopState");
    expect(fresh.useDesktop.getState()).toMatchObject({
      sidebar: false,
      inspector: true,
      slots: [{ tab: "info" }],
    });
  });

  it("starts at the default from a malformed stored value, not from its good parts", async () => {
    window.localStorage.setItem(DESKTOP_KEY, '{"sidebar":false,"inspector":false,"slots":"x"}');
    vi.resetModules();
    const fresh = await import("./desktopState");
    expect(fresh.useDesktop.getState()).toMatchObject(DEFAULT);
  });

  it("starts at the default and still sets when the storage throws", async () => {
    blockStorage();
    vi.resetModules();
    const fresh = await import("./desktopState");
    expect(fresh.useDesktop.getState()).toMatchObject(DEFAULT);
    expect(() => {
      fresh.useDesktop.getState().setSidebar(false);
    }).not.toThrow();
    expect(fresh.useDesktop.getState().sidebar).toBe(false);
  });

  it("follows another window's write, and a later set writes that choice back unchanged", () => {
    fromOtherWindow('{"sidebar":false,"inspector":true,"slots":[{"tab":"info"}]}');
    expect(useDesktop.getState()).toMatchObject({
      sidebar: false,
      inspector: true,
      slots: [{ tab: "info" }],
    });

    useDesktop.getState().setInspector(false);
    expect(stored()).toEqual({ sidebar: false, inspector: false, slots: [{ tab: "info" }] });
  });

  it("reads another window's malformed write as the default, and ignores another key's", () => {
    useDesktop.setState({ sidebar: false });
    window.dispatchEvent(
      new StorageEvent("storage", { key: "loom.other", newValue: '{"sidebar":true}' }),
    );
    expect(useDesktop.getState().sidebar).toBe(false);

    fromOtherWindow('{"sidebar":false,"inspector":false,"slots":"x"}');
    expect(useDesktop.getState()).toMatchObject(DEFAULT);
  });

  it("writes back what this layout does not own: other top-level keys and a slot's other fields", async () => {
    window.localStorage.setItem(
      DESKTOP_KEY,
      JSON.stringify({
        sidebar: true,
        inspector: true,
        slots: [
          { tab: "info", pinned: true },
          { tab: "flow", width: 300 },
        ],
        theme: { name: "dusk" },
      }),
    );
    vi.resetModules();
    const fresh = await import("./desktopState");

    fresh.useDesktop.getState().setSidebar(false);
    fresh.useDesktop.getState().setTab("changes");
    expect(stored()).toEqual({
      sidebar: false,
      inspector: true,
      slots: [
        { tab: "changes", pinned: true },
        { tab: "flow", width: 300 },
      ],
      theme: { name: "dusk" },
    });
    expect(fresh.useDesktop.getState()).toMatchObject({
      sidebar: false,
      slots: [{ tab: "changes" }, { tab: "flow" }],
    });
  });

  it("keeps what another window's write holds beyond this layout's fields", () => {
    fromOtherWindow(
      '{"sidebar":true,"inspector":true,"slots":[{"tab":"info","pinned":true}],"theme":"dusk"}',
    );
    useDesktop.getState().setInspector(false);
    expect(stored()).toEqual({
      sidebar: true,
      inspector: false,
      slots: [{ tab: "info", pinned: true }],
      theme: "dusk",
    });
  });
});
