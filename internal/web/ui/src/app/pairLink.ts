// watchPairLink (spec §26): once the app starts, pairs with the code app/pairHold.ts took out of
// the address, on the first unpaired signal. It is apart from pairHold.ts because it runs a command
// and so reaches the router, which must not be evaluated before pairHold.ts has run.
import { onUnpaired } from "@/api/request";
import { commandById, runCommand } from "@/app/commands";
import { useFleet } from "@/store/fleet";

import { dropHeldCode, heldCode } from "./pairHold";

// watchPairLink pairs with the held code on the first unpaired signal (the daemon's 401), once. A
// live snapshot first means this browser is paired already, so the code is dropped unused; a stale
// one (a cold start's saved snapshot) is not that. It returns its stop.
export function watchPairLink(): () => void {
  if (heldCode() === undefined) return () => undefined;
  const live = (): boolean => {
    const { snapshot, stale } = useFleet.getState();
    return snapshot !== null && !stale;
  };
  if (live()) {
    dropHeldCode();
    return () => undefined;
  }
  let offFleet = (): void => undefined;
  let offUnpaired = (): void => undefined;
  const stop = (): void => {
    offFleet();
    offUnpaired();
  };
  offFleet = useFleet.subscribe(() => {
    if (!live()) return;
    dropHeldCode();
    stop();
  });
  offUnpaired = onUnpaired(() => {
    const code = heldCode();
    dropHeldCode();
    stop();
    if (code === undefined) return;
    const command = commandById("device.pair");
    if (command === undefined) throw new Error("loom: unknown command device.pair");
    void runCommand(
      command,
      { session: undefined, link: useFleet.getState().link, stale: true, panel: "pair" },
      { code },
    );
  });
  return stop;
}
