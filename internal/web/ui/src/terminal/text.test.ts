import { describe, expect, it } from "vitest";
import { plainText, type BufferLine } from "./text";

function row(text: string, wrapped = false): BufferLine {
  return { text, wrapped };
}

describe("plainText", () => {
  it('gives "" for no rows and for rows with nothing in them', () => {
    expect(plainText([])).toBe("");
    expect(plainText([row("   "), row(""), row("  ")])).toBe("");
  });

  it("cuts the trailing blanks of each line and keeps the leading ones", () => {
    expect(plainText([row("  indent   "), row("b  ")])).toBe("  indent\nb");
  });

  it("joins a wrapped row to the row before it", () => {
    expect(plainText([row("echo hello w"), row("orld   ", true)])).toBe("echo hello world");
  });

  it("joins a chain of wrapped rows and cuts only the end of the last", () => {
    expect(plainText([row("aa "), row("bb ", true), row("cc  ", true), row("next  ")])).toBe(
      "aa bb cc\nnext",
    );
  });

  it("keeps the blank a wrapped row ends in when the next row continues it", () => {
    expect(plainText([row("ls -la "), row("/tmp   ", true)])).toBe("ls -la /tmp");
  });

  it("cuts a blank row that continues a line along with the line's own trailing blanks", () => {
    expect(plainText([row("abc  "), row("     ", true), row("next")])).toBe("abc\nnext");
  });

  it("drops the empty lines at the end and keeps the ones before text", () => {
    expect(plainText([row(""), row("a"), row(""), row("b"), row("   "), row("")])).toBe("\na\n\nb");
  });

  it("starts a line at a first row that says it is wrapped", () => {
    expect(plainText([row("x", true), row("y")])).toBe("x\ny");
  });

  it("reads any iterable once", () => {
    let started = 0;
    function* gen(): Generator<BufferLine> {
      started++;
      yield row("a");
      yield row("b ", true);
      yield row("c");
    }
    expect(plainText(gen())).toBe("ab\nc");
    expect(started).toBe(1);
  });

  it("leaves a tab, which is not a blank cell, where it is", () => {
    expect(plainText([row("a\t")])).toBe("a\t");
  });
});
