import { describe, expect, it } from "vitest";

import { compareCodePoints, fuzzyScore } from "./palette";

describe("fuzzyScore", () => {
  it('scores "pa" against "Pause" as 7', () => {
    expect(fuzzyScore("pa", "Pause")).toBe(7);
  });

  it('scores "pa" against "update panel" as 7, from the alignment at "panel"', () => {
    expect(fuzzyScore("pa", "update panel")).toBe(7);
  });

  it('scores "ps" against "Pause" as 5', () => {
    expect(fuzzyScore("ps", "Pause")).toBe(5);
  });

  it('scores "sp" against "session.push" as 8', () => {
    expect(fuzzyScore("sp", "session.push")).toBe(8);
  });

  it("gives null when the query's code points do not all match in order", () => {
    expect(fuzzyScore("xyz", "Pause")).toBeNull();
    expect(fuzzyScore("ba", "ab")).toBeNull();
  });

  it("takes the best start, not the first or the last", () => {
    expect(fuzzyScore("ab", "a ab")).toBe(7);
    expect(fuzzyScore("ab", "axbab")).toBe(5);
  });

  it("scores an empty or blank query as 0, and trims the query", () => {
    expect(fuzzyScore("", "Pause")).toBe(0);
    expect(fuzzyScore("   ", "Pause")).toBe(0);
    expect(fuzzyScore("  pa ", "Pause")).toBe(7);
    expect(fuzzyScore("", "")).toBe(0);
  });

  it("lowercases both sides", () => {
    expect(fuzzyScore("PA", "pause")).toBe(7);
    expect(fuzzyScore("pa", "PAUSE")).toBe(7);
  });

  it("starts a word at the text's start or after a code point that is neither a letter nor a digit", () => {
    expect(fuzzyScore("b", "b")).toBe(4);
    expect(fuzzyScore("b", " b")).toBe(4);
    expect(fuzzyScore("b", "-b")).toBe(4);
    expect(fuzzyScore("b", "ab")).toBe(1);
    expect(fuzzyScore("b", "1b")).toBe(1);
  });

  it("walks code points, not UTF-16 units", () => {
    expect(fuzzyScore("\u{1F600}", "a\u{1F600}")).toBe(1);
    // 4 for the first code point at the text's start, then 1 + 2 + 3 for the b: it follows the
    // emoji, which is neither a letter nor a digit, so it also starts a word.
    expect(fuzzyScore("\u{1F600}b", "\u{1F600}b")).toBe(10);
  });
});

describe("compareCodePoints", () => {
  it("orders by code point", () => {
    expect(compareCodePoints("a", "b")).toBeLessThan(0);
    expect(compareCodePoints("b", "a")).toBeGreaterThan(0);
    expect(compareCodePoints("a", "a")).toBe(0);
    expect(compareCodePoints("ab", "abc")).toBeLessThan(0);
    expect(compareCodePoints("Z", "a")).toBeLessThan(0);
  });

  it("puts an astral code point after a BMP one, where < on the strings does the opposite", () => {
    expect(compareCodePoints("\u{FF61}", "\u{1F600}")).toBeLessThan(0);
    expect(compareCodePoints("\u{1F600}", "\u{FF61}")).toBeGreaterThan(0);
    expect("\u{FF61}" < "\u{1F600}").toBe(false);
  });
});
