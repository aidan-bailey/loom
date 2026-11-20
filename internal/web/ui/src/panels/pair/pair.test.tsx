import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";

import { api } from "@/api/request";
import { panelFor } from "@/app/panels";
import "@/commands/device";
import { deferred, resetFleet, spyOnAdd } from "@/commands/testkit";
import { fixtureSnapshot } from "@/fixtures";
import { startFleet, useFleet } from "@/store/fleet";

import "./index";
import { CODE_LENGTH, normalizeCode, Pair, PAIRING_ALPHABET } from "./Pair";

vi.mock("@/store/fleet", async (original) => ({
  ...(await original<typeof import("@/store/fleet")>()),
  startFleet: vi.fn(() => () => undefined),
}));

describe("normalizeCode", () => {
  it("normalizeCode: upper-cases what the alphabet holds", () => {
    expect(normalizeCode("k7qm2xpa")).toBe("K7QM2XPA");
    expect(PAIRING_ALPHABET).toHaveLength(31);
    for (const ch of ["0", "1", "I", "L", "O"]) expect(PAIRING_ALPHABET).not.toContain(ch);
    const first = PAIRING_ALPHABET.slice(0, CODE_LENGTH);
    expect(normalizeCode(first)).toBe(first);
  });

  it("normalizeCode: drops every character the alphabet lacks, as typed or pasted", () => {
    expect(normalizeCode("K7QM-2XPA")).toBe("K7QM2XPA");
    expect(normalizeCode("O0I1L")).toBe("");
    expect(normalizeCode("k7 qm")).toBe("K7QM");
    expect(normalizeCode("k7qmo02xpa")).toBe("K7QM2XPA");
  });

  it("normalizeCode: clamps to 8 after the filter", () => {
    expect(normalizeCode("K7QM2XPA9")).toBe("K7QM2XPA");
    expect(normalizeCode("K7QMO2XPA")).toBe("K7QM2XPA");
    expect(normalizeCode("23456789ABC")).toBe("23456789");
  });

  it("normalizeCode: is idempotent", () => {
    for (const x of ["k7qm-2xpa9", "ßß", "oO0", "", PAIRING_ALPHABET, "k7qmo02xpa"]) {
      expect(normalizeCode(normalizeCode(x))).toBe(normalizeCode(x));
    }
  });
});

let pair: MockInstance<typeof api.pair>;

beforeEach(() => {
  window.location.hash = "#/pair";
  pair = vi
    .spyOn(api, "pair")
    .mockResolvedValue({ kind: "ok", status: 200, value: { device: "iPhone" }, notices: [] });
  vi.spyOn(window.location, "assign").mockImplementation(() => undefined);
  spyOnAdd();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.mocked(startFleet).mockClear();
  resetFleet();
  window.location.hash = "";
});

function firstSlot(): HTMLElement {
  return screen.getByRole("textbox", { name: "Pairing code" });
}

function slots(): HTMLInputElement[] {
  return Array.from(document.querySelectorAll<HTMLInputElement>('[data-slot="input-otp-slot"]'));
}

function slotValues(): string[] {
  return slots().map((s) => s.value);
}

// type clicks the first slot and types text, which Base UI spreads over the slots.
async function type(user: ReturnType<typeof userEvent.setup>, text: string): Promise<void> {
  await user.click(firstSlot());
  await user.keyboard(text);
}

describe("Pair", () => {
  it("Pair: draws the wordmark, the lede, the two steps and eight slots in two groups of four", () => {
    render(<Pair />);
    expect(screen.getByRole("heading", { level: 1, name: "Loom" })).toBeTruthy();
    expect(screen.getByText("Pair this phone with your loom.")).toBeTruthy();
    const steps = screen.getAllByRole("listitem");
    expect(steps).toHaveLength(2);
    expect(steps[0]?.textContent).toBe("On the computer running loom, run loom web pair.");
    expect(steps[0]?.querySelector("code")?.textContent).toBe("loom web pair");
    expect(steps[1]?.textContent).toBe("Enter the code it prints. It works once, for 10 minutes.");
    const all = slots();
    expect(all).toHaveLength(8);
    expect(all[0]).toBe(firstSlot());
    expect(screen.getByRole("textbox", { name: "Character 2 of 8" })).toBe(all[1]);
    expect(screen.getByRole("textbox", { name: "Character 8 of 8" })).toBe(all[7]);
    expect(document.querySelectorAll('[data-slot="input-otp-separator"]')).toHaveLength(1);
    const left = all[0]?.parentElement;
    const right = all[4]?.parentElement;
    expect(left).not.toBe(right);
    expect(all.slice(0, 4).every((s) => s.parentElement === left)).toBe(true);
    expect(all.slice(4).every((s) => s.parentElement === right)).toBe(true);
  });

  it("Pair: Pair is off with seven characters and on with eight", async () => {
    const user = userEvent.setup();
    render(<Pair />);
    await type(user, "K7QM2XP");
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "Pair" }).disabled).toBe(true);
    await user.keyboard("A");
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "Pair" }).disabled).toBe(false);
  });

  it("Pair: typing drops what the alphabet lacks, so a mistyped O never reaches the daemon", async () => {
    const user = userEvent.setup();
    render(<Pair />);
    await type(user, "k7qmo02xpa");
    expect(slotValues()).toEqual(["K", "7", "Q", "M", "2", "X", "P", "A"]);
    await user.click(screen.getByRole("button", { name: "Pair" }));
    expect(pair).toHaveBeenCalledTimes(1);
    expect(pair).toHaveBeenCalledWith({ code: "K7QM2XPA" });
  });

  it("Pair: a paste is upper-cased, loses its dash and is clamped", async () => {
    const user = userEvent.setup();
    render(<Pair />);
    await user.click(firstSlot());
    await user.paste("k7qm-2xpa9");
    expect(slotValues().join("")).toBe("K7QM2XPA");
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "Pair" }).disabled).toBe(false);
  });

  it("Pair: filling the eight slots sends nothing until Pair is pressed", async () => {
    const user = userEvent.setup();
    render(<Pair />);
    await type(user, "K7QM2XPA");
    await waitFor(() => Promise.resolve());
    expect(pair).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Pair" }));
    expect(pair).toHaveBeenCalledTimes(1);
  });

  it("Pair: Enter in a slot pairs once at eight characters and does nothing before", async () => {
    const user = userEvent.setup();
    render(<Pair />);
    await type(user, "K7QM2XP");
    await user.keyboard("{Enter}");
    expect(pair).not.toHaveBeenCalled();
    await user.keyboard("A");
    await user.keyboard("{Enter}");
    expect(pair).toHaveBeenCalledTimes(1);
    expect(pair).toHaveBeenCalledWith({ code: "K7QM2XPA" });
  });

  it('Pair: the button reads "Pairing…" and is off while the request is in flight, and a refusal keeps the code', async () => {
    const held = deferred<Awaited<ReturnType<typeof api.pair>>>();
    pair.mockReturnValue(held.promise);
    const user = userEvent.setup();
    render(<Pair />);
    await type(user, "K7QM2XPA");
    await user.click(screen.getByRole("button", { name: "Pair" }));
    const busy = await screen.findByRole<HTMLButtonElement>("button", { name: "Pairing…" });
    expect(busy.disabled).toBe(true);
    await user.click(firstSlot());
    await user.keyboard("{Enter}");
    expect(pair).toHaveBeenCalledTimes(1);
    await act(async () => {
      held.resolve({ kind: "refused", status: 400, message: "that code is not valid" });
      await held.promise;
    });
    const back = await screen.findByRole<HTMLButtonElement>("button", { name: "Pair" });
    expect(back.disabled).toBe(false);
    expect(slotValues().join("")).toBe("K7QM2XPA");
  });

  it("Pair: a live snapshot opens the fleet, a stale or absent one does not", () => {
    render(<Pair />);
    act(() => {
      useFleet.setState({ snapshot: fixtureSnapshot(), stale: true, link: "offline" });
    });
    expect(window.location.hash).toBe("#/pair");
    act(() => {
      useFleet.setState({ snapshot: null, stale: false });
    });
    expect(window.location.hash).toBe("#/pair");
    act(() => {
      useFleet.setState({ snapshot: fixtureSnapshot(), stale: false, link: "live" });
    });
    expect(window.location.hash).toBe("#/");
  });
});

describe("the pair panel", () => {
  it("the pair panel: is registered with no head and Pair as its body, and @/panels registers it", async () => {
    const panel = panelFor("pair");
    expect(panel?.Body).toBe(Pair);
    expect(panel?.Head).toBeUndefined();
    expect(panel?.tab).toBeUndefined();
    vi.resetModules();
    await import("@/panels");
    const { panelFor: fresh } = await import("@/app/panels");
    expect(fresh("pair")).toBeDefined();
  });
});
