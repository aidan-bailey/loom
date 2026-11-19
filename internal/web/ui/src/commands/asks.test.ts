import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { AnswerBody, Ask, AskQuestion } from "@/api/gen";
import { api } from "@/api/request";
import { commandEnabled, runCommand } from "@/app/commands";
import { askConfirm } from "@/app/ConfirmSheet";
import { MAX_MESSAGE_BYTES } from "@/lib/utf8";
import { useDrafts, type AskDraft, type QuestionDraft } from "@/store/drafts";

import { allowArgs, answerBody } from "./asks";
import {
  askedSheet,
  command,
  context,
  deferred,
  makeSession,
  noticesOk,
  resetFleet,
  seed,
  spyOnAdd,
} from "./testkit";

vi.mock("@/app/ConfirmSheet", async (original) => ({
  ...(await original<typeof import("@/app/ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));

let add: ReturnType<typeof spyOnAdd>;

beforeEach(() => {
  add = spyOnAdd();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.mocked(askConfirm).mockReset();
  resetFleet();
  useDrafts.getState().clear();
});

function question(text: string, multi: boolean): AskQuestion {
  return {
    question: text,
    header: "Pick",
    multi,
    options: [
      { label: "a", description: "" },
      { label: "b", description: "" },
    ],
  };
}

const questionAsk: Ask = {
  id: "ask-1",
  kind: "question",
  agent: "",
  opened: "2026-11-19T10:00:00Z",
  answering: false,
  questions: [question("Which?", false), question("Which all?", true)],
};

const permissionAsk: Ask = {
  id: "perm-1",
  kind: "permission",
  agent: "",
  opened: "2026-11-19T10:00:00Z",
  answering: false,
  tool: "Bash",
  suggestions: [
    { label: "Always allow ls", persists: true },
    { label: "Allow ls this session", persists: false },
    { label: "Allow ls once more" },
  ],
};

const picked = (labels: string[], other = "", note = ""): QuestionDraft => ({
  labels,
  other,
  note,
});

const drafted = (one: QuestionDraft, two: QuestionDraft): AskDraft => ({
  answers: { "Which?": one, "Which all?": two },
});

describe("allowArgs", () => {
  it("is the plain Allow at suggestion 0: no rule to save", () => {
    expect(allowArgs(permissionAsk, 0)).toEqual({
      ask: "perm-1",
      suggestion: 0,
      label: "Allow",
      persists: false,
    });
  });

  it("is the 1-based suggestion with its label, persisting when that suggestion does", () => {
    expect(allowArgs(permissionAsk, 1)).toEqual({
      ask: "perm-1",
      suggestion: 1,
      label: "Always allow ls",
      persists: true,
    });
    expect(allowArgs(permissionAsk, 2)).toEqual({
      ask: "perm-1",
      suggestion: 2,
      label: "Allow ls this session",
      persists: false,
    });
  });

  it("does not persist a suggestion that does not say so", () => {
    expect(allowArgs(permissionAsk, 3).persists).toBe(false);
  });

  it("refuses a suggestion the ask does not hold, rather than allowing something else", () => {
    expect(() => allowArgs(permissionAsk, 4)).toThrow("no suggestion 4");
    expect(() => allowArgs({ ...permissionAsk, suggestions: undefined }, 1)).toThrow(
      "no suggestion 1",
    );
  });
});

describe("answerBody", () => {
  it("is null until every question has a picked label or an Other text", () => {
    expect(answerBody(questionAsk, undefined)).toBeNull();
    expect(answerBody(questionAsk, drafted(picked([]), picked(["a"])))).toBeNull();
    expect(answerBody(questionAsk, drafted(picked(["a"]), picked([])))).toBeNull();
    expect(answerBody(questionAsk, drafted(picked(["a"]), picked(["a"])))).not.toBeNull();
    expect(answerBody(questionAsk, drafted(picked([], "mine"), picked([], "ours")))).not.toBeNull();
  });

  it("does not count an Other text of whitespace, or a note alone, as an answer", () => {
    expect(answerBody(questionAsk, drafted(picked([], "   "), picked(["a"])))).toBeNull();
    expect(answerBody(questionAsk, drafted(picked([], "", "a note"), picked(["a"])))).toBeNull();
  });

  it("is null when the draft lacks one of the questions", () => {
    const draft: AskDraft = { answers: { "Which?": picked(["a"]) } };
    expect(answerBody(questionAsk, draft)).toBeNull();
  });

  it("is null for an ask with no questions", () => {
    expect(answerBody({ ...questionAsk, questions: [] }, { answers: {} })).toBeNull();
    expect(answerBody({ ...questionAsk, questions: undefined }, { answers: {} })).toBeNull();
  });

  it("keys each answer by its question's text, labels in pick order, other and note trimmed", () => {
    const body = answerBody(
      questionAsk,
      drafted(picked(["a"], "", "  because  "), picked(["b", "a"], "  extra  ")),
    );
    expect(body).toStrictEqual({
      answers: {
        "Which?": { labels: ["a"], note: "because" },
        "Which all?": { labels: ["b", "a"], other: "extra" },
      },
    });
  });

  it("leaves other and note out when they are empty after trimming", () => {
    const body = answerBody(questionAsk, drafted(picked(["a"], "  ", "\n"), picked(["b"])));
    expect(body).toStrictEqual({
      answers: { "Which?": { labels: ["a"] }, "Which all?": { labels: ["b"] } },
    });
  });

  it("answers a single-select question with its Other text alone", () => {
    const body = answerBody(questionAsk, drafted(picked([], " mine "), picked(["a"])));
    expect(body).toStrictEqual({
      answers: { "Which?": { labels: [], other: "mine" }, "Which all?": { labels: ["a"] } },
    });
  });

  it("gives a single-select question one answer, a label or Other and never both", () => {
    const both = answerBody(questionAsk, drafted(picked(["b"], "typed"), picked(["a"])));
    expect(both).toStrictEqual({
      answers: { "Which?": { labels: ["b"] }, "Which all?": { labels: ["a"] } },
    });
    const two = answerBody(questionAsk, drafted(picked(["a", "b"]), picked(["a"])));
    expect(two?.answers?.["Which?"]).toStrictEqual({ labels: ["b"] });
  });

  it("lets a multi-select question carry labels and Other together", () => {
    const body = answerBody(questionAsk, drafted(picked(["a"]), picked(["a", "b"], "and more")));
    expect(body?.answers?.["Which all?"]).toStrictEqual({
      labels: ["a", "b"],
      other: "and more",
    });
  });

  it("is null while an Other text or a note passes the limit in bytes, and not at it", () => {
    const at = "é".repeat(MAX_MESSAGE_BYTES / 2);
    const over = at + "a";
    expect(answerBody(questionAsk, drafted(picked(["a"]), picked(["a"], at)))).not.toBeNull();
    expect(answerBody(questionAsk, drafted(picked(["a"]), picked(["a"], over)))).toBeNull();
    expect(answerBody(questionAsk, drafted(picked(["a"], "", at), picked(["a"])))).not.toBeNull();
    expect(answerBody(questionAsk, drafted(picked(["a"], "", over), picked(["a"])))).toBeNull();
  });

  it("measures the limit on the trimmed text it would send, not the whitespace around it", () => {
    const at = "é".repeat(MAX_MESSAGE_BYTES / 2);
    const padded = drafted(picked(["a"]), picked(["a"], ` ${at}\n`));
    expect(answerBody(questionAsk, padded)).not.toBeNull();
    const notePadded = drafted(picked(["a"], "", `${at}  `), picked(["a"]));
    expect(answerBody(questionAsk, notePadded)).not.toBeNull();
  });

  it("does not measure an Other text that a single-select question's label drops", () => {
    const over = "é".repeat(MAX_MESSAGE_BYTES / 2 + 1);
    const body = answerBody(questionAsk, drafted(picked(["a"], over), picked(["a"])));
    expect(body?.answers?.["Which?"]).toStrictEqual({ labels: ["a"] });
    const alone = answerBody(questionAsk, drafted(picked([], over), picked(["a"])));
    expect(alone).toBeNull();
  });

  it("counts bytes, not characters: 32769 two-byte characters is over the limit", () => {
    const text = "é".repeat(MAX_MESSAGE_BYTES / 2 + 1);
    expect(text.length).toBeLessThan(MAX_MESSAGE_BYTES);
    expect(answerBody(questionAsk, drafted(picked(["a"]), picked(["a"], text)))).toBeNull();
  });
});

describe("the ask commands' availability", () => {
  it.each(["ask.answer", "ask.chat", "ask.allow", "ask.deny"])(
    "%s is on with a session on screen, and off with none or while the snapshot is stale",
    (id) => {
      const session = makeSession();
      expect(commandEnabled(command(id), context(session))).toBe(true);
      expect(commandEnabled(command(id), context(undefined))).toBe(false);
      expect(commandEnabled(command(id), context(session, { stale: true, link: "offline" }))).toBe(
        false,
      );
    },
  );
});

describe("ask.answer", () => {
  const body: AnswerBody = { answers: { "Which?": { labels: ["a"] } } };

  it("sends the body to the ask and drops that ask's draft, and no other", async () => {
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());
    useDrafts.getState().setAsk("ask-1", drafted(picked(["a"]), picked(["a"])));
    useDrafts.getState().setAsk("ask-2", drafted(picked(["b"]), picked(["b"])));

    await runCommand(command("ask.answer"), context(makeSession()), { ask: "ask-1", body });

    expect(answer).toHaveBeenCalledOnce();
    expect(answer).toHaveBeenCalledWith(1, "ask-1", body);
    expect(Object.keys(useDrafts.getState().asks)).toEqual(["ask-2"]);
  });

  it("keeps the draft on a 202 and says the outcome will follow", async () => {
    vi.spyOn(api, "answer").mockResolvedValue({ kind: "pending" });
    useDrafts.getState().setAsk("ask-1", drafted(picked(["a"]), picked(["a"])));

    await runCommand(command("ask.answer"), context(makeSession()), { ask: "ask-1", body });

    expect(Object.keys(useDrafts.getState().asks)).toEqual(["ask-1"]);
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "Answer is still running. Its outcome will follow.",
        type: "info",
      }),
    );
  });

  it("keeps the draft and toasts the model's text when the answer is refused", async () => {
    vi.spyOn(api, "answer").mockResolvedValue({
      kind: "refused",
      status: 409,
      message: "that question is already answered",
    });
    useDrafts.getState().setAsk("ask-1", drafted(picked(["a"]), picked(["a"])));

    await runCommand(command("ask.answer"), context(makeSession()), { ask: "ask-1", body });

    expect(Object.keys(useDrafts.getState().asks)).toEqual(["ask-1"]);
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "that question is already answered",
        type: "error",
      }),
    );
  });

  it("keys its in-flight mark by the ask: another ask runs beside it, the same ask runs once", async () => {
    const gate = deferred<ReturnType<typeof noticesOk>>();
    const answer = vi.spyOn(api, "answer").mockReturnValue(gate.promise);
    const ctx = context(makeSession());

    const first = runCommand(command("ask.answer"), ctx, { ask: "ask-1", body });
    const again = runCommand(command("ask.answer"), ctx, { ask: "ask-1", body });
    const other = runCommand(command("ask.answer"), ctx, { ask: "ask-2", body });
    gate.resolve(noticesOk());
    await Promise.all([first, again, other]);

    expect(answer.mock.calls.map((call) => call[1])).toEqual(["ask-1", "ask-2"]);
  });
});

describe("ask.chat", () => {
  it("asks for what to clarify, in the classic words, with the message's limit", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);

    await runCommand(command("ask.chat"), context(session), { ask: "ask-1" });

    expect(askedSheet()).toEqual({
      title: "Chat about this",
      body: "Claude puts the questions aside and asks what you want to clarify.",
      action: "Send",
      destructive: false,
      field: {
        kind: "text",
        label: "What to clarify",
        placeholder: "What to clarify (optional)",
        max: MAX_MESSAGE_BYTES,
        tooLong: "Too long: a message is at most 64 KiB.",
      },
    });
  });

  it("sends the trimmed text as a chat and drops the ask's draft", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("  what does a do?  ");
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());
    useDrafts.getState().setAsk("ask-1", drafted(picked(["a"]), picked(["a"])));

    await runCommand(command("ask.chat"), context(session), { ask: "ask-1" });

    expect(answer).toHaveBeenCalledWith(1, "ask-1", { chat: true, chat_text: "what does a do?" });
    expect(useDrafts.getState().asks).toEqual({});
  });

  it("sends an empty chat text when nothing was typed", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.chat"), context(session), { ask: "ask-1" });

    expect(answer).toHaveBeenCalledWith(1, "ask-1", { chat: true, chat_text: "" });
  });

  it("sends nothing, and keeps the draft, when the sheet is cancelled", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());
    useDrafts.getState().setAsk("ask-1", drafted(picked(["a"]), picked(["a"])));

    await runCommand(command("ask.chat"), context(session), { ask: "ask-1" });

    expect(answer).not.toHaveBeenCalled();
    expect(Object.keys(useDrafts.getState().asks)).toEqual(["ask-1"]);
  });

  it("keeps the draft when the daemon answers 202 or refuses", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("hm");
    useDrafts.getState().setAsk("ask-1", drafted(picked(["a"]), picked(["a"])));
    const answer = vi.spyOn(api, "answer").mockResolvedValue({ kind: "pending" });
    await runCommand(command("ask.chat"), context(session), { ask: "ask-1" });
    expect(Object.keys(useDrafts.getState().asks)).toEqual(["ask-1"]);

    answer.mockResolvedValue({ kind: "refused", status: 409, message: "already answered" });
    await runCommand(command("ask.chat"), context(session), { ask: "ask-1" });
    expect(Object.keys(useDrafts.getState().asks)).toEqual(["ask-1"]);
  });
});

describe("ask.allow", () => {
  it("allows with no sheet and no suggestion for the plain Allow", async () => {
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.allow"), context(makeSession()), allowArgs(permissionAsk, 0));

    expect(askConfirm).not.toHaveBeenCalled();
    expect(answer).toHaveBeenCalledOnce();
    expect(answer.mock.calls[0]?.[2]).toStrictEqual({ allow: true });
    expect(answer.mock.calls[0]?.[1]).toBe("perm-1");
  });

  it("allows a suggestion that does not persist with no sheet, naming it 1-based", async () => {
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.allow"), context(makeSession()), allowArgs(permissionAsk, 2));

    expect(askConfirm).not.toHaveBeenCalled();
    expect(answer).toHaveBeenCalledWith(1, "perm-1", { allow: true, suggestion: 2 });
  });

  it("asks 'Save this rule?' first for a suggestion that persists, and allows on a yes", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.allow"), context(session), allowArgs(permissionAsk, 1));

    expect(askedSheet()).toEqual({
      title: "Save this rule?",
      body: "Always allow ls.\n\nClaude writes it to a settings file, so it lasts past this session.",
      action: "Allow and save",
      destructive: false,
    });
    expect(answer).toHaveBeenCalledWith(1, "perm-1", { allow: true, suggestion: 1 });
  });

  it("allows nothing when the rule's sheet is cancelled", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.allow"), context(session), allowArgs(permissionAsk, 1));

    expect(answer).not.toHaveBeenCalled();
  });

  it("keys its in-flight mark by the ask", async () => {
    const gate = deferred<ReturnType<typeof noticesOk>>();
    const answer = vi.spyOn(api, "answer").mockReturnValue(gate.promise);
    const ctx = context(makeSession());
    const other: Ask = { ...permissionAsk, id: "perm-2" };

    const first = runCommand(command("ask.allow"), ctx, allowArgs(permissionAsk, 0));
    const again = runCommand(command("ask.allow"), ctx, allowArgs(permissionAsk, 0));
    const second = runCommand(command("ask.allow"), ctx, allowArgs(other, 0));
    gate.resolve(noticesOk());
    await Promise.all([first, again, second]);

    expect(answer.mock.calls.map((call) => call[1])).toEqual(["perm-1", "perm-2"]);
  });
});

describe("ask.deny", () => {
  it("asks what Claude should do instead, destructively, with the message's limit", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);

    await runCommand(command("ask.deny"), context(session), { ask: "perm-1" });

    expect(askedSheet()).toEqual({
      title: "Deny this?",
      body: 'Claude stops this turn, as "No" does in the terminal.',
      action: "Deny",
      destructive: true,
      field: {
        kind: "text",
        label: "What Claude should do instead",
        placeholder: "What Claude should do instead (optional)",
        max: MAX_MESSAGE_BYTES,
        tooLong: "Too long: a message is at most 64 KiB.",
      },
    });
  });

  it("denies with the trimmed message", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("  use ls instead  ");
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.deny"), context(session), { ask: "perm-1" });

    expect(answer).toHaveBeenCalledWith(1, "perm-1", { allow: false, message: "use ls instead" });
  });

  it("denies with an empty message when none was typed", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.deny"), context(session), { ask: "perm-1" });

    expect(answer).toHaveBeenCalledWith(1, "perm-1", { allow: false, message: "" });
  });

  it("denies nothing when the sheet is cancelled", async () => {
    const session = makeSession();
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const answer = vi.spyOn(api, "answer").mockResolvedValue(noticesOk());

    await runCommand(command("ask.deny"), context(session), { ask: "perm-1" });

    expect(answer).not.toHaveBeenCalled();
  });
});
