import type { ReactElement } from "react";

// BROWSER_KEY is the localStorage key of "continue in the browser anyway". It is the classic app's
// own key (internal/web/static/js/store.mjs's pref("browser")), so a choice made in one app holds
// in the other.
export const BROWSER_KEY = "loom.browser";

// chosenBrowser reads the stored choice as JSON; an unset, unparsable or unreadable value is no
// choice.
function chosenBrowser(): boolean {
  try {
    const text = localStorage.getItem(BROWSER_KEY);
    return text !== null && JSON.parse(text) === true;
  } catch {
    return false;
  }
}

function standalone(): boolean {
  if ((navigator as Navigator & { standalone?: boolean }).standalone === true) return true;
  if (typeof window.matchMedia !== "function") return false;
  return window.matchMedia("(display-mode: standalone)").matches === true;
}

// needsInstall is whether this launch is an iOS browser tab that has not chosen to stay there. The
// installed app keeps its own cookies, separate from Safari's, so pairing in a Safari tab would
// pair the wrong place: such a tab is asked to add Loom to the Home Screen first. iPadOS says
// Macintosh, so a Mac with touch points is an iPad. It never throws.
export function needsInstall(): boolean {
  try {
    const ios =
      /iPhone|iPad|iPod/.test(navigator.userAgent) ||
      (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
    return ios && !standalone() && !chosenBrowser();
  } catch {
    return false;
  }
}

// AddToHome is the page an iOS browser tab shows instead of the app, drawn outside the shell. Continue
// stores the choice and then goes on; a storage that refuses (private mode, a full quota) still
// continues, the choice just does not outlive the page.
export function AddToHome({ onContinue }: { onContinue: () => void }): ReactElement {
  const choose = (): void => {
    try {
      localStorage.setItem(BROWSER_KEY, "true");
    } catch {
      // The choice then lasts only until the page closes.
    }
    onContinue();
  };

  return (
    <div className="mx-auto flex min-h-dvh w-full max-w-[40rem] flex-col gap-5 bg-background px-4 pt-[calc(env(safe-area-inset-top)+2rem)] pr-[max(1rem,env(safe-area-inset-right))] pb-[calc(env(safe-area-inset-bottom)+2rem)] pl-[max(1rem,env(safe-area-inset-left))] text-foreground">
      <h1 className="text-2xl font-semibold">Loom</h1>
      <p className="text-muted-foreground">
        Add Loom to your Home Screen to use it. The installed app keeps its own pairing, separate
        from this tab.
      </p>
      <ol className="flex list-decimal flex-col gap-2 pl-5 text-muted-foreground">
        <li>Tap the Share button in Safari&apos;s toolbar.</li>
        <li>
          Scroll down and tap <strong className="text-foreground">Add to Home Screen</strong>.
        </li>
        <li>
          Tap <strong className="text-foreground">Add</strong>, then open Loom from your Home
          Screen.
        </li>
      </ol>
      <p className="text-muted-foreground">The app then asks for a pairing code.</p>
      <button
        type="button"
        onClick={choose}
        className="min-h-[44px] self-start text-[16px] text-muted-foreground underline"
      >
        Continue in the browser anyway
      </button>
    </div>
  );
}
