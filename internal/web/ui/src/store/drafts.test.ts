import { afterEach, describe, expect, it } from "vitest";

import { useDrafts, type AskDraft } from "./drafts";

afterEach(() => {
  useDrafts.getState().clear();
});

const state = () => useDrafts.getState();

const draft = (labels: string[]): AskDraft => ({
  answers: { "Which?": { labels, other: "", note: "" } },
});

describe("the reply drafts", () => {
  it("keeps a reply per session", () => {
    state().setReply(1, "first");
    state().setReply(2, "second");
    expect(state().replies).toEqual({ 1: "first", 2: "second" });
  });

  it("replaces a session's reply and leaves the others", () => {
    state().setReply(1, "first");
    state().setReply(2, "second");
    state().setReply(1, "first, longer");
    expect(state().replies).toEqual({ 1: "first, longer", 2: "second" });
  });

  it("deletes a session's entry when it is set to the empty string, and no other", () => {
    state().setReply(1, "first");
    state().setReply(2, "second");
    state().setReply(1, "");
    expect(state().replies).toStrictEqual({ 2: "second" });
    expect(1 in state().replies).toBe(false);
  });

  it("keeps whitespace as typed, since only the empty string deletes", () => {
    state().setReply(1, "  ");
    expect(state().replies).toEqual({ 1: "  " });
  });

  it("changes nothing when an empty reply is set for a session that has none", () => {
    state().setReply(2, "second");
    const before = state().replies;
    state().setReply(1, "");
    expect(state().replies).toBe(before);
  });
});

describe("the ask drafts", () => {
  it("keeps a draft per ask and replaces a whole draft by setting it again", () => {
    state().setAsk("a", draft(["x"]));
    state().setAsk("b", draft(["y"]));
    state().setAsk("a", draft(["z"]));
    expect(state().asks).toEqual({ a: draft(["z"]), b: draft(["y"]) });
  });

  it("drops one ask's draft and keeps the others", () => {
    state().setAsk("a", draft(["x"]));
    state().setAsk("b", draft(["y"]));
    state().dropAsk("a");
    expect(state().asks).toStrictEqual({ b: draft(["y"]) });
  });

  it("drops an ask it holds no draft for without a word", () => {
    state().setAsk("a", draft(["x"]));
    state().dropAsk("nothing");
    expect(state().asks).toEqual({ a: draft(["x"]) });
  });
});

describe("clear", () => {
  it("forgets every reply and every answer", () => {
    state().setReply(1, "first");
    state().setAsk("a", draft(["x"]));
    state().clear();
    expect(state().replies).toEqual({});
    expect(state().asks).toEqual({});
  });
});
