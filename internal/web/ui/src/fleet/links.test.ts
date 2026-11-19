import { describe, expect, it } from "vitest";
import type { Ask, Session } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "./groups";
import { opensMockup, sessionHref } from "./links";

const NOW = Date.parse("2026-11-18T12:00:00Z");
const sessions = allSessions(fixtureSnapshot(NOW));
const byId = (id: number): Session => sessions.find((s) => s.id === id) as Session;
const ask = { id: "q1", kind: "question", agent: "", opened: "", answering: false } as Ask;

describe("opensMockup", () => {
  it("opens the mockup for a fresh one with no open ask", () => {
    expect(opensMockup(byId(7))).toBe(true);
  });

  it("does not open the mockup while a dialog waits", () => {
    expect(opensMockup({ ...byId(7), asks: [ask] })).toBe(false);
  });

  it("does not open a mockup that did not change in the latest turn", () => {
    const s = byId(7);
    expect(opensMockup({ ...s, mockup: { ...s.mockup!, fresh: false } })).toBe(false);
  });

  it("does not open a mockup that is not there", () => {
    expect(opensMockup(byId(1))).toBe(false);
  });

  it("reads a null asks list as none", () => {
    expect(opensMockup({ ...byId(7), asks: null })).toBe(true);
  });
});

describe("sessionHref", () => {
  it("links a session to its conversation", () => {
    expect(sessionHref(byId(1))).toBe("#/s/1");
  });

  it("links a session with a fresh mockup to the mockup", () => {
    expect(sessionHref(byId(7))).toBe("#/s/7/mockup");
  });

  it("links to the conversation while a dialog waits, though the mockup is fresh", () => {
    expect(sessionHref({ ...byId(7), asks: [ask] })).toBe("#/s/7");
  });
});
