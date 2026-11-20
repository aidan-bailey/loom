import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import "@/commands";
import type { Device, Notice, Outcome, QuickReplies, Snapshot } from "@/api/gen";
import { api } from "@/api/request";
import { askConfirm } from "@/app/ConfirmSheet";
import { panelFor } from "@/app/panels";
import { activeRegistration, pushSupported, usePushStatus } from "@/app/push";
import { hrefFor } from "@/app/routes";
import { askedSheet, deferred, resetFleet, spyOnAdd } from "@/commands/testkit";
import { fixtureSnapshot } from "@/fixtures";
import { useFleet } from "@/store/fleet";

import { SettingsHead, settingsPanel } from "./index";
import SettingsBody from "./SettingsBody";

vi.mock("@/app/ConfirmSheet", async (original) => ({
  ...(await original<typeof import("@/app/ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));
// The phone's own push state and the browser calls the command makes are the test's to answer;
// the command itself stays real.
vi.mock("@/app/push", async (original) => ({
  ...(await original<typeof import("@/app/push")>()),
  usePushStatus: vi.fn(),
  pushSupported: vi.fn(),
  activeRegistration: vi.fn(),
}));

const NOW = Date.parse("2026-11-20T12:00:00Z");

const PHONE: Device = {
  name: "iphone",
  notify: { waiting: true, finished: false, paused_until: "" },
  push: "ok",
};

let add: ReturnType<typeof spyOnAdd>;

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
  window.history.replaceState(null, "", "#/settings");
  add = spyOnAdd();
  vi.mocked(usePushStatus).mockReturnValue("off");
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.mocked(askConfirm).mockReset();
  resetFleet();
  window.history.replaceState(null, "", window.location.pathname);
});

// live makes the store hold the canned snapshot with these fields changed, live.
function live(over: Partial<Snapshot> = {}): void {
  useFleet.setState({
    snapshot: {
      ...fixtureSnapshot(NOW),
      device: PHONE,
      notices: [],
      quick_replies: ["ok", "later"],
      host: "fresco",
      build: "v1.2.3",
      ...over,
    },
    stale: false,
    link: "live",
    updated: NOW,
  });
}

function ok<T>(value: T, status = 200): Outcome<T> {
  return { kind: "ok", status, value, notices: [] };
}

// section is the section of the screen under this heading.
function section(name: string): HTMLElement {
  const found = screen.getByRole("heading", { name }).closest("section");
  if (found === null) throw new Error(`the screen has no ${name} section`);
  return found;
}

const ago = (ms: number): string => new Date(NOW - ms).toISOString();

describe("the settings panel", () => {
  it("is registered with its head and its title", () => {
    expect(panelFor("settings")).toBe(settingsPanel);
    expect(settingsPanel.Head).toBe(SettingsHead);
    expect(settingsPanel.title({ sessionId: undefined, params: {}, search: {} })).toBe("Settings");
  });

  it("heads the screen with Settings and a way back to the fleet", () => {
    render(<SettingsHead />);
    expect(screen.getByRole("heading", { level: 1, name: "Settings" })).toBeInTheDocument();
    expect(screen.getByRole("link")).toHaveAttribute("href", hrefFor("fleet"));
  });
});

describe("SettingsBody: Loading…", () => {
  it("says Loading… until the snapshot holds a device", () => {
    render(<SettingsBody />);
    expect(screen.getByText("Loading…")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "This phone" })).toBeNull();

    act(() => live());
    expect(screen.queryByText("Loading…")).toBeNull();
    expect(screen.getByRole("heading", { name: "This phone" })).toBeInTheDocument();
  });

  it("says Loading… for a saved snapshot that holds no device", () => {
    live({ device: undefined as unknown as Device });
    render(<SettingsBody />);
    expect(screen.getByText("Loading…")).toBeInTheDocument();
  });
});

describe("SettingsBody: This phone and App", () => {
  it("names the device, the host and the build", () => {
    live();
    render(<SettingsBody />);
    const phone = within(section("This phone"));
    expect(phone.getByText("Paired as")).toBeInTheDocument();
    expect(phone.getByText("iphone")).toBeInTheDocument();
    expect(phone.getByText("Host")).toBeInTheDocument();
    expect(phone.getByText("fresco")).toBeInTheDocument();
    expect(phone.getByText("Build")).toBeInTheDocument();
    expect(phone.getByText("v1.2.3")).toBeInTheDocument();
  });

  it("asks before it unpairs, and unpairs only on a yes", async () => {
    const unpair = vi.spyOn(api, "deleteDevice").mockResolvedValue(ok(undefined, 204));
    live();
    render(<SettingsBody />);
    const user = userEvent.setup();

    vi.mocked(askConfirm).mockResolvedValueOnce(null);
    await user.click(screen.getByRole("button", { name: "Unpair this phone" }));
    expect(askedSheet().title).toBe("Unpair this phone?");
    expect(unpair).not.toHaveBeenCalled();

    vi.mocked(askConfirm).mockResolvedValueOnce("");
    await user.click(screen.getByRole("button", { name: "Unpair this phone" }));
    await vi.waitFor(() => expect(unpair).toHaveBeenCalledTimes(1));
  });

  it("is off while the snapshot is stale, except the way back to the classic app", () => {
    live();
    useFleet.setState({ stale: true, link: "offline" });
    render(<SettingsBody />);
    expect(screen.getByRole("button", { name: "Unpair this phone" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Back to classic" })).toBeEnabled();
  });
});

describe("SettingsBody: Recent notices", () => {
  const notice = (text: string, at: string, error = false): Notice => ({ text, error, at });

  it("lists the newest first, each with its age, an error in red", () => {
    live({
      notices: [
        notice("Merged flow-run into main.", ago(12 * 60_000)),
        notice("Could not push fix-pane-cursor.", ago(3 * 60_000), true),
      ],
    });
    render(<SettingsBody />);
    const items = within(section("Recent notices")).getAllByRole("listitem");
    expect(items).toHaveLength(2);
    expect(items[0]).toHaveTextContent("3m");
    expect(items[0]).toHaveTextContent("Could not push fix-pane-cursor.");
    expect(items[0]).toHaveClass("text-error");
    expect(items[1]).toHaveTextContent("12m");
    expect(items[1]).toHaveTextContent("Merged flow-run into main.");
    expect(items[1]).not.toHaveClass("text-error");
  });

  it("says None. when there are no notices, and for notices that are null", () => {
    live({ notices: [] });
    const view = render(<SettingsBody />);
    expect(within(section("Recent notices")).getByText("None.")).toBeInTheDocument();
    view.unmount();

    live({ notices: null });
    render(<SettingsBody />);
    expect(within(section("Recent notices")).getByText("None.")).toBeInTheDocument();
  });
});

// pausedUntil is a device whose pause ends this long from now.
const pausedFor = (ms: number): Device => ({
  ...PHONE,
  notify: { ...PHONE.notify, paused_until: new Date(NOW + ms).toISOString() },
});

const aSwitch = (name: string): HTMLElement => screen.getByRole("switch", { name });

describe("SettingsBody: the push control", () => {
  it.each([
    [
      "unsupported",
      "Notifications need loom on the Home Screen. Tap Share, then Add to Home Screen, and open loom from there.",
    ],
    [
      "denied",
      "Notifications are blocked for loom. Allow them in the phone's Settings, under Notifications, then loom.",
    ],
    ["on", "Notifications are on for this phone."],
  ] as const)(
    "says %s in the classic words and offers no button to turn it on",
    (status, words) => {
      vi.mocked(usePushStatus).mockReturnValue(status);
      live();
      render(<SettingsBody />);
      expect(within(section("Notifications")).getByText(words)).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: /notifications/i })).toBeNull();
    },
  );

  it("draws nothing for the push control while the phone's state loads", () => {
    vi.mocked(usePushStatus).mockReturnValue("loading");
    live();
    render(<SettingsBody />);
    expect(screen.queryByRole("button", { name: /notifications/i })).toBeNull();
    expect(screen.queryByText(/Home Screen|blocked for loom|are on for this phone/)).toBeNull();
  });

  it("offers Turn on notifications when it is off and Turn notifications on again when it lapsed", () => {
    vi.mocked(usePushStatus).mockReturnValue("off");
    live();
    const view = render(<SettingsBody />);
    expect(screen.getByRole("button", { name: "Turn on notifications" })).toBeEnabled();
    expect(usePushStatus).toHaveBeenCalledWith(PHONE);
    view.unmount();

    vi.mocked(usePushStatus).mockReturnValue("again");
    live({ device: { ...PHONE, push: "expired" } });
    render(<SettingsBody />);
    expect(screen.getByRole("button", { name: "Turn notifications on again" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "Turn on notifications" })).toBeNull();
  });

  it("is off while the snapshot is stale", () => {
    live();
    useFleet.setState({ stale: true, link: "offline" });
    render(<SettingsBody />);
    expect(screen.getByRole("button", { name: "Turn on notifications" })).toBeDisabled();
  });

  it("reads Turning on… with its note while push.enable runs, and still does after a visit elsewhere", async () => {
    const registration = deferred<ServiceWorkerRegistration | null>();
    vi.mocked(pushSupported).mockReturnValue(true);
    vi.mocked(activeRegistration).mockReturnValue(registration.promise);
    vi.stubGlobal("Notification", { requestPermission: vi.fn().mockResolvedValue("granted") });
    live();
    const first = render(<SettingsBody />);

    await userEvent.setup().click(screen.getByRole("button", { name: "Turn on notifications" }));
    expect(await screen.findByRole("button", { name: "Turning on…" })).toBeDisabled();
    expect(screen.getByText("The phone can take a minute or two.")).toBeInTheDocument();

    first.unmount();
    render(<SettingsBody />);
    expect(screen.getByRole("button", { name: "Turning on…" })).toBeDisabled();

    await act(async () => {
      registration.resolve(null);
      await registration.promise;
    });
    expect(await screen.findByRole("button", { name: "Turn on notifications" })).toBeEnabled();
    expect(screen.queryByText("The phone can take a minute or two.")).toBeNull();
    expect(add).toHaveBeenCalledWith(expect.objectContaining({ type: "error" }));
  });
});

describe("SettingsBody: the expired or failing line", () => {
  it.each([
    ["expired", "Notifications stopped; turn them on again."],
    ["failing: 410 Gone", "Notifications are failing (410 Gone); turn them on again."],
  ])("says %s in red", (push, words) => {
    live({ device: { ...PHONE, push } });
    render(<SettingsBody />);
    expect(screen.getByText(words)).toHaveClass("text-error");
  });

  it.each(["", "ok"])("says nothing for a push state of %j", (push) => {
    live({ device: { ...PHONE, push } });
    render(<SettingsBody />);
    expect(screen.queryByText(/Notifications stopped|Notifications are failing/)).toBeNull();
  });
});

describe("SettingsBody: the notification Switches", () => {
  it("shows the stored values", () => {
    live({ device: { ...PHONE, notify: { ...PHONE.notify, waiting: true, finished: false } } });
    render(<SettingsBody />);
    expect(aSwitch("When a session needs you")).toHaveAttribute("aria-checked", "true");
    expect(aSwitch("When a session finishes")).toHaveAttribute("aria-checked", "false");
  });

  it("shows the value asked for while the write is in flight, holds a second tap, then shows the stored one", async () => {
    const write = deferred<Outcome<Device>>();
    const put = vi.spyOn(api, "putDevice").mockReturnValue(write.promise);
    live();
    render(<SettingsBody />);
    const user = userEvent.setup();
    const waiting = aSwitch("When a session needs you");

    await user.click(waiting);
    expect(put).toHaveBeenCalledTimes(1);
    expect(put).toHaveBeenCalledWith({ waiting: false });
    // Asked for, while the store still holds true.
    expect(waiting).toHaveAttribute("aria-checked", "false");
    expect(useFleet.getState().snapshot?.device.notify.waiting).toBe(true);

    // Off while its write runs, so the second tap reaches nothing and the asked value stays.
    expect(waiting).toHaveAttribute("data-disabled");
    await user.click(waiting);
    expect(put).toHaveBeenCalledTimes(1);
    expect(waiting).toHaveAttribute("aria-checked", "false");

    await act(async () => {
      write.resolve(ok({ ...PHONE, notify: { ...PHONE.notify, waiting: false } }));
      await write.promise;
    });
    expect(useFleet.getState().snapshot?.device.notify.waiting).toBe(false);
    expect(waiting).toHaveAttribute("aria-checked", "false");
  });

  it("writes only the field it is for", async () => {
    const put = vi
      .spyOn(api, "putDevice")
      .mockResolvedValue(ok({ ...PHONE, notify: { ...PHONE.notify, finished: true } }));
    live();
    render(<SettingsBody />);

    await userEvent.setup().click(aSwitch("When a session finishes"));
    await waitFor(() => expect(put).toHaveBeenCalledWith({ finished: true }));
    expect(put).toHaveBeenCalledTimes(1);
    await waitFor(() =>
      expect(aSwitch("When a session finishes")).toHaveAttribute("aria-checked", "true"),
    );
  });

  it("snaps back to the stored value, and toasts the daemon's words, when the write is refused", async () => {
    vi.spyOn(api, "putDevice").mockResolvedValue({
      kind: "refused",
      status: 400,
      message: "the device was unpaired",
    });
    live();
    render(<SettingsBody />);

    await userEvent.setup().click(aSwitch("When a session finishes"));
    await waitFor(() =>
      expect(add).toHaveBeenCalledWith(
        expect.objectContaining({ description: "the device was unpaired", type: "error" }),
      ),
    );
    await waitFor(() =>
      expect(aSwitch("When a session finishes")).toHaveAttribute("aria-checked", "false"),
    );
    expect(useFleet.getState().snapshot?.device.notify.finished).toBe(false);
  });

  it("shows what the daemon stored when it answers otherwise than asked", async () => {
    vi.spyOn(api, "putDevice").mockResolvedValue(
      ok({ ...PHONE, notify: { ...PHONE.notify, waiting: true } }),
    );
    live();
    render(<SettingsBody />);

    await userEvent.setup().click(aSwitch("When a session needs you"));
    await waitFor(() =>
      expect(aSwitch("When a session needs you")).toHaveAttribute("aria-checked", "true"),
    );
  });

  it("is off while the snapshot is stale", async () => {
    const put = vi.spyOn(api, "putDevice");
    live();
    useFleet.setState({ stale: true, link: "offline" });
    render(<SettingsBody />);

    // Story 2's Switch is a span, so a disabled one shows `data-disabled`, not the `disabled` property.
    expect(aSwitch("When a session needs you")).toHaveAttribute("data-disabled");
    expect(aSwitch("When a session finishes")).toHaveAttribute("data-disabled");

    await userEvent.setup().click(aSwitch("When a session needs you"));
    expect(put).not.toHaveBeenCalled();
    expect(aSwitch("When a session needs you")).toHaveAttribute("aria-checked", "true");
  });
});

describe("SettingsBody: the pause row", () => {
  it("offers Pause for an hour when not paused", async () => {
    const put = vi.spyOn(api, "putDevice").mockResolvedValue(ok(pausedFor(3_600_000)));
    live();
    render(<SettingsBody />);
    expect(screen.getByText("Not paused")).toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole("button", { name: "Pause for an hour" }));
    expect(put).toHaveBeenCalledWith({ pause: "1h" });
    expect(await screen.findByRole("button", { name: "Resume now" })).toBeInTheDocument();
    expect(screen.getByText(/^Paused until \d/)).toBeInTheDocument();
  });

  it("says when the pause ends and offers Resume now", async () => {
    const put = vi.spyOn(api, "putDevice").mockResolvedValue(ok(PHONE));
    live({ device: pausedFor(40 * 60_000) });
    render(<SettingsBody />);
    expect(screen.getByText(/^Paused until \d/)).toBeInTheDocument();
    expect(screen.queryByText("Not paused")).toBeNull();

    await userEvent.setup().click(screen.getByRole("button", { name: "Resume now" }));
    expect(put).toHaveBeenCalledWith({ pause: "none" });
    expect(await screen.findByText("Not paused")).toBeInTheDocument();
  });

  it("draws the row again when the pause ends", () => {
    vi.useRealTimers();
    vi.useFakeTimers({ now: NOW });
    live({ device: pausedFor(60_000) });
    render(<SettingsBody />);
    expect(screen.getByText(/^Paused until \d/)).toBeInTheDocument();

    act(() => {
      vi.advanceTimersByTime(59_000);
    });
    expect(screen.getByText(/^Paused until \d/)).toBeInTheDocument();

    act(() => {
      vi.advanceTimersByTime(1_000);
    });
    expect(screen.getByText("Not paused")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Pause for an hour" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Resume now" })).toBeNull();
  });

  it("holds Paused until past the longest timer delay and ends at the true end", () => {
    const MAX_DELAY = 2_147_483_647;
    vi.useRealTimers();
    vi.useFakeTimers({ now: NOW });
    // Fake timers accept any delay, a browser fires one past MAX_DELAY at once; so the delays asked
    // for are what pins the cap.
    const asked: number[] = [];
    const armed = globalThis.setTimeout;
    globalThis.setTimeout = ((handler: TimerHandler, delay?: number, ...args: unknown[]) => {
      asked.push(delay ?? 0);
      return armed(handler, delay, ...args);
    }) as typeof setTimeout;
    try {
      live({ device: pausedFor(MAX_DELAY + 60_000) });
      render(<SettingsBody />);
      expect(screen.getByText(/^Paused until \d/)).toBeInTheDocument();
      const delays = (): number[] => asked;
      expect(delays()).toContain(MAX_DELAY);
      expect(Math.max(...delays())).toBe(MAX_DELAY);

      // The first timer is capped at MAX_DELAY; its redraw arms the rest.
      act(() => {
        vi.advanceTimersByTime(MAX_DELAY);
      });
      expect(screen.getByText(/^Paused until \d/)).toBeInTheDocument();
      expect(delays()).toContain(60_000);

      act(() => {
        vi.advanceTimersByTime(59_000);
      });
      expect(screen.getByText(/^Paused until \d/)).toBeInTheDocument();

      act(() => {
        vi.advanceTimersByTime(1_000);
      });
      expect(screen.getByText("Not paused")).toBeInTheDocument();
    } finally {
      globalThis.setTimeout = armed;
    }
  });

  it("says Not paused for a pause that ended before the screen opened", () => {
    live({ device: pausedFor(-60_000) });
    render(<SettingsBody />);
    expect(screen.getByText("Not paused")).toBeInTheDocument();
  });
});

const repliesBox = (): HTMLTextAreaElement =>
  screen.getByRole<HTMLTextAreaElement>("textbox", { name: "Quick replies" });

describe("SettingsBody: Quick replies", () => {
  it("shows the stored list, one a line, with the classic note", () => {
    live({ quick_replies: ["ok", "later"] });
    render(<SettingsBody />);
    expect(repliesBox()).toHaveValue("ok\nlater");
    expect(
      screen.getByText(
        "One a line, at most 12. Every paired phone offers them above its reply box.",
      ),
    ).toBeInTheDocument();
  });

  it("shows an empty box for no stored list", () => {
    live({ quick_replies: null });
    render(<SettingsBody />);
    expect(repliesBox()).toHaveValue("");
  });

  it("follows the stored list until typed in, then keeps the draft over a new stored list", () => {
    live({ quick_replies: ["ok", "later"] });
    render(<SettingsBody />);

    act(() => useFleet.getState().apply("quick_replies", { quick_replies: ["x", "y"] }));
    expect(repliesBox()).toHaveValue("x\ny");

    fireEvent.change(repliesBox(), { target: { value: "mine" } });
    expect(repliesBox()).toHaveValue("mine");

    act(() => useFleet.getState().apply("quick_replies", { quick_replies: ["z"] }));
    expect(repliesBox()).toHaveValue("mine");
  });

  it("leaves Save off until the person has typed, and on after", () => {
    live();
    render(<SettingsBody />);
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Reset to defaults" })).toBeEnabled();

    fireEvent.change(repliesBox(), { target: { value: "ok" } });
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("saves the lines trimmed with the blanks dropped, then shows the stored list again", async () => {
    const put = vi
      .spyOn(api, "putQuickReplies")
      .mockResolvedValue(ok<QuickReplies>({ quick_replies: ["ok", "later"] }));
    live({ quick_replies: ["old"] });
    render(<SettingsBody />);

    fireEvent.change(repliesBox(), { target: { value: "  ok  \n\n later\n   \n" } });
    await userEvent.setup().click(screen.getByRole("button", { name: "Save" }));

    expect(put).toHaveBeenCalledTimes(1);
    expect(put).toHaveBeenCalledWith({ replies: ["ok", "later"] });
    await waitFor(() => expect(repliesBox()).toHaveValue("ok\nlater"));
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("resets to the defaults the daemon answers, sending no list", async () => {
    const put = vi
      .spyOn(api, "putQuickReplies")
      .mockResolvedValue(ok<QuickReplies>({ quick_replies: ["yes", "continue"] }));
    live({ quick_replies: ["old"] });
    render(<SettingsBody />);

    fireEvent.change(repliesBox(), { target: { value: "typed" } });
    await userEvent.setup().click(screen.getByRole("button", { name: "Reset to defaults" }));

    expect(put).toHaveBeenCalledTimes(1);
    expect(put.mock.calls[0]?.[0]).toStrictEqual({});
    await waitFor(() => expect(repliesBox()).toHaveValue("yes\ncontinue"));
  });

  it("keeps the typed text, and Save on, when the daemon refuses the list", async () => {
    vi.spyOn(api, "putQuickReplies").mockResolvedValue({
      kind: "refused",
      status: 400,
      message: "at most 12 quick replies",
    });
    live({ quick_replies: ["old"] });
    render(<SettingsBody />);

    fireEvent.change(repliesBox(), { target: { value: "one\ntwo" } });
    await userEvent.setup().click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(add).toHaveBeenCalledWith(
        expect.objectContaining({ description: "at most 12 quick replies", type: "error" }),
      ),
    );
    await waitFor(() => expect(repliesBox()).toBeEnabled());
    expect(repliesBox()).toHaveValue("one\ntwo");
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("holds the box and both buttons while a save is in flight", async () => {
    const write = deferred<Outcome<QuickReplies>>();
    const put = vi.spyOn(api, "putQuickReplies").mockReturnValue(write.promise);
    live();
    render(<SettingsBody />);
    const user = userEvent.setup();

    fireEvent.change(repliesBox(), { target: { value: "one" } });
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(repliesBox()).toBeDisabled();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Reset to defaults" })).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Reset to defaults" }));
    expect(put).toHaveBeenCalledTimes(1);

    await act(async () => {
      write.resolve(ok<QuickReplies>({ quick_replies: ["one"] }));
      await write.promise;
    });
    expect(repliesBox()).toBeEnabled();
  });

  it("is off while the snapshot is stale", () => {
    live();
    useFleet.setState({ stale: true, link: "offline" });
    render(<SettingsBody />);
    expect(repliesBox()).toBeDisabled();
    expect(screen.getByRole("button", { name: "Reset to defaults" })).toBeDisabled();
  });
});
