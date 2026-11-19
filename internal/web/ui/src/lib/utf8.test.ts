import { describe, expect, it } from "vitest";

import { MAX_MESSAGE_BYTES, utf8Length } from "./utf8";

describe("utf8Length", () => {
  it("counts bytes, not UTF-16 units", () => {
    expect(utf8Length("")).toBe(0);
    expect(utf8Length("abc")).toBe(3);
    expect(utf8Length("é")).toBe(2);
    expect(utf8Length("€")).toBe(3);
    expect(utf8Length("😀")).toBe(4);
  });

  it("counts a lone surrogate as the three bytes of U+FFFD, as the daemon receives it", () => {
    expect(utf8Length("\ud83d")).toBe(3);
  });
});

describe("MAX_MESSAGE_BYTES", () => {
  it("is the 64 KiB the daemon holds a reply, an answer's text and a message to", () => {
    expect(MAX_MESSAGE_BYTES).toBe(65536);
  });
});
