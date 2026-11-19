import { describe, expect, it } from "vitest";
import { AGENT_ID } from "@/app/routes";

describe("AGENT_ID", () => {
  it("AGENT_ID: admits the ids the daemon writes and refuses the rest", () => {
    for (const ok of ["a1f00dcafe0001", "agent_1", "a-b"]) expect(AGENT_ID.test(ok), ok).toBe(true);
    for (const bad of ["", "a b", "a/b", "../x", "a?b=1"]) {
      expect(AGENT_ID.test(bad), bad).toBe(false);
    }
  });
});
