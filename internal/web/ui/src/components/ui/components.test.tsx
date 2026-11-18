import { describe, expect, it } from "vitest";
import { cn } from "@/lib/utils";

describe("cn", () => {
  it("cn joins its inputs, drops falsy ones and lets the last conflicting utility win", () => {
    expect(cn("a", false, ["b", undefined], "c")).toBe("a b c");
    expect(cn("p-2", "p-4")).toBe("p-4");
    expect(cn("text-faint", "text-muted-foreground")).toBe("text-muted-foreground");
  });
});
