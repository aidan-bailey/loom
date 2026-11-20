import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";

import type { Created, LaunchDefaults, Outcome } from "@/api/gen";
import { api } from "@/api/request";
import { commandEnabled, runCommand } from "@/app/commands";
import { usePending } from "@/store/pending";

import { launchBody, type LaunchForm } from "./launch";
import { command, context, makeSession, resetFleet, seed, spyOnAdd } from "./testkit";

let add: ReturnType<typeof spyOnAdd>;
let assign: MockInstance<(url: string | URL) => void>;

beforeEach(() => {
  add = spyOnAdd();
  assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);
});

afterEach(() => {
  vi.restoreAllMocks();
  usePending.getState().clear();
  resetFleet();
});

const DEFAULTS: LaunchDefaults = {
  account: "",
  accounts: ["work"],
  model: "opus",
  models: ["opus", "sonnet"],
  permission_mode: "auto",
  permission_modes: ["auto", "plan"],
  effort: "high",
  efforts: ["high", "low"],
};

function form(over: Partial<LaunchForm> = {}): LaunchForm {
  return {
    workspace: 1,
    title: "fix the build",
    prompt: "go",
    picks: {
      account: DEFAULTS.account,
      model: DEFAULTS.model,
      permission_mode: DEFAULTS.permission_mode,
      effort: DEFAULTS.effort,
    },
    ...over,
  };
}

const NEW = context(undefined, { panel: "new" });

function created(id: number): Outcome<Created> {
  return { kind: "ok", status: 201, value: { id, notices: null }, notices: [] };
}

describe("launchBody", () => {
  it("trims the title, and the end of the prompt only", () => {
    const body = launchBody(
      form({ title: "  fix the build \n", prompt: "  go on \n\n" }),
      DEFAULTS,
    );
    expect(body.title).toBe("fix the build");
    expect(body.prompt).toBe("  go on");
    expect(body.workspace).toBe(1);
  });

  it("sends no pick that equals its default", () => {
    expect(launchBody(form(), DEFAULTS)).toStrictEqual({
      workspace: 1,
      title: "fix the build",
      prompt: "go",
    });
  });

  it("sends each pick that differs from its default, and only those", () => {
    const picks = { account: "work", model: "sonnet", permission_mode: "auto", effort: "low" };
    expect(launchBody(form({ picks }), DEFAULTS)).toStrictEqual({
      workspace: 1,
      title: "fix the build",
      prompt: "go",
      account: "work",
      model: "sonnet",
      effort: "low",
    });
  });

  it("sends an empty pick when the default is not empty: the empty one is a choice too", () => {
    const picks = { account: "", model: "", permission_mode: "auto", effort: "high" };
    expect(launchBody(form({ picks }), DEFAULTS)).toStrictEqual({
      workspace: 1,
      title: "fix the build",
      prompt: "go",
      model: "",
    });
  });
});

describe("session.create", () => {
  it("is on in the new-session view, and off elsewhere and while the snapshot is stale", () => {
    const create = command("session.create");
    expect(commandEnabled(create, NEW)).toBe(true);
    expect(commandEnabled(create, context(undefined, { panel: "fleet" }))).toBe(false);
    expect(
      commandEnabled(create, context(undefined, { panel: "new", stale: true, link: "offline" })),
    ).toBe(false);
  });

  it("sends the body, marks the new session pending and opens it on a 201", async () => {
    const body = launchBody(form(), DEFAULTS);
    const create = vi.spyOn(api, "create").mockResolvedValue(created(42));

    await runCommand(command("session.create"), NEW, { body });

    expect(create).toHaveBeenCalledOnce();
    expect(create).toHaveBeenCalledWith(body);
    expect(usePending.getState().id).toBe(42);
    expect(assign).toHaveBeenCalledOnce();
    expect(assign).toHaveBeenCalledWith("#/s/42");
  });

  it("does not hold a session pending when the snapshot already lists it", async () => {
    seed([makeSession({ id: 42 })]);
    vi.spyOn(api, "create").mockResolvedValue(created(42));

    await runCommand(command("session.create"), NEW, { body: launchBody(form(), DEFAULTS) });

    expect(usePending.getState().id).toBeNull();
    expect(assign).toHaveBeenCalledWith("#/s/42");
  });

  it("toasts that the session is still starting and opens the fleet on a 202", async () => {
    vi.spyOn(api, "create").mockResolvedValue({ kind: "pending" });

    await runCommand(command("session.create"), NEW, { body: launchBody(form(), DEFAULTS) });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "Still starting; the session will show in the fleet.",
        type: "info",
      }),
    );
    expect(assign).toHaveBeenCalledOnce();
    expect(assign).toHaveBeenCalledWith("#/");
    expect(usePending.getState().id).toBeNull();
  });

  it.each([
    [400, "title: a session needs a title"],
    [409, 'repo already has a session titled "fix the build"'],
  ])(
    "toasts the daemon's words and goes nowhere when it refuses with %i",
    async (status, message) => {
      vi.spyOn(api, "create").mockResolvedValue({ kind: "refused", status, message });

      await runCommand(command("session.create"), NEW, { body: launchBody(form(), DEFAULTS) });

      expect(add).toHaveBeenCalledWith(
        expect.objectContaining({ description: message, type: "error" }),
      );
      expect(assign).not.toHaveBeenCalled();
      expect(usePending.getState().id).toBeNull();
    },
  );

  it("toasts the failure's words and goes nowhere when the request fails", async () => {
    vi.spyOn(api, "create").mockResolvedValue({
      kind: "failed",
      status: 0,
      message: "Can't reach loom.",
    });

    await runCommand(command("session.create"), NEW, { body: launchBody(form(), DEFAULTS) });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "Can't reach loom.", type: "error" }),
    );
    expect(assign).not.toHaveBeenCalled();
  });

  it("runs nothing while the snapshot is stale", async () => {
    const create = vi.spyOn(api, "create").mockResolvedValue(created(42));

    await runCommand(command("session.create"), context(undefined, { panel: "new", stale: true }), {
      body: launchBody(form(), DEFAULTS),
    });

    expect(create).not.toHaveBeenCalled();
  });
});
