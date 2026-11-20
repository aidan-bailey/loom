import { describe, expect, it } from "vitest";

import { CODE_LENGTH, normalizeCode, PAIRING_ALPHABET } from "./Pair";

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
