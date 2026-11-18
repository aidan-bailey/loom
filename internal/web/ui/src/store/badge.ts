// The Home Screen badge: the count of sessions that need you, which the stream carries.

interface BadgingNavigator {
  setAppBadge?: (count: number) => Promise<void>;
  clearAppBadge?: () => Promise<void>;
}

/**
 * Puts the count on the Home Screen icon, and clears it at 0, so answering every session clears
 * what a push set. A browser without the Badging API, or one that refuses, is left alone.
 */
export function setBadge(count: number): void {
  try {
    const nav = navigator as Navigator & BadgingNavigator;
    const shown = count > 0 ? nav.setAppBadge?.(count) : nav.clearAppBadge?.();
    shown?.catch(() => undefined);
  } catch {
    // no Badging API, or it threw: the icon keeps what it had
  }
}
