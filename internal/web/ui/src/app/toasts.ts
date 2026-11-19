import type { Notice, Outcome } from "@/api/gen";
import { onNotices } from "@/api/request";
import { toastManager } from "@/components/ui/toast";

// FADE_MS is how long a plain message stays. A notice, and an error, stay
// until dismissed: a stash notice names a SHA that may be the only record of
// the work.
const FADE_MS = 4000;

function add(text: string, error: boolean, sticky: boolean): void {
  toastManager.add({
    description: text,
    type: error ? "error" : "info",
    timeout: sticky ? 0 : FADE_MS,
    priority: error ? "high" : "low",
  });
}

// showNotices toasts every notice, each until dismissed (Kill's and Resume's
// stash notices are never dropped).
export function showNotices(notices: Notice[]): void {
  for (const n of notices) add(n.text, n.error, true);
}

// showMessage toasts a plain message: it fades, unless it is an error.
export function showMessage(text: string, error = false): void {
  add(text, error, error);
}

// showOutcome toasts what an outcome that is not a success says, in the
// classic app's words; `what` names the action. An ok outcome shows nothing:
// its notices already went through onNotices. An unpaired one shows nothing
// either: the app is on its way to pairing.
export function showOutcome(outcome: Outcome<unknown>, what: string): void {
  switch (outcome.kind) {
    case "ok":
    case "unpaired":
      return;
    case "pending":
      showMessage(`${what} is still running. Its outcome will follow.`);
      return;
    case "refused":
      showMessage(outcome.message || `${what} didn't work.`, true);
      return;
    case "stopping":
      showMessage(outcome.message || "loom is restarting. Try again in a moment.", true);
      return;
    case "timeout":
      showMessage(outcome.message || `${what} timed out.`, true);
      return;
    case "failed":
      showMessage(outcome.message || "Can't reach loom. Try again when it is back.", true);
      return;
  }
}

// watchNotices toasts every notice onNotices delivers (a response's, and the
// stream's) until the returned stop is called. main.tsx calls it once.
export function watchNotices(): () => void {
  return onNotices(showNotices);
}
