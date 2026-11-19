import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Session } from "@/api/gen";
import { toastManager } from "@/components/ui/toast";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import { useFleet } from "@/store/fleet";

import {
  commandById,
  commandEnabled,
  defineCommand,
  runCommand,
  useCommandBusy,
  useCommandContext,
  type CommandContext,
} from "./commands";
import { askConfirm } from "./ConfirmSheet";
import { switchApp } from "./switchApp";

vi.mock("./ConfirmSheet", async (original) => ({
  ...(await original<typeof import("./ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));
vi.mock("./switchApp", () => ({ switchApp: vi.fn() }));

function spyOnAdd() {
  return vi.spyOn(toastManager, "add").mockImplementation(() => "toast");
}

let add: ReturnType<typeof spyOnAdd>;

beforeEach(() => {
  add = spyOnAdd();
});

afterEach(() => {
  add.mockRestore();
  vi.mocked(askConfirm).mockReset();
  vi.mocked(switchApp).mockReset();
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  window.history.replaceState(null, "", window.location.pathname);
});

function ctx(over: Partial<CommandContext> = {}): CommandContext {
  return { session: undefined, link: "live", stale: false, panel: "fleet", ...over };
}

describe("the registry", () => {
  it("finds a command by its id, and none for an unknown one", () => {
    const command = defineCommand({
      id: "test.registry",
      title: "Registry",
      when: () => true,
      run: () => Promise.resolve(),
    });
    expect(commandById("test.registry")).toBe(command);
    expect(commandById("test.nothing")).toBeUndefined();
  });

  it("replaces a command defined again under the same id", () => {
    defineCommand({
      id: "test.again",
      title: "first",
      when: () => true,
      run: () => Promise.resolve(),
    });
    const second = defineCommand({
      id: "test.again",
      title: "second",
      when: () => true,
      run: () => Promise.resolve(),
    });
    expect(commandById("test.again")).toBe(second);
  });
});

describe("commandEnabled", () => {
  const base = { id: "test.enabled", title: "t", run: () => Promise.resolve() };

  it.each([
    ["live and when true", { when: () => true }, ctx(), true],
    ["stale", { when: () => true }, ctx({ stale: true }), false],
    ["stale but offline", { when: () => true, offline: true }, ctx({ stale: true }), true],
    ["when false", { when: () => false }, ctx(), false],
    [
      "stale and offline, when false",
      { when: () => false, offline: true },
      ctx({ stale: true }),
      false,
    ],
  ])("%s", (_name, over, context, want) => {
    expect(commandEnabled({ ...base, ...over }, context)).toBe(want);
  });
});

describe("runCommand", () => {
  function counting(over: { offline?: boolean; when?: () => boolean } = {}) {
    const run = vi.fn<(ctx: CommandContext, args: string, input: string) => Promise<void>>(() =>
      Promise.resolve(),
    );
    const command = defineCommand<string>({
      id: "test.run",
      title: "Run",
      when: over.when ?? (() => true),
      ...(over.offline === undefined ? {} : { offline: over.offline }),
      run,
    });
    return { command, run };
  }

  it("runs an enabled command with its context and arguments", async () => {
    const { command, run } = counting();
    const context = ctx();
    await runCommand(command, context, "args");
    expect(run).toHaveBeenCalledTimes(1);
    expect(run).toHaveBeenCalledWith(context, "args", "");
  });

  it("runs nothing while the snapshot is stale", async () => {
    const { command, run } = counting();
    await runCommand(command, ctx({ stale: true }), "args");
    expect(run).not.toHaveBeenCalled();
  });

  it("runs an offline command while the snapshot is stale", async () => {
    const { command, run } = counting({ offline: true });
    await runCommand(command, ctx({ stale: true }), "args");
    expect(run).toHaveBeenCalledOnce();
  });

  it("runs nothing when its own when says no", async () => {
    const { command, run } = counting({ when: () => false });
    await runCommand(command, ctx(), "args");
    expect(run).not.toHaveBeenCalled();
  });

  it("asks the sheet first, with the spec built from the context and arguments, and runs on yes", async () => {
    vi.mocked(askConfirm).mockResolvedValue("");
    const run = vi.fn<(ctx: CommandContext, note: string, input: string) => Promise<void>>(() =>
      Promise.resolve(),
    );
    const command = defineCommand<string>({
      id: "test.confirm",
      title: "Send",
      when: () => true,
      confirm: (_ctx, note) => ({ title: "Send", body: note, action: "Send", destructive: false }),
      run,
    });
    const context = ctx();
    // The sheet's yes is checked against the store again, which is live here.
    useFleet.setState({ stale: false, link: "live" });

    await runCommand(command, context, "hello");

    expect(askConfirm).toHaveBeenCalledWith({
      title: "Send",
      body: "hello",
      action: "Send",
      destructive: false,
    });
    expect(run).toHaveBeenCalledTimes(1);
    expect(run).toHaveBeenCalledWith(context, "hello", "");
  });

  it("runs nothing when the sheet is declined", async () => {
    vi.mocked(askConfirm).mockResolvedValue(null);
    const run = vi.fn(() => Promise.resolve());
    const command = defineCommand({
      id: "test.declined",
      title: "Kill",
      when: () => true,
      confirm: () => ({ title: "Kill", body: "", action: "Kill", destructive: true }),
      run,
    });

    await runCommand(command, ctx(), undefined);

    expect(run).not.toHaveBeenCalled();
  });

  it("checks the enablement again after the sheet's yes, and runs nothing when the view went stale meanwhile", async () => {
    useFleet.setState({ snapshot: fixtureSnapshot(), stale: false, link: "live" });
    vi.mocked(askConfirm).mockImplementation(() => {
      useFleet.getState().setLink("offline");
      return Promise.resolve("");
    });
    const run = vi.fn(() => Promise.resolve());
    const command = defineCommand({
      id: "test.staleWhileAsking",
      title: "Kill",
      when: () => true,
      confirm: () => ({ title: "Kill", body: "", action: "Kill", destructive: true }),
      run,
    });

    await runCommand(command, ctx(), undefined);

    expect(askConfirm).toHaveBeenCalledOnce();
    expect(run).not.toHaveBeenCalled();
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: expect.stringContaining("nothing was done") as string,
        type: "error",
      }),
    );
  });

  it("runs nothing after the sheet's yes when the session left the fleet meanwhile, and runs with the fresh context otherwise", async () => {
    const snapshot = fixtureSnapshot();
    const session = allSessions(snapshot)[0];
    if (session === undefined) throw new Error("the fixture holds no session");
    useFleet.setState({ snapshot, stale: false, link: "live" });
    const run = vi.fn<(ctx: CommandContext, args: undefined) => Promise<void>>(() =>
      Promise.resolve(),
    );
    const command = defineCommand({
      id: "test.sessionGone",
      title: "Kill",
      when: (c) => c.session !== undefined,
      confirm: () => ({ title: "Kill", body: "", action: "Kill", destructive: true }),
      run,
    });

    vi.mocked(askConfirm).mockResolvedValueOnce("");
    await runCommand(command, ctx({ session }), undefined);
    expect(run).toHaveBeenCalledOnce();
    expect(run.mock.calls[0]?.[0].session?.id).toBe(session.id);

    // The sheet's yes arrives after the session changed: run gets the store's
    // copy, not the context the command was started with.
    run.mockClear();
    const changed: Session = {
      ...session,
      status: session.status === "Running" ? "Ready" : "Running",
    };
    vi.mocked(askConfirm).mockImplementationOnce(() => {
      const sessions = Object.fromEntries(
        Object.entries(snapshot.sessions ?? {}).map(([key, list]) => [
          key,
          (list ?? []).map((s) => (s.id === session.id ? changed : s)),
        ]),
      );
      useFleet.setState({ snapshot: { ...snapshot, sessions } });
      return Promise.resolve("");
    });
    const started = ctx({ session });
    await runCommand(command, started, undefined);
    expect(run).toHaveBeenCalledOnce();
    const got = run.mock.calls[0]?.[0].session;
    expect(got).toEqual(changed);
    expect(got).not.toBe(started.session);

    vi.mocked(askConfirm).mockImplementationOnce(() => {
      useFleet.setState({ snapshot: { ...snapshot, sessions: {} } });
      return Promise.resolve("");
    });
    await runCommand(command, ctx({ session }), undefined);
    expect(run).toHaveBeenCalledOnce();
  });

  it("runs a command once at a time for one session: a second call while the first is in flight does nothing", async () => {
    let finish: () => void = () => {};
    const run = vi.fn<(ctx: CommandContext, args: undefined) => Promise<void>>(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    const command = defineCommand({ id: "test.inflight", title: "Once", when: () => true, run });
    const busy = renderHook(() => useCommandBusy(command, ctx(), undefined));
    expect(busy.result.current).toBe(false);

    let first: Promise<void> = Promise.resolve();
    act(() => {
      first = runCommand(command, ctx(), undefined);
    });
    await runCommand(command, ctx(), undefined);

    expect(run).toHaveBeenCalledOnce();
    expect(busy.result.current).toBe(true);

    await act(async () => {
      finish();
      await first;
    });
    expect(busy.result.current).toBe(false);

    let second: Promise<void> = Promise.resolve();
    act(() => {
      second = runCommand(command, ctx(), undefined);
    });
    expect(run).toHaveBeenCalledTimes(2);
    await act(async () => {
      finish();
      await second;
    });
  });

  it("gives run the sheet's value as its input", async () => {
    vi.mocked(askConfirm).mockResolvedValue("because");
    useFleet.setState({ stale: false, link: "live" });
    const run = vi.fn<(ctx: CommandContext, id: string, input: string) => Promise<void>>(() =>
      Promise.resolve(),
    );
    const command = defineCommand<string>({
      id: "test.input",
      title: "Deny",
      when: () => true,
      confirm: () => ({
        title: "Deny this?",
        body: "",
        action: "Deny",
        destructive: true,
        field: { kind: "text", label: "Why", placeholder: "", max: 100, tooLong: "too long" },
      }),
      run,
    });
    const context = ctx();

    await runCommand(command, context, "ask-1");

    expect(run).toHaveBeenCalledOnce();
    expect(run).toHaveBeenCalledWith(context, "ask-1", "because");
  });

  it("runs with no sheet and an empty input when confirm returns null, and asks when it returns a spec", async () => {
    useFleet.setState({ stale: false, link: "live" });
    vi.mocked(askConfirm).mockResolvedValue("yes");
    const run = vi.fn<(ctx: CommandContext, persists: boolean, input: string) => Promise<void>>(
      () => Promise.resolve(),
    );
    const command = defineCommand<boolean>({
      id: "test.nullConfirm",
      title: "Allow",
      when: () => true,
      confirm: (_ctx, persists) =>
        persists
          ? { title: "Save this rule?", body: "", action: "Save", destructive: false }
          : null,
      run,
    });
    const context = ctx();

    await runCommand(command, context, false);
    expect(askConfirm).not.toHaveBeenCalled();
    expect(run).toHaveBeenCalledOnce();
    expect(run).toHaveBeenLastCalledWith(context, false, "");

    await runCommand(command, context, true);
    expect(askConfirm).toHaveBeenCalledOnce();
    expect(run).toHaveBeenCalledTimes(2);
    expect(run).toHaveBeenLastCalledWith(context, true, "yes");
  });

  it("keeps the in-flight mark per key: the same key is held, another key runs, and each key's busy state is its own", async () => {
    const finishers: (() => void)[] = [];
    const run = vi.fn<(ctx: CommandContext, args: { job: number }, input: string) => Promise<void>>(
      () =>
        new Promise<void>((resolve) => {
          finishers.push(resolve);
        }),
    );
    const command = defineCommand<{ job: number }>({
      id: "test.keyed",
      title: "Park",
      when: () => true,
      key: (args) => `job${args.job}`,
      run,
    });
    const one = renderHook(() => useCommandBusy(command, ctx(), { job: 1 }));
    const two = renderHook(() => useCommandBusy(command, ctx(), { job: 2 }));
    const three = renderHook(() => useCommandBusy(command, ctx(), { job: 3 }));

    let first: Promise<void> = Promise.resolve();
    let second: Promise<void> = Promise.resolve();
    act(() => {
      first = runCommand(command, ctx(), { job: 1 });
    });
    await runCommand(command, ctx(), { job: 1 });
    expect(run).toHaveBeenCalledOnce();

    act(() => {
      second = runCommand(command, ctx(), { job: 2 });
    });
    expect(run).toHaveBeenCalledTimes(2);
    expect(one.result.current).toBe(true);
    expect(two.result.current).toBe(true);
    expect(three.result.current).toBe(false);

    await act(async () => {
      for (const finish of finishers) finish();
      await Promise.all([first, second]);
    });
    expect(one.result.current).toBe(false);
    expect(two.result.current).toBe(false);
  });

  it("holds the same key for another session apart: the key joins the session's, it does not replace it", async () => {
    const snapshot = fixtureSnapshot();
    const sessions = allSessions(snapshot);
    const a = sessions[0];
    const b = sessions[1];
    if (a === undefined || b === undefined)
      throw new Error("the fixture holds fewer than two sessions");
    const finishers: (() => void)[] = [];
    const run = vi.fn<(ctx: CommandContext, args: { job: number }, input: string) => Promise<void>>(
      () =>
        new Promise<void>((resolve) => {
          finishers.push(resolve);
        }),
    );
    const command = defineCommand<{ job: number }>({
      id: "test.keyedSessions",
      title: "Park",
      when: () => true,
      key: (args) => `job${args.job}`,
      run,
    });

    let first: Promise<void> = Promise.resolve();
    let second: Promise<void> = Promise.resolve();
    act(() => {
      first = runCommand(command, ctx({ session: a }), { job: 1 });
      second = runCommand(command, ctx({ session: b }), { job: 1 });
    });
    expect(run).toHaveBeenCalledTimes(2);

    await act(async () => {
      for (const finish of finishers) finish();
      await Promise.all([first, second]);
    });
  });

  it("treats a key that throws as no key: running the command and reading its busy state neither reject nor throw", async () => {
    const run = vi.fn<(ctx: CommandContext, args: string, input: string) => Promise<void>>(() =>
      Promise.resolve(),
    );
    const command = defineCommand<string>({
      id: "test.keyThrows",
      title: "Park",
      when: () => true,
      key: () => {
        throw new Error("no job to key on");
      },
      run,
    });

    const busy = renderHook(() => useCommandBusy(command, ctx(), "args"));
    expect(busy.result.current).toBe(false);
    await expect(runCommand(command, ctx(), "args")).resolves.toBeUndefined();
    await expect(runCommand(command, ctx(), "args")).resolves.toBeUndefined();

    expect(run).toHaveBeenCalledTimes(2);
  });

  it("clears the in-flight mark when the run throws", async () => {
    const run = vi.fn(() => Promise.reject(new Error("no")));
    const command = defineCommand({
      id: "test.inflightThrows",
      title: "Boom",
      when: () => true,
      run,
    });

    await runCommand(command, ctx(), undefined);
    await runCommand(command, ctx(), undefined);

    expect(run).toHaveBeenCalledTimes(2);
  });

  it("asks nothing for a command with no confirm, nor for a disabled one", async () => {
    const plain = counting();
    await runCommand(plain.command, ctx(), "args");
    const confirming = defineCommand({
      id: "test.disabled",
      title: "Kill",
      when: () => true,
      confirm: () => ({ title: "Kill", body: "", action: "Kill", destructive: true }),
      run: () => Promise.resolve(),
    });
    await runCommand(confirming, ctx({ stale: true }), undefined);
    expect(askConfirm).not.toHaveBeenCalled();
  });

  it("toasts an error for a run that throws, and rejects nothing", async () => {
    const command = defineCommand({
      id: "test.throws",
      title: "Boom",
      when: () => true,
      run: () => Promise.reject(new Error("the daemon said no")),
    });

    await expect(runCommand(command, ctx(), undefined)).resolves.toBeUndefined();

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "the daemon said no", type: "error" }),
    );
  });
});

describe("app.backToClassic", () => {
  function backToClassic() {
    const command = commandById("app.backToClassic");
    if (command === undefined) throw new Error("app.backToClassic is not defined");
    return command;
  }

  it("is an offline command, on for every context", () => {
    const command = backToClassic();
    expect(command.offline).toBe(true);
    for (const context of [ctx(), ctx({ stale: true, link: "offline" }), ctx({ panel: "pair" })]) {
      expect(commandEnabled(command, context)).toBe(true);
    }
  });

  it("switches to the classic app", async () => {
    vi.mocked(switchApp).mockResolvedValue("switched");
    await runCommand(backToClassic(), ctx({ stale: true }), undefined);
    expect(switchApp).toHaveBeenCalledTimes(1);
    expect(switchApp).toHaveBeenCalledWith("classic");
    expect(add).not.toHaveBeenCalled();
  });

  it("says the switch comes with the next launch when the worker could not update", async () => {
    vi.mocked(switchApp).mockResolvedValue("deferred");
    await runCommand(backToClassic(), ctx({ stale: true }), undefined);
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: expect.stringContaining("next time") as string }),
    );
  });
});

describe("useCommandContext", () => {
  it("is the route's session, the link and the route's panel", () => {
    const snapshot = fixtureSnapshot();
    const session = allSessions(snapshot)[0];
    if (session === undefined) throw new Error("the fixture holds no session");
    useFleet.setState({ snapshot, stale: false, link: "live" });
    window.history.replaceState(null, "", `#/s/${session.id}/info`);

    const { result } = renderHook(() => useCommandContext());

    expect(result.current).toEqual({ session, link: "live", stale: false, panel: "info" });
  });

  it("has no session on a route without one, or for an ID the fleet does not list", () => {
    useFleet.setState({ snapshot: fixtureSnapshot(), stale: false, link: "live" });

    window.history.replaceState(null, "", "#/");
    expect(renderHook(() => useCommandContext()).result.current.session).toBeUndefined();

    window.history.replaceState(null, "", "#/s/999999/info");
    expect(renderHook(() => useCommandContext()).result.current.session).toBeUndefined();
  });

  it("is stale, with the link's state, while the stream is down", () => {
    useFleet.setState({ snapshot: fixtureSnapshot(), stale: true, link: "offline" });

    const { result } = renderHook(() => useCommandContext());

    expect(result.current.stale).toBe(true);
    expect(result.current.link).toBe("offline");
  });
});
