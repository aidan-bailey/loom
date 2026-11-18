import { describe, expect, it } from "vitest";
import type { Outcome } from "./gen";
import { OutcomeError, queryClient, retryRead, unwrap } from "./queries";

const refused: Outcome<unknown> = { kind: "refused", status: 409, message: "session is busy" };
const failed: Outcome<unknown> = { kind: "failed", status: 500, message: "boom" };

describe("unwrap", () => {
  it("returns an ok outcome's value", () => {
    const ok: Outcome<{ id: number }> = { kind: "ok", status: 200, value: { id: 3 }, notices: [] };
    expect(unwrap(ok)).toEqual({ id: 3 });
  });

  it.each<[Outcome<unknown>, string]>([
    [{ kind: "pending" }, "still running"],
    [{ kind: "unpaired" }, "not paired"],
    [refused, "session is busy"],
    [{ kind: "stopping", message: "loom is stopping" }, "loom is stopping"],
    [{ kind: "timeout", message: "timed out" }, "timed out"],
    [failed, "boom"],
  ])("throws an OutcomeError carrying the %j outcome", (outcome, message) => {
    let caught: unknown;
    try {
      unwrap(outcome);
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeInstanceOf(OutcomeError);
    expect((caught as OutcomeError).outcome).toBe(outcome);
    expect((caught as OutcomeError).message).toBe(message);
  });

  it("makes an OutcomeError an Error named OutcomeError", () => {
    const err = new OutcomeError(refused);
    expect(err).toBeInstanceOf(Error);
    expect(err.name).toBe("OutcomeError");
  });
});

describe("retryRead", () => {
  it.each<Outcome<unknown>>([
    failed,
    { kind: "timeout", message: "timed out" },
    { kind: "stopping", message: "loom is stopping" },
  ])("retries a %j read", (outcome) => {
    expect(retryRead(0, new OutcomeError(outcome))).toBe(true);
    expect(retryRead(1, new OutcomeError(outcome))).toBe(true);
  });

  it.each<Outcome<unknown>>([
    refused,
    { kind: "unpaired" },
    { kind: "pending" },
    { kind: "stopping", message: "push is not available" },
  ])("never retries a %j read", (outcome) => {
    expect(retryRead(0, new OutcomeError(outcome))).toBe(false);
  });

  it("stops after two retries", () => {
    expect(retryRead(2, new OutcomeError(failed))).toBe(false);
  });

  it("does not retry an error that is not an outcome", () => {
    expect(retryRead(0, new TypeError("a bug"))).toBe(false);
  });
});

describe("queryClient", () => {
  it("retries reads by retryRead and does not refetch on window focus", () => {
    const options = queryClient.getDefaultOptions().queries;
    expect(options?.retry).toBe(retryRead);
    expect(options?.refetchOnWindowFocus).toBe(false);
  });
});
