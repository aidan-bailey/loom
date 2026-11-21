import { describe, expect, it } from "vitest";

import * as pairPanel from "./Pair";
import { CODE_LENGTH, normalizeCode, PAIRING_ALPHABET } from "./code";

describe("panels/pair/code", () => {
  it("code: Pair.tsx re-exports the same values, so its importers are unchanged", () => {
    expect(pairPanel.CODE_LENGTH).toBe(CODE_LENGTH);
    expect(pairPanel.normalizeCode).toBe(normalizeCode);
    expect(pairPanel.PAIRING_ALPHABET).toBe(PAIRING_ALPHABET);
  });

  it("code: a code is 8 characters of the pairing alphabet, normalised as typed or pasted", () => {
    expect(CODE_LENGTH).toBe(8);
    expect(normalizeCode("k7qm-2xpa9")).toBe("K7QM2XPA");
    expect(normalizeCode("O0I1L")).toBe("");
  });
});
