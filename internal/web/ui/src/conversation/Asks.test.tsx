import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Ask, Session } from "@/api/gen";
import { api } from "@/api/request";
import "@/commands";
import { askConfirm } from "@/app/ConfirmSheet";
import { toastManager } from "@/components/ui/toast";
import { makeSession, noticesOk, resetFleet, seed } from "@/commands/testkit";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "@/fleet/groups";
import { useDrafts } from "@/store/drafts";
import { useFleet } from "@/store/fleet";

import { Asks } from "./Asks";

vi.mock("@/app/ConfirmSheet", async (original) => ({
  ...(await original<typeof import("@/app/ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));

const canned = allSessions(fixtureSnapshot()).find((s) => s.id === 2)?.asks ?? [];
const permission = canned.find((a) => a.id === "fixture-permission") as Ask;
const question = canned.find((a) => a.id === "fixture-question") as Ask;

function setup(asks: Ask[] | null): ReturnType<typeof render> {
  const session: Session = makeSession({
    id: 2,
    status: "Prompting",
    group: "needs_you",
    asks,
  });
  seed([session]);
  return render(<Asks session={session} />);
}

beforeEach(() => {
  vi.spyOn(toastManager, "add").mockImplementation(() => "toast");
  window.history.replaceState(null, "", "#/s/2");
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.mocked(askConfirm).mockReset();
  resetFleet();
  useDrafts.getState().clear();
  window.history.replaceState(null, "", window.location.pathname);
});

describe("Asks", () => {
  it("draws a card per ask in order, and nothing for a session with no asks", () => {
    const { container, unmount } = setup([question, permission]);
    const cards = Array.from(container.querySelectorAll("[data-ask]"));
    expect(cards.map((c) => c.getAttribute("data-ask"))).toEqual([
      "fixture-question",
      "fixture-permission",
    ]);
    unmount();
    const empty = setup([]);
    expect(empty.container).toBeEmptyDOMElement();
    empty.unmount();
    expect(setup(null).container).toBeEmptyDOMElement();
  });

  it("names the subagent that asked", () => {
    expect(question.agent).not.toBe("");
    const { unmount } = setup([question]);
    expect(screen.getByText(`asked by ${question.agent}`)).toBeInTheDocument();
    unmount();
    setup([permission]);
    expect(screen.queryByText(/asked by/)).toBeNull();
  });

  it("an ask of a kind this build does not know draws a neutral card that names it and has no answer buttons", () => {
    const { container } = setup([{ ...permission, kind: "plan" as Ask["kind"] }]);
    expect(screen.getByText("plan")).toBeInTheDocument();
    expect(screen.getByText(/This kind of ask is answered in Claude\./)).toBeInTheDocument();
    expect(container.querySelector("button")).toBeNull();
  });

  describe("a permission ask", () => {
    it("draws the tool, each input block, a mono block as preformatted text and a cut block marked", () => {
      const input = [
        ...(permission.input ?? []),
        { label: "command", text: "ls", mono: true, cut: true },
      ];
      setup([{ ...permission, input }]);
      expect(screen.getByText(permission.tool ?? "")).toBeInTheDocument();
      for (const block of permission.input ?? []) {
        expect(screen.getByText(block.label)).toBeInTheDocument();
        const text = screen.getByText(block.text);
        expect(text.closest("pre") !== null).toBe(block.mono === true);
      }
      expect(screen.getByText("command (cut)")).toBeInTheDocument();
      expect(screen.getByText("ls").closest("pre")).not.toBeNull();
    });

    it("Allow answers allow: true with no suggestion", async () => {
      const spy = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());
      setup([permission]);
      await userEvent.click(screen.getByRole("button", { name: "Allow" }));
      expect(spy).toHaveBeenCalledWith(2, "fixture-permission", { allow: true });
      expect(spy.mock.calls[0]?.[2]).not.toHaveProperty("suggestion");
    });

    it("a suggestion that does not persist answers at once with its 1-based number", async () => {
      const spy = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());
      setup([permission]);
      const suggestions = permission.suggestions ?? [];
      const i = suggestions.findIndex((g) => g.persists !== true);
      const label = suggestions[i]?.label ?? "";
      await userEvent.click(screen.getByRole("button", { name: label }));
      expect(askConfirm).not.toHaveBeenCalled();
      expect(spy).toHaveBeenCalledWith(2, "fixture-permission", { allow: true, suggestion: i + 1 });
    });

    it("a suggestion that persists asks first: cancelled sends nothing, confirmed sends its number", async () => {
      const spy = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());
      setup([permission]);
      const suggestions = permission.suggestions ?? [];
      const m = suggestions.findIndex((g) => g.persists === true);
      const label = suggestions[m]?.label ?? "";
      vi.mocked(askConfirm).mockResolvedValue(null);
      await userEvent.click(screen.getByRole("button", { name: label }));
      expect(askConfirm).toHaveBeenCalledTimes(1);
      expect(spy).not.toHaveBeenCalled();
      vi.mocked(askConfirm).mockResolvedValue("");
      await userEvent.click(screen.getByRole("button", { name: label }));
      await vi.waitFor(() =>
        expect(spy).toHaveBeenCalledWith(2, "fixture-permission", {
          allow: true,
          suggestion: m + 1,
        }),
      );
    });

    it("Deny sends the typed message trimmed, and nothing when it is cancelled", async () => {
      const spy = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());
      setup([permission]);
      vi.mocked(askConfirm).mockResolvedValue("  use ls instead  ");
      await userEvent.click(screen.getByRole("button", { name: "Deny…" }));
      await vi.waitFor(() =>
        expect(spy).toHaveBeenCalledWith(2, "fixture-permission", {
          allow: false,
          message: "use ls instead",
        }),
      );
      spy.mockClear();
      vi.mocked(askConfirm).mockResolvedValue(null);
      await userEvent.click(screen.getByRole("button", { name: "Deny…" }));
      expect(spy).not.toHaveBeenCalled();
    });

    it("an answering card is off and says Sending…", () => {
      setup([{ ...permission, answering: true }]);
      expect(screen.getByText("Sending…")).toBeInTheDocument();
      const buttons = screen.getAllByRole("button");
      expect(buttons.length).toBe(2 + (permission.suggestions?.length ?? 0));
      for (const b of buttons) expect(b).toBeDisabled();
    });

    it("a card's buttons are off while the snapshot is stale", () => {
      setup([permission]);
      act(() => {
        useFleet.setState({ stale: true });
      });
      expect(screen.getByRole("button", { name: "Allow" })).toBeDisabled();
    });
  });
});
