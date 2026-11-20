import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import "@/commands";
import type { Device, Notice, Outcome, Snapshot } from "@/api/gen";
import { api } from "@/api/request";
import { askConfirm } from "@/app/ConfirmSheet";
import { panelFor } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { askedSheet, resetFleet } from "@/commands/testkit";
import { fixtureSnapshot } from "@/fixtures";
import { useFleet } from "@/store/fleet";

import { SettingsHead, settingsPanel } from "./index";
import SettingsBody from "./SettingsBody";

vi.mock("@/app/ConfirmSheet", async (original) => ({
  ...(await original<typeof import("@/app/ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));

const NOW = Date.parse("2026-11-20T12:00:00Z");

const PHONE: Device = {
  name: "iphone",
  notify: { waiting: true, finished: false, paused_until: "" },
  push: "ok",
};

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
  window.history.replaceState(null, "", "#/settings");
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
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
