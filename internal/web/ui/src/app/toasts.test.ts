import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Notice, Outcome } from "@/api/gen";
import { onNotices } from "@/api/request";
import { toastManager } from "@/components/ui/toast";

import { showMessage, showNotices, showOutcome, watchNotices } from "./toasts";

vi.mock("@/api/request", () => ({ onNotices: vi.fn() }));

function spyOnAdd() {
  return vi.spyOn(toastManager, "add").mockImplementation(() => "toast");
}

let add: ReturnType<typeof spyOnAdd>;

beforeEach(() => {
  add = spyOnAdd();
});

afterEach(() => {
  add.mockRestore();
});

function notice(text: string, error = false): Notice {
  return { text, error, at: "2026-11-18T12:00:00Z" };
}

describe("showNotices", () => {
  it("shows every notice, each until dismissed", () => {
    showNotices([
      notice("Stashed your changes as 1a2b3c4 on loom/x."),
      notice("Resumed from stash 9f8e7d6."),
      notice("The worktree is locked.", true),
    ]);

    expect(add).toHaveBeenCalledTimes(3);
    expect(add).toHaveBeenNthCalledWith(
      1,
      expect.objectContaining({
        description: "Stashed your changes as 1a2b3c4 on loom/x.",
        timeout: 0,
        type: "info",
      }),
    );
    expect(add).toHaveBeenNthCalledWith(
      2,
      expect.objectContaining({ description: "Resumed from stash 9f8e7d6.", timeout: 0 }),
    );
    expect(add).toHaveBeenNthCalledWith(
      3,
      expect.objectContaining({
        description: "The worktree is locked.",
        timeout: 0,
        type: "error",
        priority: "high",
      }),
    );
  });

  it("shows nothing for no notices", () => {
    showNotices([]);
    expect(add).not.toHaveBeenCalled();
  });
});

describe("showMessage", () => {
  it("fades a plain message", () => {
    showMessage("Copied.");
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "Copied.", type: "info", timeout: 4000 }),
    );
  });

  it("keeps an error until it is dismissed", () => {
    showMessage("That didn't work.", true);
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "That didn't work.",
        type: "error",
        timeout: 0,
        priority: "high",
      }),
    );
  });
});

describe("showOutcome", () => {
  it.each<[string, Outcome<unknown>, string | null, boolean]>([
    ["ok", { kind: "ok", status: 200, value: {}, notices: [notice("seen already")] }, null, false],
    ["unpaired", { kind: "unpaired" }, null, false],
    ["pending", { kind: "pending" }, "Merge is still running. Its outcome will follow.", false],
    [
      "refused",
      { kind: "refused", status: 409, message: "session is not paused" },
      "session is not paused",
      true,
    ],
    [
      "refused with no text",
      { kind: "refused", status: 400, message: "" },
      "Merge didn't work.",
      true,
    ],
    ["stopping", { kind: "stopping", message: "loom is stopping" }, "loom is stopping", true],
    ["timeout", { kind: "timeout", message: "" }, "Merge timed out.", true],
    ["failed", { kind: "failed", status: 500, message: "internal error" }, "internal error", true],
    [
      "failed with no answer",
      { kind: "failed", status: 0, message: "" },
      "Can't reach loom. Try again when it is back.",
      true,
    ],
  ])("%s", (_name, outcome, want, error) => {
    showOutcome(outcome, "Merge");

    if (want === null) {
      // An ok outcome's notices already reached onNotices; showing them here
      // would toast each one twice.
      expect(add).not.toHaveBeenCalled();
      return;
    }
    expect(add).toHaveBeenCalledTimes(1);
    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: want, type: error ? "error" : "info" }),
    );
  });
});

describe("watchNotices", () => {
  it("shows what onNotices delivers, and returns its unsubscribe", () => {
    const unsubscribe = vi.fn();
    vi.mocked(onNotices).mockImplementation((listener) => {
      listener([notice("from the stream")]);
      return unsubscribe;
    });

    const stop = watchNotices();

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "from the stream", timeout: 0 }),
    );
    expect(stop).toBe(unsubscribe);
  });
});
