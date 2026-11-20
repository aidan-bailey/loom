import { act, fireEvent, render, screen, within, type RenderResult } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";

import "@/commands";
import type { LaunchDefaults, Workspace } from "@/api/gen";
import { useLaunchDefaults } from "@/api/reads";
import { api } from "@/api/request";
import { panelFor } from "@/app/panels";
import { PanelHost } from "@/app/shell";
import { resetFleet, spyOnAdd } from "@/commands/testkit";
import { fixtureSnapshot } from "@/fixtures";
import { useFleet } from "@/store/fleet";
import { usePending } from "@/store/pending";

import { NewHead, newPanel } from "./index";
import NewSessionBody from "./NewSessionBody";

vi.mock("@/api/reads", async (original) => ({
  ...(await original<typeof import("@/api/reads")>()),
  useLaunchDefaults: vi.fn(),
}));

const DEFAULTS: Record<number, LaunchDefaults> = {
  1: {
    account: "",
    accounts: ["default", "work"],
    model: "opus",
    models: ["opus", "sonnet"],
    permission_mode: "",
    permission_modes: ["default", "acceptEdits"],
    effort: "high",
    efforts: ["low", "high"],
  },
  2: {
    account: "",
    accounts: null,
    model: "",
    models: ["opus", "sonnet"],
    permission_mode: "plan",
    permission_modes: ["plan", "default"],
    effort: "",
    efforts: ["low"],
  },
};

const W1: Workspace = { id: 1, name: "alpha", label: "Alpha", shown: true };
const W2: Workspace = { id: 2, name: "beta", label: "", shown: true };
const W3: Workspace = { id: 3, name: "gamma", label: "Gamma", shown: false };

type Link = ReturnType<typeof useFleet.getState>["link"];

const retry = vi.fn();
// Whether the mock answers while the stream is down too (the stale-gate test needs the defaults
// to be there regardless).
let answerAlways = false;
let spyCreate: MockInstance<typeof api.create>;
let assign: MockInstance<(url: string | URL) => void>;
let add: ReturnType<typeof spyOnAdd>;

function answer(workspace: number | undefined, live: boolean) {
  const defaults = workspace === undefined ? undefined : DEFAULTS[workspace];
  if (defaults !== undefined && (live || answerAlways)) {
    return { defaults, error: "", loading: false, retry };
  }
  return { defaults: undefined, error: "", loading: true, retry };
}

beforeEach(() => {
  answerAlways = false;
  retry.mockReset();
  vi.mocked(useLaunchDefaults).mockImplementation(answer);
  spyCreate = vi.spyOn(api, "create").mockResolvedValue({
    kind: "ok",
    status: 201,
    value: { id: 42, notices: null },
    notices: [],
  });
  assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);
  add = spyOnAdd();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.mocked(useLaunchDefaults).mockReset();
  usePending.getState().clear();
  resetFleet();
  window.localStorage.clear();
  window.history.replaceState(null, "", window.location.pathname);
});

function load(workspaces: Workspace[], link: Link = "live"): void {
  useFleet.setState({
    snapshot: { ...fixtureSnapshot(), workspaces },
    stale: link !== "live",
    link,
    updated: Date.now(),
  });
  window.history.replaceState(null, "", "#/new");
}

function show(
  workspaces: Workspace[],
  link: Link = "live",
): RenderResult & {
  user: ReturnType<typeof userEvent.setup>;
} {
  load(workspaces, link);
  const user = userEvent.setup();
  return { ...render(<NewSessionBody />), user };
}

const combo = (name: string): HTMLElement => screen.getByRole("combobox", { name });
const title = (): HTMLElement => screen.getByRole("textbox", { name: "Title" });
const prompt = (): HTMLElement => screen.getByRole("textbox", { name: "Prompt" });
const start = (): HTMLElement => screen.getByRole("button", { name: "Start session" });
const lastCall = () => vi.mocked(useLaunchDefaults).mock.lastCall;

async function pick(
  user: ReturnType<typeof userEvent.setup>,
  name: string,
  option: string,
): Promise<void> {
  await user.click(combo(name));
  await user.click(await screen.findByRole("option", { name: option }));
}

async function openOptions(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByRole("button", { name: "Launch options" }));
}

const optionNames = async (): Promise<string[]> =>
  within(await screen.findByRole("listbox"))
    .getAllByRole("option")
    .map((o) => o.textContent ?? "");

describe("NewSessionBody", () => {
  it("says Loading… until the first snapshot", () => {
    render(<NewSessionBody />);
    expect(screen.getByText("Loading…")).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Workspace" })).not.toBeInTheDocument();
  });

  it("starts on the fleet filter's workspace", () => {
    window.localStorage.setItem("loom.filter", JSON.stringify("2"));
    show([W1, W2]);
    expect(combo("Workspace")).toHaveTextContent("beta");
    expect(lastCall()?.slice(0, 2)).toEqual([2, true]);
  });

  it("falls back to the first shown workspace, then the first one", () => {
    const cases: [string, Workspace[], string][] = [
      ["", [W3, W1], "Alpha"],
      [JSON.stringify("9"), [W1, W2], "Alpha"],
      ["", [W3], "Gamma"],
    ];
    for (const [filter, workspaces, name] of cases) {
      window.localStorage.setItem("loom.filter", filter === "" ? JSON.stringify("") : filter);
      const view = show(workspaces);
      expect(combo("Workspace")).toHaveTextContent(name);
      view.unmount();
      resetFleet();
    }
  });

  it("a stored filter that is not a JSON string is read as none", () => {
    window.localStorage.setItem("loom.filter", "not json");
    show([W3, W1]);
    expect(combo("Workspace")).toHaveTextContent("Alpha");
  });

  it("groups the workspaces Active and Other workspaces when one is hidden, and lists them flat when none is", async () => {
    const grouped = show([W1, W3]);
    await grouped.user.click(combo("Workspace"));
    const list = await screen.findByRole("listbox");
    expect(within(list).getByText("Active")).toBeInTheDocument();
    expect(within(list).getByText("Other workspaces")).toBeInTheDocument();
    const groups = within(list).getAllByRole("group");
    expect(groups.map((g) => g.textContent)).toEqual(["ActiveAlpha", "Other workspacesGamma"]);
    grouped.unmount();
    resetFleet();

    const flat = show([W1, W2]);
    await flat.user.click(combo("Workspace"));
    expect(await optionNames()).toEqual(["Alpha", "beta"]);
    expect(screen.queryByText("Active")).not.toBeInTheDocument();
    expect(screen.queryByText("Other workspaces")).not.toBeInTheDocument();
  });

  it("says No workspace is open. and keeps Start session off with none", async () => {
    const { user } = show([]);
    expect(screen.getByText("No workspace is open.")).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Workspace" })).not.toBeInTheDocument();
    expect(lastCall()?.slice(0, 2)).toEqual([undefined, true]);
    await user.type(title(), "fix");
    expect(start()).toBeDisabled();
  });

  it("reads the defaults only while the stream is live", () => {
    const offline = show([W1], "offline");
    expect(lastCall()?.slice(0, 2)).toEqual([1, false]);
    expect(screen.queryByRole("button", { name: "Launch options" })).not.toBeInTheDocument();
    offline.unmount();
    resetFleet();
    show([W1], "live");
    expect(lastCall()?.slice(0, 2)).toEqual([1, true]);
  });

  it("Start session is off until the defaults, a title and a fitting prompt are there", async () => {
    vi.mocked(useLaunchDefaults).mockReturnValue({
      defaults: undefined,
      error: "",
      loading: true,
      retry,
    });
    const { user } = show([W1]);
    await user.type(title(), "fix");
    expect(start()).toBeDisabled();

    vi.mocked(useLaunchDefaults).mockImplementation(answer);
    await user.clear(title());
    await user.type(title(), "   ");
    expect(start()).toBeDisabled();
    await user.clear(title());
    await user.type(title(), "fix");
    expect(start()).toBeEnabled();

    fireEvent.change(prompt(), { target: { value: "a".repeat(65536) + "   " } });
    expect(start()).toBeEnabled();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    for (const text of ["a".repeat(65537), "é".repeat(32769)]) {
      fireEvent.change(prompt(), { target: { value: text } });
      expect(start()).toBeDisabled();
      expect(screen.getByRole("alert")).toHaveTextContent("Too long: a prompt is at most 64 KiB.");
    }
  });

  it("Start session is off while the snapshot is stale", async () => {
    answerAlways = true;
    const { user } = show([W1], "offline");
    await user.type(title(), "fix");
    expect(start()).toBeDisabled();
  });

  it("Start session goes off when a snapshot no longer lists the chosen workspace", async () => {
    const { user } = show([W1, W2]);
    await user.type(title(), "fix");
    expect(lastCall()?.slice(0, 2)).toEqual([1, true]);
    expect(start()).toBeEnabled();

    act(() => {
      useFleet.setState({ snapshot: { ...fixtureSnapshot(), workspaces: [W2] } });
    });
    expect(start()).toBeDisabled();
  });

  it("Launch options is closed at first and offers Account only when the defaults list accounts", async () => {
    const first = show([W1, W2]);
    expect(screen.queryByRole("combobox", { name: "Model" })).not.toBeInTheDocument();
    await openOptions(first.user);
    for (const name of ["Account", "Model", "Permission mode", "Effort"]) {
      expect(combo(name)).toBeInTheDocument();
    }
    first.unmount();
    resetFleet();

    window.localStorage.setItem("loom.filter", JSON.stringify("2"));
    const second = show([W1, W2]);
    await openOptions(second.user);
    expect(combo("Model")).toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: "Account" })).not.toBeInTheDocument();
  });

  it("a pick's list puts a missing default first and labels the empty one (default)", async () => {
    window.localStorage.setItem("loom.filter", JSON.stringify("2"));
    const two = show([W1, W2]);
    await openOptions(two.user);
    expect(combo("Model")).toHaveTextContent("(default)");
    await two.user.click(combo("Model"));
    expect(await optionNames()).toEqual(["(default)", "opus", "sonnet"]);
    two.unmount();
    resetFleet();

    window.localStorage.setItem("loom.filter", JSON.stringify("1"));
    const one = show([W1, W2]);
    await openOptions(one.user);
    await one.user.click(combo("Model"));
    expect(await optionNames()).toEqual(["opus", "sonnet"]);
    await one.user.keyboard("{Escape}");
    await one.user.click(combo("Permission mode"));
    expect(await optionNames()).toEqual(["(default)", "default", "acceptEdits"]);
  });

  it("sends the trimmed title, the prompt's end trimmed and only the picks that differ from their defaults", async () => {
    const { user } = show([W1]);
    await user.type(title(), "  fix the build ");
    fireEvent.change(prompt(), { target: { value: "go on\n\n" } });
    await openOptions(user);
    await pick(user, "Model", "sonnet");
    await user.click(start());
    expect(spyCreate).toHaveBeenCalledTimes(1);
    expect(spyCreate.mock.calls[0]?.[0]).toStrictEqual({
      workspace: 1,
      title: "fix the build",
      prompt: "go on",
      model: "sonnet",
    });
    expect(assign).toHaveBeenCalledWith("#/s/42");
  });

  it("changing the workspace resets the picks and keeps the title and prompt", async () => {
    const { user } = show([W1, W2]);
    await user.type(title(), "fix");
    fireEvent.change(prompt(), { target: { value: "go" } });
    await openOptions(user);
    await pick(user, "Model", "sonnet");
    expect(combo("Model")).toHaveTextContent("sonnet");

    await pick(user, "Workspace", "beta");
    expect(lastCall()?.slice(0, 2)).toEqual([2, true]);
    expect(combo("Model")).toHaveTextContent("(default)");
    expect(title()).toHaveValue("fix");
    expect(prompt()).toHaveValue("go");

    await pick(user, "Workspace", "Alpha");
    expect(combo("Model")).toHaveTextContent("opus");
    await user.click(start());
    expect(spyCreate.mock.calls[0]?.[0]).toStrictEqual({
      workspace: 1,
      title: "fix",
      prompt: "go",
    });
  });

  it("a stream that drops and returns leaves the picks as they are", async () => {
    const { user } = show([W1]);
    await openOptions(user);
    await pick(user, "Model", "sonnet");
    // The hook keeps the answer it holds while the stream is down.
    answerAlways = true;
    act(() => useFleet.getState().setLink("offline"));
    act(() => useFleet.getState().setLink("live"));
    expect(combo("Model")).toHaveTextContent("sonnet");
  });

  it("Enter in the title starts the session only when it is ready", async () => {
    const ready = show([W1]);
    await ready.user.type(title(), "fix{Enter}");
    expect(spyCreate).toHaveBeenCalledTimes(1);
    ready.unmount();
    resetFleet();
    spyCreate.mockClear();

    const blank = show([W1]);
    await blank.user.type(title(), "{Enter}");
    expect(spyCreate).not.toHaveBeenCalled();
    blank.unmount();
    resetFleet();

    answerAlways = true;
    const offline = show([W1], "offline");
    await offline.user.type(title(), "fix{Enter}");
    expect(spyCreate).not.toHaveBeenCalled();
  });

  it("a refused create keeps the form as typed", async () => {
    spyCreate.mockResolvedValue({ kind: "refused", status: 409, message: "Too many sessions." });
    const { user } = show([W1]);
    await user.type(title(), "fix");
    fireEvent.change(prompt(), { target: { value: "go on" } });
    await openOptions(user);
    await pick(user, "Model", "sonnet");
    await user.click(start());

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "Too many sessions.", type: "error" }),
    );
    expect(title()).toHaveValue("fix");
    expect(prompt()).toHaveValue("go on");
    expect(combo("Model")).toHaveTextContent("sonnet");
    expect(start()).toBeEnabled();
    expect(assign).not.toHaveBeenCalled();
  });

  it("a failed read says so and Retry asks again", async () => {
    vi.mocked(useLaunchDefaults).mockReturnValue({
      defaults: undefined,
      error: "network",
      loading: false,
      retry,
    });
    const { user } = show([W1]);
    expect(screen.getByRole("alert")).toHaveTextContent("Could not load the launch defaults.");
    expect(screen.queryByRole("button", { name: "Launch options" })).not.toBeInTheDocument();
    await user.type(title(), "fix");
    expect(start()).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    expect(retry).toHaveBeenCalledTimes(1);
  });
});

describe("the new panel", () => {
  it("registers the new panel with its head and a lazy body", () => {
    const panel = panelFor("new");
    expect(panel).toBe(newPanel);
    expect(panel?.Head).toBe(NewHead);
    expect(panel?.title({ sessionId: undefined, params: {}, search: {} })).toBe("New session");
    expect(panel?.tab).toBeUndefined();
  });

  it('NewHead is "New session" with a way back to the fleet', () => {
    render(<NewHead />);
    expect(screen.getByRole("heading").textContent).toBe("New session");
    expect(screen.getByRole("link").getAttribute("href")).toBe("#/");
  });

  it("draws the form through PanelHost once its chunk loads", async () => {
    load([W1]);
    render(<PanelHost panel="new" props={{ sessionId: undefined, params: {}, search: {} }} />);
    expect(await screen.findByRole("combobox", { name: "Workspace" })).toBeInTheDocument();
  });
});
