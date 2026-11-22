import { beforeEach, describe, expect, it } from "vitest";

import { commandById, runCommand, type CommandContext } from "@/app/commands";
import { DESKTOP_KEY, useDesktop } from "@/app/desktopState";

import "./view";

function ctx(over: Partial<CommandContext> = {}): CommandContext {
  return { session: undefined, link: "live", stale: false, panel: "fleet", ...over };
}

function command(id: string) {
  const found = commandById(id);
  if (found === undefined) throw new Error(`${id} is not registered`);
  return found;
}

beforeEach(() => {
  useDesktop.setState({ sidebar: true, inspector: true });
  window.localStorage.clear();
});

describe("the view toggles", () => {
  it("view.toggleSidebar is titled, offline, in the palette and always on", () => {
    const c = command("view.toggleSidebar");
    expect(c.title).toBe("Show or hide the sidebar");
    expect(c.offline).toBe(true);
    expect(c.palette).toBe(true);
    expect(c.when(ctx())).toBe(true);
    expect(c.when(ctx({ panel: "settings" }))).toBe(true);
  });

  it("view.toggleInspector is titled, offline, in the palette and always on", () => {
    const c = command("view.toggleInspector");
    expect(c.title).toBe("Show or hide the inspector");
    expect(c.offline).toBe(true);
    expect(c.palette).toBe(true);
    expect(c.when(ctx())).toBe(true);
    expect(c.when(ctx({ panel: "settings" }))).toBe(true);
  });

  it("view.toggleSidebar flips the sidebar and leaves the inspector", async () => {
    await runCommand(command("view.toggleSidebar"), ctx(), undefined);
    expect(useDesktop.getState().sidebar).toBe(false);
    expect(useDesktop.getState().inspector).toBe(true);
    await runCommand(command("view.toggleSidebar"), ctx(), undefined);
    expect(useDesktop.getState().sidebar).toBe(true);
  });

  it("view.toggleInspector flips the inspector and leaves the sidebar", async () => {
    await runCommand(command("view.toggleInspector"), ctx(), undefined);
    expect(useDesktop.getState().inspector).toBe(false);
    expect(useDesktop.getState().sidebar).toBe(true);
    await runCommand(command("view.toggleInspector"), ctx(), undefined);
    expect(useDesktop.getState().inspector).toBe(true);
  });

  it("a toggle runs while the snapshot is stale", async () => {
    const stale = ctx({ stale: true, link: "connecting" });
    await runCommand(command("view.toggleSidebar"), stale, undefined);
    await runCommand(command("view.toggleInspector"), stale, undefined);
    expect(useDesktop.getState().sidebar).toBe(false);
    expect(useDesktop.getState().inspector).toBe(false);
  });

  it("a toggle writes its choice to loom.desktop", async () => {
    await runCommand(command("view.toggleSidebar"), ctx(), undefined);
    const stored = JSON.parse(window.localStorage.getItem(DESKTOP_KEY) ?? "null") as {
      sidebar: boolean;
      inspector: boolean;
    } | null;
    expect(stored).toMatchObject({ sidebar: false, inspector: true });
  });
});
