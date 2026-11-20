import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Ask, Session } from "@/api/gen";
import { api } from "@/api/request";
import "@/commands";
import { askConfirm } from "@/app/ConfirmSheet";
import { toastManager } from "@/components/ui/toast";
import { makeSession, noticesOk, resetFleet, seed } from "@/commands/testkit";
import { MAX_MESSAGE_BYTES } from "@/lib/utf8";
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

  describe("a question ask", () => {
    const questions = question.questions ?? [];
    const q0 = questions[0];
    const q1 = questions[1];
    if (q0 === undefined || q1 === undefined)
      throw new Error("the canned question needs two questions");
    const Q0 = q0.question;
    const Q1 = q1.question;
    const o0 = q0.options?.[0]?.label ?? "";
    const o1 = q0.options?.[1]?.label ?? "";
    const m0 = q1.options?.[0]?.label ?? "";
    const m1 = q1.options?.[1]?.label ?? "";
    const withPreview = q0.options?.find((o) => o.preview !== undefined && o.preview !== "");
    const radio = (label: string): HTMLInputElement => screen.getByRole("radio", { name: label });
    const checkbox = (label: string): HTMLInputElement =>
      screen.getByRole("checkbox", { name: label });
    const other = (q: string): HTMLInputElement =>
      screen.getByRole("textbox", { name: `Other answer to ${q}` });
    const note = (q: string): HTMLInputElement =>
      screen.getByRole("textbox", { name: `Note on ${q}` });
    const answerButton = (): HTMLElement => screen.getByRole("button", { name: "Answer" });
    const draftOf = () => useDrafts.getState().asks["fixture-question"];

    it("draws a radio per option of a single-select question and a checkbox per option of a multi-select one", () => {
      setup([question]);
      for (const o of q0.options ?? []) expect(radio(o.label)).toBeInTheDocument();
      expect(screen.queryAllByRole("checkbox").length).toBe(q1.options?.length);
      expect(screen.queryAllByRole("radio").length).toBe(q0.options?.length);
      for (const o of q1.options ?? []) expect(checkbox(o.label)).toBeInTheDocument();
      for (const q of questions) {
        expect(screen.getByText(q.header)).toBeInTheDocument();
        expect(screen.getByText(q.question)).toBeInTheDocument();
        for (const o of q.options ?? [])
          if (o.description !== "") expect(screen.getByText(o.description)).toBeInTheDocument();
      }
    });

    it("picking another option of a single-select question replaces the pick", async () => {
      setup([question]);
      await userEvent.click(radio(o0));
      await userEvent.click(radio(o1));
      expect(radio(o1)).toBeChecked();
      expect(radio(o0)).not.toBeChecked();
      expect(draftOf()?.answers[Q0]?.labels).toEqual([o1]);
    });

    it("a multi-select keeps its picks in the order picked, and unpicking removes one", async () => {
      setup([question]);
      await userEvent.click(checkbox(m1));
      await userEvent.click(checkbox(m0));
      expect(draftOf()?.answers[Q1]?.labels).toEqual([m1, m0]);
      await userEvent.click(checkbox(m1));
      expect(draftOf()?.answers[Q1]?.labels).toEqual([m0]);
    });

    it("typing Other on a single-select question clears its pick, picking clears Other, and a multi-select keeps both", async () => {
      setup([question]);
      await userEvent.click(radio(o0));
      await userEvent.type(other(Q0), "mine");
      expect(radio(o0)).not.toBeChecked();
      expect(draftOf()?.answers[Q0]?.labels).toEqual([]);
      await userEvent.click(radio(o0));
      expect(other(Q0).value).toBe("");
      await userEvent.click(checkbox(m0));
      await userEvent.type(other(Q1), "mine");
      expect(checkbox(m0)).toBeChecked();
      expect(draftOf()?.answers[Q1]?.other).toBe("mine");
    });

    it("shows the preview of the last picked option that has one", async () => {
      const preview = withPreview?.preview ?? "";
      expect(preview).not.toBe("");
      const { unmount } = setup([question]);
      await userEvent.click(radio(withPreview?.label ?? ""));
      expect(screen.getByText(preview, { normalizer: (t) => t }).closest("pre")).not.toBeNull();
      await userEvent.click(radio(q0.options?.find((o) => o.preview === undefined)?.label ?? ""));
      expect(screen.queryByText(preview, { normalizer: (t) => t })).toBeNull();
      unmount();
      useDrafts.getState().clear();
      const own: Ask = {
        ...question,
        questions: [
          {
            ...q1,
            options: [
              { label: "p", description: "", preview: "PREVIEW TEXT" },
              { label: "q", description: "" },
            ],
          },
        ],
      };
      setup([own]);
      await userEvent.click(checkbox("p"));
      expect(screen.getByText("PREVIEW TEXT").closest("pre")).not.toBeNull();
      await userEvent.click(checkbox("q"));
      expect(screen.getByText("PREVIEW TEXT")).toBeInTheDocument();
    });

    it("Answer is off until every question has a pick or an Other text", async () => {
      setup([question]);
      expect(answerButton()).toBeDisabled();
      await userEvent.click(radio(o0));
      expect(answerButton()).toBeDisabled();
      fireEvent.change(other(Q1), { target: { value: "   " } });
      expect(answerButton()).toBeDisabled();
      fireEvent.change(other(Q1), { target: { value: "x" } });
      expect(answerButton()).toBeEnabled();
      fireEvent.change(other(Q1), { target: { value: "" } });
      expect(answerButton()).toBeDisabled();
    });

    it("Answer sends the body answerBody builds and a 200 drops the draft", async () => {
      const spy = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());
      setup([question]);
      await userEvent.click(radio(o0));
      fireEvent.change(other(Q1), { target: { value: "x" } });
      fireEvent.change(note(Q1), { target: { value: "  n  " } });
      await userEvent.click(answerButton());
      await waitFor(() =>
        expect(spy).toHaveBeenCalledWith(2, "fixture-question", {
          answers: { [Q0]: { labels: [o0] }, [Q1]: { labels: [], other: "x", note: "n" } },
        }),
      );
      await waitFor(() => expect(radio(o0)).not.toBeChecked());
      expect(draftOf()).toBeUndefined();
    });

    it("an Other text or a note past 64 KiB says so and turns Answer off", async () => {
      setup([question]);
      await userEvent.click(radio(o0));
      const long = (n: number): string => "a".repeat(n);
      fireEvent.change(other(Q1), { target: { value: long(MAX_MESSAGE_BYTES) } });
      expect(answerButton()).toBeEnabled();
      expect(screen.queryByText(/Too long/)).toBeNull();
      fireEvent.change(other(Q1), { target: { value: long(MAX_MESSAGE_BYTES + 1) } });
      expect(screen.getByText("Too long: an answer is at most 64 KiB.")).toBeInTheDocument();
      expect(answerButton()).toBeDisabled();
      fireEvent.change(other(Q1), { target: { value: "x" } });
      fireEvent.change(note(Q1), { target: { value: long(MAX_MESSAGE_BYTES) } });
      expect(answerButton()).toBeEnabled();
      fireEvent.change(note(Q1), { target: { value: long(MAX_MESSAGE_BYTES + 1) } });
      expect(screen.getByText("Too long: an answer is at most 64 KiB.")).toBeInTheDocument();
      expect(answerButton()).toBeDisabled();
    });

    it("an Other of exactly 64 KiB plus a trailing space says Too long and turns Answer off", async () => {
      setup([question]);
      await userEvent.click(radio(o0));
      fireEvent.change(other(Q1), { target: { value: `${"a".repeat(MAX_MESSAGE_BYTES)} ` } });
      expect(screen.getByText("Too long: an answer is at most 64 KiB.")).toBeInTheDocument();
      expect(answerButton()).toBeDisabled();
    });

    it("Chat about this sends the sheet's text and nothing when the sheet is cancelled", async () => {
      const spy = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());
      setup([question]);
      vi.mocked(askConfirm).mockResolvedValue("what does x mean");
      await userEvent.click(screen.getByRole("button", { name: "Chat about this" }));
      await waitFor(() =>
        expect(spy).toHaveBeenCalledWith(2, "fixture-question", {
          chat: true,
          chat_text: "what does x mean",
        }),
      );
      spy.mockClear();
      vi.mocked(askConfirm).mockResolvedValue(null);
      await userEvent.click(screen.getByRole("button", { name: "Chat about this" }));
      expect(spy).not.toHaveBeenCalled();
    });

    it("keeps the picks across a remount", async () => {
      const { unmount } = setup([question]);
      await userEvent.click(radio(o0));
      fireEvent.change(note(Q1), { target: { value: "kept" } });
      unmount();
      setup([question]);
      expect(radio(o0)).toBeChecked();
      expect(note(Q1).value).toBe("kept");
    });

    it("the inputs and both buttons are off while the ask is answering, and the inputs while the snapshot is stale", () => {
      const { unmount } = setup([{ ...question, answering: true }]);
      for (const el of [
        ...screen.getAllByRole("radio"),
        ...screen.getAllByRole("checkbox"),
        ...screen.getAllByRole("textbox"),
        answerButton(),
        screen.getByRole("button", { name: "Chat about this" }),
      ])
        expect(el).toBeDisabled();
      unmount();
      setup([question]);
      act(() => {
        useFleet.setState({ stale: true });
      });
      for (const el of [...screen.getAllByRole("radio"), ...screen.getAllByRole("textbox")])
        expect(el).toBeDisabled();
    });
  });
});
