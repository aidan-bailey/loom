import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { Snapshot } from "@/api/gen";
import { defineCommand, type CommandContext } from "@/app/commands";
import { ConfirmSheet, confirmOpen, testSeam } from "@/app/ConfirmSheet";
import { useDesktop } from "@/app/desktopState";
import { definePanel } from "@/app/panels";
import { fixtureSnapshot } from "@/fixtures";
import { useFleet } from "@/store/fleet";
import "@/commands/view";

import { Palette } from "./Palette";
import { usePalette } from "./paletteState";

const runSpy = vi.fn();
const killSpy = vi.fn();
const seen: { open: boolean; ctx: CommandContext }[] = [];

defineCommand({
  id: "test.palette.run",
  title: "Zebra run",
  palette: true,
  when: () => true,
  run: (ctx) => {
    seen.push({ open: usePalette.getState().open, ctx });
    runSpy();
    return Promise.resolve();
  },
});

defineCommand({
  id: "test.palette.never",
  title: "Aardvark never",
  palette: true,
  when: () => false,
  run: () => Promise.resolve(),
});

defineCommand({
  id: "test.palette.kill",
  title: "Zebra kill",
  palette: true,
  when: () => true,
  confirm: () => ({ title: "Kill zebra", body: "b", action: "Kill", destructive: true }),
  run: () => {
    killSpy();
    return Promise.resolve();
  },
});

definePanel({
  id: "turns",
  title: () => "Conversation",
  tab: { order: 1, label: "conversation" },
  Body: () => null,
});
definePanel({
  id: "info",
  title: () => "Info",
  tab: { order: 3, label: "info" },
  Body: () => null,
});

function onlySession3(): Snapshot {
  return {
    ...fixtureSnapshot(),
    sessions: Object.fromEntries(
      Object.entries(fixtureSnapshot().sessions ?? {}).map(([workspace, list]) => [
        workspace,
        (list ?? []).filter((s) => s.id === 3),
      ]),
    ),
  };
}

function setup(hash = "#/s/3", over: Partial<ReturnType<typeof useFleet.getState>> = {}): void {
  window.history.replaceState(null, "", hash);
  useFleet.setState({
    snapshot: fixtureSnapshot(),
    stale: false,
    link: "live",
    updated: Date.now(),
    ...over,
  });
  useDesktop.setState({ sidebar: true, inspector: true });
  render(
    <>
      <Palette />
      <ConfirmSheet variant="dialog" />
    </>,
  );
  act(() => usePalette.getState().setOpen(true));
}

afterEach(() => {
  act(() => usePalette.getState().setOpen(false));
  const id = testSeam.openId();
  if (id !== null) act(() => testSeam.answer(id, null));
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  window.history.replaceState(null, "", window.location.pathname);
  runSpy.mockClear();
  killSpy.mockClear();
  seen.length = 0;
});

const items = (): HTMLElement[] =>
  Array.from(document.querySelectorAll<HTMLElement>('[data-slot="palette-item"]'));
const titles = (): string[] => items().map((el) => el.firstElementChild?.textContent ?? "");
const input = (): HTMLInputElement =>
  document.querySelector<HTMLInputElement>('[data-slot="palette-input"]')!;
const labels = (): string[] =>
  ["Sessions", "Commands", "Places"].filter((l) =>
    Array.from(document.querySelectorAll("[data-slot=palette] *")).some(
      (el) => el.children.length === 0 && el.textContent === l,
    ),
  );

describe("Palette", () => {
  it("draws nothing while closed", () => {
    window.history.replaceState(null, "", "#/s/3");
    useFleet.setState({
      snapshot: fixtureSnapshot(),
      stale: false,
      link: "live",
      updated: Date.now(),
    });
    render(<Palette />);
    expect(document.querySelector('[data-slot="palette"]')).toBeNull();
  });

  it("draws a dialog named for the palette with the input focused", async () => {
    setup();
    const dialog = screen.getByRole("dialog", { name: "Command palette" });
    expect(dialog).toHaveAttribute("data-slot", "palette");
    await waitFor(() => expect(input()).toHaveFocus());
  });

  it("with an empty query lists the sessions that need you, then the commands, then the places", () => {
    setup();
    const text = document.querySelector('[data-slot="palette"]')!.textContent ?? "";
    expect(text.indexOf("Sessions")).toBeGreaterThanOrEqual(0);
    expect(text.indexOf("Sessions")).toBeLessThan(text.indexOf("Commands"));
    expect(text.indexOf("Commands")).toBeLessThan(text.indexOf("Places"));
    const kinds = items().map((el) => el.getAttribute("data-kind"));
    const order = ["session", "command", "place"];
    const ranks = kinds.map((k) => order.indexOf(k ?? ""));
    expect([...ranks].sort((a, b) => a - b)).toEqual(ranks);
    expect(ranks).not.toContain(-1);
    expect(titles().filter((_, i) => kinds[i] === "session")).toEqual(["flow-docs", "sandbox-ask"]);
    const places = titles().filter((_, i) => kinds[i] === "place");
    for (const name of ["Fleet", "New session", "Settings", "conversation", "info"]) {
      expect(places).toContain(name);
    }
    expect(labels()).toEqual(["Sessions", "Commands", "Places"]);
  });

  it("draws each item with its kind", async () => {
    setup();
    for (const el of items()) {
      expect(["session", "command", "place"]).toContain(el.getAttribute("data-kind"));
    }
    await userEvent.type(input(), "plain-shell");
    const session = items().find((el) => el.getAttribute("data-kind") === "session");
    expect(session).toHaveTextContent("plain-shell");
    expect(session).toHaveTextContent("loom · aidanb/plain-shell");
  });

  it("a query narrows the items as paletteGroups does", async () => {
    setup();
    await userEvent.type(input(), "plain-shell");
    expect(titles()).toEqual(["plain-shell"]);
    expect(items()[0]).toHaveAttribute("data-kind", "session");
    expect(labels()).toEqual(["Sessions"]);
  });

  it("lists what paletteGroups matches by subsequence, which the list's own filter would drop", async () => {
    setup();
    await userEvent.type(input(), "zbrn");
    expect(titles()).toContain("Zebra run");
  });

  it("Escape returns focus to the element that had it before the palette opened", async () => {
    const behind = document.createElement("input");
    behind.setAttribute("aria-label", "behind");
    document.body.appendChild(behind);
    behind.focus();
    setup();
    await waitFor(() => expect(input()).toHaveFocus());
    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(behind).toHaveFocus());
    behind.remove();
  });

  it("reopened before the closing popup unmounts, starts with an empty query", async () => {
    setup();
    await userEvent.type(input(), "plain-shell");
    expect(input()).toHaveValue("plain-shell");
    act(() => usePalette.getState().setOpen(false));
    act(() => usePalette.getState().setOpen(true));
    expect(input()).toHaveValue("");
  });

  it("starts each opening with an empty query", async () => {
    setup();
    await userEvent.type(input(), "plain-shell");
    const empty = ["flow-docs", "sandbox-ask"];
    act(() => usePalette.getState().setOpen(false));
    await waitFor(() => expect(document.querySelector('[data-slot="palette"]')).toBeNull());
    act(() => usePalette.getState().setOpen(true));
    expect(input()).toHaveValue("");
    expect(titles().filter((_, i) => items()[i]?.getAttribute("data-kind") === "session")).toEqual(
      empty,
    );
  });

  it("Esc in the input with a query closes the palette", async () => {
    setup();
    await userEvent.type(input(), "plain-shell");
    await userEvent.keyboard("{Escape}");
    expect(usePalette.getState().open).toBe(false);
    await waitFor(() => expect(document.querySelector('[data-slot="palette"]')).toBeNull());
  });

  it("Esc with an empty query closes it too", async () => {
    setup();
    await userEvent.keyboard("{Escape}");
    expect(usePalette.getState().open).toBe(false);
    await waitFor(() => expect(document.querySelector('[data-slot="palette"]')).toBeNull());
  });

  it("Enter on a session goes to its link and closes the palette", async () => {
    setup();
    await userEvent.type(input(), "plain-shell");
    await userEvent.keyboard("{Enter}");
    expect(window.location.hash).toBe("#/s/3");
    expect(usePalette.getState().open).toBe(false);
  });

  it("a click on a session does the same", async () => {
    setup();
    await userEvent.type(input(), "sandbox-ask");
    const only = items();
    expect(only).toHaveLength(1);
    await userEvent.click(only[0]);
    expect(window.location.hash).toBe("#/s/2");
    expect(usePalette.getState().open).toBe(false);
  });

  it("Enter on a place goes to its link and closes the palette", async () => {
    setup();
    await userEvent.type(input(), "Settings");
    await userEvent.keyboard("{Enter}");
    expect(window.location.hash).toBe("#/settings");
    expect(usePalette.getState().open).toBe(false);
  });

  it("Enter on a tab place goes to its link", async () => {
    setup("#/s/3", { snapshot: onlySession3() });
    await userEvent.type(input(), "info");
    expect(titles()).toEqual(["info"]);
    expect(items()[0]).toHaveAttribute("data-kind", "place");
    await userEvent.keyboard("{Enter}");
    expect(window.location.hash).toBe("#/s/3/info");
    expect(usePalette.getState().open).toBe(false);
  });

  it("Enter on a command closes the palette, then runs it through runCommand with the route's context", async () => {
    setup();
    await userEvent.type(input(), "Zebra run");
    await userEvent.keyboard("{Enter}");
    await waitFor(() => expect(runSpy).toHaveBeenCalledTimes(1));
    expect(seen[0]?.open).toBe(false);
    expect(seen[0]?.ctx.session?.id).toBe(3);
    expect(seen[0]?.ctx.panel).toBe("turns");
    expect(window.location.hash).toBe("#/s/3");
  });

  it("a click on a command runs it the same way", async () => {
    setup();
    await userEvent.type(input(), "Zebra run");
    await userEvent.click(items()[0]);
    await waitFor(() => expect(runSpy).toHaveBeenCalledTimes(1));
    expect(usePalette.getState().open).toBe(false);
  });

  it("a command with a confirmation asks first, with the palette already closed", async () => {
    setup();
    await userEvent.type(input(), "Zebra kill");
    await userEvent.keyboard("{Enter}");
    await waitFor(() => expect(confirmOpen()).toBe(true));
    expect(killSpy).not.toHaveBeenCalled();
    expect(usePalette.getState().open).toBe(false);
    act(() => testSeam.answer(testSeam.openId() as number, ""));
    await waitFor(() => expect(killSpy).toHaveBeenCalledTimes(1));
  });

  it("leaves focus in the confirmation, not on the page behind the palette", async () => {
    window.history.replaceState(null, "", "#/s/3");
    const behind = document.createElement("input");
    behind.setAttribute("aria-label", "behind");
    document.body.appendChild(behind);
    behind.focus();
    setup();
    await userEvent.type(input(), "Zebra kill");
    await userEvent.keyboard("{Enter}");
    const sheet = await screen.findByRole("dialog", { name: "Kill zebra" });
    const cancel = within(sheet).getByRole("button", { name: "Cancel" });
    await waitFor(() => expect(cancel).toHaveFocus());
    await act(() => new Promise((r) => setTimeout(r, 100)));
    expect(cancel).toHaveFocus();
    expect(behind).not.toHaveFocus();
    behind.remove();
  });

  it("a command whose when fails is a disabled item that does nothing", async () => {
    setup();
    await userEvent.type(input(), "Aardvark never");
    const item = items()[0];
    expect(item).toHaveAttribute("data-disabled");
    await userEvent.keyboard("{Enter}");
    await userEvent.click(item);
    expect(usePalette.getState().open).toBe(true);
    expect(seen).toHaveLength(0);
  });

  it("while the snapshot is stale, a session command is disabled and a view toggle still runs", async () => {
    setup("#/s/3", { stale: true, link: "offline" });
    await userEvent.type(input(), "Zebra run");
    expect(items()[0]).toHaveAttribute("data-disabled");
    await userEvent.keyboard("{Enter}");
    expect(runSpy).not.toHaveBeenCalled();
    expect(usePalette.getState().open).toBe(true);

    await userEvent.clear(input());
    await userEvent.type(input(), "sidebar");
    const toggle = items().find((el) => el.textContent?.includes("Show or hide the sidebar"));
    expect(toggle).toBeDefined();
    expect(toggle).not.toHaveAttribute("data-disabled");
    await userEvent.keyboard("{Enter}");
    expect(useDesktop.getState().sidebar).toBe(false);
    expect(usePalette.getState().open).toBe(false);
  });

  it("the popup carries palette, not dialog-content", () => {
    setup();
    expect(document.querySelector('[data-slot="palette"]')).not.toBeNull();
    expect(document.querySelector('[data-slot="dialog-content"]')).toBeNull();
  });
});
