import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";

import type { Device, Outcome, Pair, QuickReplies } from "@/api/gen";
import { api, onUnpaired } from "@/api/request";
import { commandEnabled, runCommand } from "@/app/commands";
import { askConfirm } from "@/app/ConfirmSheet";
import { startFleet, useFleet } from "@/store/fleet";

import "./device";
import "./launch";
import {
  askedSheet,
  command,
  context,
  deferred,
  makeSession,
  resetFleet,
  seed,
  spyOnAdd,
} from "./testkit";

vi.mock("@/app/ConfirmSheet", async (original) => ({
  ...(await original<typeof import("@/app/ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));

vi.mock("@/store/fleet", async (original) => ({
  ...(await original<typeof import("@/store/fleet")>()),
  startFleet: vi.fn(() => () => undefined),
}));

let add: ReturnType<typeof spyOnAdd>;
let assign: MockInstance<(url: string | URL) => void>;

beforeEach(() => {
  add = spyOnAdd();
  assign = vi.spyOn(window.location, "assign").mockImplementation(() => undefined);
  seed([makeSession()]);
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.mocked(askConfirm).mockReset();
  vi.mocked(startFleet).mockClear();
  resetFleet();
});

const SETTINGS = context(undefined, { panel: "settings" });
const PAIR_OFFLINE = context(undefined, { panel: "pair", stale: true, link: "offline" });

function ok<T>(value: T, status = 200): Outcome<T> {
  return { kind: "ok", status, value, notices: [] };
}

const ANSWERED: Device = {
  name: "iPhone",
  notify: { waiting: true, finished: false, paused_until: "2026-11-20T13:00:00Z" },
  push: "",
};
const STREAMED: Device = { ...ANSWERED, notify: { ...ANSWERED.notify, waiting: false } };
const device = () => useFleet.getState().snapshot?.device;
const replies = () => useFleet.getState().snapshot?.quick_replies;

describe("device.update", () => {
  it("is on in the settings view, and off elsewhere and while the snapshot is stale", () => {
    const update = command("device.update");
    expect(commandEnabled(update, SETTINGS)).toBe(true);
    expect(commandEnabled(update, context(undefined, { panel: "turns" }))).toBe(false);
    expect(
      commandEnabled(
        update,
        context(undefined, { panel: "settings", stale: true, link: "offline" }),
      ),
    ).toBe(false);
  });

  it("sends the body as given and applies the answered device to the store", async () => {
    const put = vi.spyOn(api, "putDevice").mockResolvedValue(ok(ANSWERED));

    await runCommand(command("device.update"), SETTINGS, { waiting: true, pause: "1h" });

    expect(put).toHaveBeenCalledOnce();
    expect(put).toHaveBeenCalledWith({ waiting: true, pause: "1h" });
    expect(device()).toEqual(ANSWERED);
  });

  it("keeps the stream's device when its event was applied while the write was in flight", async () => {
    vi.spyOn(api, "putDevice").mockImplementation(() => {
      useFleet.getState().apply("device", STREAMED);
      return Promise.resolve(ok(ANSWERED));
    });

    await runCommand(command("device.update"), SETTINGS, { waiting: true });

    expect(device()).toBe(STREAMED);
  });

  it("does not apply the second answer of two writes in flight together, and the stream's event then brings its value", async () => {
    const FIRST: Device = { ...ANSWERED, notify: { ...ANSWERED.notify, waiting: false } };
    const SECOND: Device = { ...ANSWERED, notify: { ...ANSWERED.notify, finished: true } };
    const a = deferred<Outcome<Device>>();
    const b = deferred<Outcome<Device>>();
    vi.spyOn(api, "putDevice").mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise);
    const update = command("device.update");

    const runA = runCommand(update, SETTINGS, { waiting: false });
    const runB = runCommand(update, SETTINGS, { finished: true });
    a.resolve(ok(FIRST));
    await runA;
    expect(device()).toBe(FIRST);
    b.resolve(ok(SECOND));
    await runB;

    expect(device()).toBe(FIRST);
    useFleet.getState().apply("device", STREAMED);
    expect(device()).toBe(STREAMED);
  });

  it("toasts the daemon's words and leaves the device when the write is refused", async () => {
    const before = device();
    vi.spyOn(api, "putDevice").mockResolvedValue({
      kind: "refused",
      status: 400,
      message: 'pause: want "1h" or "none"',
    });

    await runCommand(command("device.update"), SETTINGS, { pause: "later" });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: 'pause: want "1h" or "none"', type: "error" }),
    );
    expect(device()).toBe(before);
  });

  it("runs writes of different fields together, and holds a second write of the same field whatever its value", async () => {
    const first = deferred<Outcome<Device>>();
    const put = vi.spyOn(api, "putDevice").mockReturnValue(first.promise);
    const update = command("device.update");

    const a = runCommand(update, SETTINGS, { waiting: true });
    const b = runCommand(update, SETTINGS, { finished: true });
    const c = runCommand(update, SETTINGS, { waiting: false });
    expect(put).toHaveBeenCalledTimes(2);
    expect(put).toHaveBeenNthCalledWith(1, { waiting: true });
    expect(put).toHaveBeenNthCalledWith(2, { finished: true });

    first.resolve(ok(ANSWERED));
    await Promise.all([a, b, c]);
  });
});

describe("quickReplies.save", () => {
  const ANSWER: QuickReplies = { quick_replies: ["ok", "later"] };

  it("is on in the settings view, and off elsewhere and while the snapshot is stale", () => {
    const save = command("quickReplies.save");
    expect(commandEnabled(save, SETTINGS)).toBe(true);
    expect(commandEnabled(save, context(undefined, { panel: "info" }))).toBe(false);
    expect(
      commandEnabled(save, context(undefined, { panel: "settings", stale: true, link: "offline" })),
    ).toBe(false);
  });

  it("sends the list, applies the answer to the store and calls onSaved", async () => {
    const put = vi.spyOn(api, "putQuickReplies").mockResolvedValue(ok(ANSWER));
    const onSaved = vi.fn();

    await runCommand(command("quickReplies.save"), SETTINGS, { replies: ["ok", "later"], onSaved });

    expect(put).toHaveBeenCalledWith({ replies: ["ok", "later"] });
    expect(replies()).toEqual(["ok", "later"]);
    expect(onSaved).toHaveBeenCalledOnce();
  });

  it("sends no list to reset to the defaults, and applies the defaults the daemon answers", async () => {
    const put = vi
      .spyOn(api, "putQuickReplies")
      .mockResolvedValue(ok({ quick_replies: ["yes", "continue"] }));

    await runCommand(command("quickReplies.save"), SETTINGS, { replies: null });

    expect(put.mock.calls[0]?.[0]).toStrictEqual({});
    expect(replies()).toEqual(["yes", "continue"]);
  });

  it("keeps the stream's list when its event was applied while the write was in flight, and still calls onSaved", async () => {
    vi.spyOn(api, "putQuickReplies").mockImplementation(() => {
      useFleet.getState().apply("quick_replies", { quick_replies: ["from the stream"] });
      return Promise.resolve(ok(ANSWER));
    });
    const onSaved = vi.fn();

    await runCommand(command("quickReplies.save"), SETTINGS, { replies: ["ok"], onSaved });

    expect(replies()).toEqual(["from the stream"]);
    expect(onSaved).toHaveBeenCalledOnce();
  });

  it("toasts the daemon's words, leaves the list and does not call onSaved when it is refused", async () => {
    const before = replies();
    vi.spyOn(api, "putQuickReplies").mockResolvedValue({
      kind: "refused",
      status: 400,
      message: "at most 12 quick replies",
    });
    const onSaved = vi.fn();

    await runCommand(command("quickReplies.save"), SETTINGS, { replies: ["a"], onSaved });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "at most 12 quick replies", type: "error" }),
    );
    expect(replies()).toBe(before);
    expect(onSaved).not.toHaveBeenCalled();
  });

  it("holds a second save, or a reset, while one is in flight", async () => {
    const first = deferred<Outcome<QuickReplies>>();
    const put = vi.spyOn(api, "putQuickReplies").mockReturnValue(first.promise);
    const save = command("quickReplies.save");

    const a = runCommand(save, SETTINGS, { replies: ["a"] });
    const b = runCommand(save, SETTINGS, { replies: null });
    expect(put).toHaveBeenCalledOnce();

    first.resolve(ok(ANSWER));
    await Promise.all([a, b]);
  });
});

describe("device.unpair", () => {
  it("is on in the settings view, and off elsewhere and while the snapshot is stale", () => {
    const unpair = command("device.unpair");
    expect(commandEnabled(unpair, SETTINGS)).toBe(true);
    expect(commandEnabled(unpair, context(undefined, { panel: "fleet" }))).toBe(false);
    expect(
      commandEnabled(
        unpair,
        context(undefined, { panel: "settings", stale: true, link: "offline" }),
      ),
    ).toBe(false);
  });

  it("asks with the classic words, and on a yes and a 204 runs the unpaired path", async () => {
    vi.mocked(askConfirm).mockResolvedValue("");
    const del = vi.spyOn(api, "deleteDevice").mockResolvedValue(ok<void>(undefined, 204));
    const unpaired = vi.fn();
    const off = onUnpaired(unpaired);

    await runCommand(command("device.unpair"), SETTINGS, undefined);
    off();

    expect(askedSheet()).toEqual({
      title: "Unpair this phone?",
      body: "This phone stops getting notifications and goes back to pairing. Pair it again with loom web pair on the host.",
      action: "Unpair",
      destructive: true,
    });
    expect(del).toHaveBeenCalledOnce();
    expect(unpaired).toHaveBeenCalledOnce();
  });

  it("sends nothing when the sheet is cancelled", async () => {
    vi.mocked(askConfirm).mockResolvedValue(null);
    const del = vi.spyOn(api, "deleteDevice").mockResolvedValue(ok<void>(undefined, 204));
    const unpaired = vi.fn();
    const off = onUnpaired(unpaired);

    await runCommand(command("device.unpair"), SETTINGS, undefined);
    off();

    expect(del).not.toHaveBeenCalled();
    expect(unpaired).not.toHaveBeenCalled();
  });

  it("toasts the daemon's words and does not unpair when the request is refused", async () => {
    vi.mocked(askConfirm).mockResolvedValue("");
    vi.spyOn(api, "deleteDevice").mockResolvedValue({
      kind: "failed",
      status: 500,
      message: "That didn't work.",
    });
    const unpaired = vi.fn();
    const off = onUnpaired(unpaired);

    await runCommand(command("device.unpair"), SETTINGS, undefined);
    off();

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "That didn't work.", type: "error" }),
    );
    expect(unpaired).not.toHaveBeenCalled();
  });
});

describe("device.pair", () => {
  it("is on the pairing route, offline, and nowhere else", () => {
    const pair = command("device.pair");
    expect(commandEnabled(pair, PAIR_OFFLINE)).toBe(true);
    expect(commandEnabled(pair, context(undefined, { panel: "settings" }))).toBe(false);
    expect(commandEnabled(pair, context(undefined, { panel: "fleet" }))).toBe(false);
  });

  it.each([
    ["session.create", "new"],
    ["device.update", "settings"],
    ["quickReplies.save", "settings"],
    ["device.unpair", "settings"],
  ] as const)(
    "%s, unlike device.pair, is off on its own route while the snapshot is stale",
    (id, panel) => {
      expect(commandEnabled(command(id), context(undefined, { panel }))).toBe(true);
      expect(
        commandEnabled(command(id), context(undefined, { panel, stale: true, link: "offline" })),
      ).toBe(false);
    },
  );

  it("pairs with the code while the snapshot is stale, starts the fleet and opens it", async () => {
    const calls: string[] = [];
    vi.mocked(startFleet).mockImplementation(() => {
      calls.push("startFleet");
      return () => undefined;
    });
    assign.mockImplementation((url: string | URL) => {
      calls.push(`assign ${String(url)}`);
    });
    const pair = vi.spyOn(api, "pair").mockResolvedValue(ok<Pair>({ device: "iPhone" }));

    await runCommand(command("device.pair"), PAIR_OFFLINE, { code: "K7QM2XPA" });

    expect(pair).toHaveBeenCalledOnce();
    expect(pair).toHaveBeenCalledWith({ code: "K7QM2XPA" });
    expect(calls).toEqual(["startFleet", "assign #/"]);
  });

  it.each([
    [400, "that code is not right"],
    [429, "too many wrong codes; run loom web pair again"],
    [409, "a device named iPhone is already paired"],
  ])(
    "toasts the daemon's words and starts nothing when it refuses with %i",
    async (status, message) => {
      vi.spyOn(api, "pair").mockResolvedValue({ kind: "refused", status, message });

      await runCommand(command("device.pair"), PAIR_OFFLINE, { code: "K7QM2XPA" });

      expect(add).toHaveBeenCalledWith(
        expect.objectContaining({ description: message, type: "error" }),
      );
      expect(startFleet).not.toHaveBeenCalled();
      expect(assign).not.toHaveBeenCalled();
    },
  );

  // request.ts answers a 503 as `stopping` and a 504 as `timeout`, never as `refused`.
  it.each([
    ["stopping", "the web is off"],
    ["timeout", "loom took too long to answer"],
  ] as const)(
    "toasts the daemon's words and starts nothing for a %s answer",
    async (kind, message) => {
      vi.spyOn(api, "pair").mockResolvedValue({ kind, message });

      await runCommand(command("device.pair"), PAIR_OFFLINE, { code: "K7QM2XPA" });

      expect(add).toHaveBeenCalledWith(
        expect.objectContaining({ description: message, type: "error" }),
      );
      expect(startFleet).not.toHaveBeenCalled();
      expect(assign).not.toHaveBeenCalled();
    },
  );

  it("toasts the daemon's text for a pairing that failed on its side", async () => {
    vi.spyOn(api, "pair").mockResolvedValue({
      kind: "failed",
      status: 500,
      message: "pairing failed; the daemon's log says why",
    });

    await runCommand(command("device.pair"), PAIR_OFFLINE, { code: "K7QM2XPA" });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "pairing failed; the daemon's log says why",
        type: "error",
      }),
    );
    expect(startFleet).not.toHaveBeenCalled();
  });

  it("says it cannot reach loom, in the classic words, when the request never got an answer", async () => {
    vi.spyOn(api, "pair").mockResolvedValue({
      kind: "failed",
      status: 0,
      message: "Can't reach loom.",
    });

    await runCommand(command("device.pair"), PAIR_OFFLINE, { code: "K7QM2XPA" });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "Can't reach loom. Check the connection and try again.",
        type: "error",
      }),
    );
    expect(startFleet).not.toHaveBeenCalled();
    expect(assign).not.toHaveBeenCalled();
  });
});
