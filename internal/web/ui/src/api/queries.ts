// TanStack Query's client (spec §26) and the helpers that turn an Outcome into a query's result.
import { QueryClient } from "@tanstack/react-query";
import type { Outcome } from "./gen";
import { isStoppingText } from "./request";

function reasonOf(outcome: Outcome<unknown>): string {
  switch (outcome.kind) {
    case "ok":
      return "ok";
    case "pending":
      return "still running";
    case "unpaired":
      return "not paired";
    default:
      return outcome.message;
  }
}

/** What unwrap throws for an outcome that is not ok; the outcome stays on it. */
export class OutcomeError extends Error {
  readonly outcome: Outcome<unknown>;

  constructor(outcome: Outcome<unknown>) {
    super(reasonOf(outcome));
    this.name = "OutcomeError";
    this.outcome = outcome;
  }
}

/** The value of an ok outcome; any other outcome throws an OutcomeError, which a query reports. */
export function unwrap<T>(outcome: Outcome<T>): T {
  if (outcome.kind === "ok") return outcome.value;
  throw new OutcomeError(outcome);
}

function retryable(outcome: Outcome<unknown>): boolean {
  switch (outcome.kind) {
    case "failed":
    case "timeout":
      return true;
    case "stopping":
      // Only a daemon that says it is stopping will answer later; another 503 (the push key's
      // "push is not available") would answer the same again.
      return isStoppingText(outcome.message);
    default:
      return false;
  }
}

/**
 * A read is retried, at most twice, only when loom could not answer it (a network failure, a
 * timeout, a stopping daemon): a refusal, an unpaired device or a bug would answer the same again.
 */
export function retryRead(failureCount: number, error: Error): boolean {
  return failureCount < 2 && error instanceof OutcomeError && retryable(error.outcome);
}

export const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: retryRead, refetchOnWindowFocus: false } },
});
