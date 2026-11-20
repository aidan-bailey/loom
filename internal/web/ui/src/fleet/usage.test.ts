import { afterEach, describe, expect, it, vi } from "vitest";
import type { Session, Usage, Window } from "@/api/gen";
import { fixtureSnapshot } from "@/fixtures";
import { allSessions } from "./groups";
import {
  isStale,
  resetPassed,
  resetText,
  rowShare,
  shareText,
  shownWindow,
  usageFor,
} from "./usage";

const NOW = Date.parse("2026-11-18T12:00:00Z");
const HOUR = 3_600_000;
const iso = (ms: number) => new Date(ms).toISOString();

const base = allSessions(fixtureSnapshot(NOW)).find((s) => s.id === 1) as Session;
const claude = (account = ""): Session => ({ ...base, claude: true, account });

const win = (pct: number, resetsAt = iso(NOW + HOUR)): Window => ({ pct, resets_at: resetsAt });
const entry = (account: string, over: Partial<Usage> = {}): Usage => ({
  account,
  plan: "max",
  five_hour: win(62.4),
  seven_day: null,
  at: iso(NOW - 60_000),
  ...over,
});

afterEach(() => {
  vi.useRealTimers();
});

describe("rowShare", () => {
  it("shows the account's 5-hour share as a rounded percentage", () => {
    expect(rowShare(claude(), [entry("default")], 900, NOW)).toEqual({
      text: "62%",
      amber: false,
    });
  });

  it("reads a named account's entry", () => {
    const usage = [entry("default"), entry("work", { five_hour: win(18) })];
    expect(rowShare(claude("work"), usage, 900, NOW)?.text).toBe("18%");
  });

  it("has no share for a session that is not Claude", () => {
    expect(rowShare({ ...claude(), claude: false }, [entry("default")], 900, NOW)).toBeNull();
  });

  it("falls back to the 7-day window when the account has no 5-hour one", () => {
    const usage = [entry("default", { five_hour: null, seven_day: win(31) })];
    expect(rowShare(claude(), usage, 900, NOW)?.text).toBe("31%");
  });

  it("has no share without a usage entry", () => {
    expect(rowShare(claude("work"), [entry("default")], 900, NOW)).toBeNull();
  });

  it("has no share when the entry has neither window", () => {
    const usage = [entry("default", { five_hour: null, seven_day: null })];
    expect(rowShare(claude(), usage, 900, NOW)).toBeNull();
  });

  it("turns amber from 80 percent of the raw value", () => {
    expect(rowShare(claude(), [entry("default", { five_hour: win(80) })], 900, NOW)).toEqual({
      text: "80%",
      amber: true,
    });
    expect(rowShare(claude(), [entry("default", { five_hour: win(79.9) })], 900, NOW)).toEqual({
      text: "80%",
      amber: false,
    });
  });

  it("says reset, and is not amber, once the window has reset", () => {
    const reset = [entry("default", { five_hour: win(90, iso(NOW - HOUR)) })];
    expect(rowShare(claude(), reset, 900, NOW)).toEqual({ text: "reset", amber: false });
    const open = [entry("default", { five_hour: win(90, iso(NOW + HOUR)) })];
    expect(rowShare(claude(), open, 900, NOW)).toEqual({ text: "90%", amber: true });
  });

  it("is not amber for a stale sample, and only when a bound is given", () => {
    const usage = [entry("default", { five_hour: win(90), at: iso(NOW - 900_000) })];
    expect(rowShare(claude(), usage, 900, NOW)?.amber).toBe(false);
    expect(rowShare(claude(), usage, 901, NOW)?.amber).toBe(true);
    expect(rowShare(claude(), usage, undefined, NOW)?.amber).toBe(true);
  });

  it("counts from the clock when no time is given", () => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
    const usage = [entry("default", { five_hour: win(50, iso(NOW - 60_000)) })];
    expect(rowShare(claude(), usage)?.text).toBe("reset");
  });
});

describe("rowShare after the widening", () => {
  it("takes its text from shareText", () => {
    expect(rowShare(claude(), [entry("default", { five_hour: win(62.5) })], 900, NOW)?.text).toBe(
      "63%",
    );
  });
});

describe("usageFor", () => {
  it('finds an account\'s entry and reads "" as the default account', () => {
    const list = [entry("default"), entry("work")];
    expect(usageFor(list, "work")?.account).toBe("work");
    expect(usageFor(list, "")?.account).toBe("default");
    expect(usageFor(list, "other")).toBeUndefined();
  });
});

describe("shownWindow", () => {
  it("names the 5-hour window and the 7-day one beside it", () => {
    const e = entry("default", { five_hour: win(62.4), seven_day: win(31) });
    expect(shownWindow(e)).toEqual({
      w: e.five_hour,
      label: "5h",
      other: e.seven_day,
      otherLabel: "7d",
    });
  });

  it("falls back to the 7-day window alone", () => {
    const e = entry("default", { five_hour: null, seven_day: win(31) });
    expect(shownWindow(e)).toEqual({ w: e.seven_day, label: "7d", other: null, otherLabel: "" });
  });

  it("is null for an entry with neither", () => {
    expect(shownWindow(entry("default", { five_hour: null, seven_day: null }))).toBeNull();
  });
});

describe("resetPassed and isStale", () => {
  it("resetPassed is true at the reset time and after, false before and for a time it cannot read", () => {
    expect(resetPassed(win(10, iso(NOW)), NOW)).toBe(true);
    expect(resetPassed(win(10, iso(NOW - 1)), NOW)).toBe(true);
    expect(resetPassed(win(10, iso(NOW + 1000)), NOW)).toBe(false);
    expect(resetPassed(win(10, ""), NOW)).toBe(false);
  });

  it("isStale needs a bound and a sample at least that old", () => {
    expect(isStale(iso(NOW - 900_000), 900, NOW)).toBe(true);
    expect(isStale(iso(NOW - 899_000), 900, NOW)).toBe(false);
    expect(isStale(iso(NOW - 900_000), undefined, NOW)).toBe(false);
    expect(isStale(iso(NOW - 900_000), 0, NOW)).toBe(false);
  });
});

describe("shareText", () => {
  it("is the rounded percentage, or reset once the window has reset", () => {
    expect(shareText(win(62.4), NOW)).toBe("62%");
    expect(shareText(win(62.5), NOW)).toBe("63%");
    expect(shareText(win(90, iso(NOW - HOUR)), NOW)).toBe("reset");
  });
});

describe("resetText", () => {
  it("words under a day as hours and minutes, or minutes alone", () => {
    expect(resetText(iso(NOW + 100 * 60_000), NOW)).toBe("1h40m");
    expect(resetText(iso(NOW + 125 * 60_000), NOW)).toBe("2h05m");
    expect(resetText(iso(NOW + 35 * 60_000), NOW)).toBe("35m");
    expect(resetText(iso(NOW + 30_000), NOW)).toBe("1m");
    expect(resetText(iso(NOW), NOW)).toBe("0m");
    expect(resetText(iso(NOW - 5 * 60_000), NOW)).toBe("0m");
  });

  it("names the weekday from a day out, and is empty for no time", () => {
    for (const t of [NOW + 24 * HOUR, NOW + 70 * HOUR]) {
      const text = resetText(iso(t), NOW);
      expect(text).toBe(new Date(t).toLocaleDateString([], { weekday: "short" }));
      expect(text).not.toMatch(/\d/);
    }
    expect(resetText("", NOW)).toBe("");
    expect(resetText("later", NOW)).toBe("");
  });
});
