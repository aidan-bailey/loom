// The leaf that takes the pairing link (spec §26) out of the address: `loom web open` opens
// <local>/?ui=next#/pair?code=<code> on the daemon's machine. main.tsx imports this module before
// any other of the app's, and its evaluation (the last two lines) runs adoptNextApp and then
// holdPairLink, because app/routes.tsx builds the router's hash history when it is imported and
// that history reads the address then: a module evaluated before this one that reaches the router
// would hand it #/pair?code=…. So this module imports leaves only (switchApp and panels/pair/code),
// never the commands, the pairing panel or the routes; watchPairLink, which needs the commands,
// is in app/pairLink.ts and reads the held code from here. The code stays in memory: a reload
// drops it and the pairing screen shows as usual.
import { CODE_LENGTH, normalizeCode } from "@/panels/pair/code";

import { hasAppCookie, setAppCookie } from "./switchApp";

// held is the normalised code taken from the address, until it is used or dropped.
let held: string | undefined;

// A hash of #/pair or #pair with a query: the pairing link's fragment.
const PAIR_HASH = /^#\/?pair\?(.*)$/;

// heldCode is the code holdPairLink took from the address, if it is still held.
export function heldCode(): string | undefined {
  return held;
}

// dropHeldCode forgets the code, used or not.
export function dropHeldCode(): void {
  held = undefined;
}

// adoptNextApp keeps a browser that arrived by ?ui=next with no app cookie on the new app: the
// daemon served it the new shell for that query alone, and the worker registers by the cookie
// (registerWorker), so without the cookie the next launch would be the classic app's. An explicit
// choice, "classic" included, is left as it is.
export function adoptNextApp(): void {
  if (new URLSearchParams(window.location.search).get("ui") !== "next") return;
  if (hasAppCookie()) return;
  setAppCookie("next");
}

// holdPairLink takes the code out of a pairing address in place (no history entry, the history's
// own state kept for the router) and holds it when it has CODE_LENGTH characters once normalised;
// any other code is dropped, and the pairing screen shows as usual. A hash with no code query, or
// another route's, is left alone.
export function holdPairLink(): void {
  held = undefined;
  const found = window.location.hash.match(PAIR_HASH);
  if (found === null) return;
  const raw = new URLSearchParams(found[1] ?? "").get("code");
  if (raw === null) return;
  window.history.replaceState(window.history.state, "", "#/pair");
  const code = normalizeCode(raw);
  if (code.length === CODE_LENGTH) held = code;
}

// The module's evaluation is the launch step: the cookie of a launch by ?ui=next is set before the
// worker registers by it, and the code is out of the address before the router reads it.
adoptNextApp();
holdPairLink();
