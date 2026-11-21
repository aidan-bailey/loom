import { CSPProvider } from "@base-ui/react/csp-provider";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

// First of the app's own imports (spec §26): its evaluation sets the cookie of a launch by ?ui=next
// and takes a pairing link's code out of the address, and app/routes.tsx builds the router from
// the address when a later import reaches it. Keep every import that reaches the router below it.
import "@/app/pairHold";

import "./styles/app.css";
import "@/panels";
import "@/commands";

import { queryClient } from "@/api/queries";
import { onUnpaired } from "@/api/request";
import { AddToHome, needsInstall } from "@/app/install";
import { watchPairLink } from "@/app/pairLink";
import { goToPairing, router, watchHash } from "@/app/routes";
import { watchNotices } from "@/app/toasts";
import { registerWorker } from "@/app/worker";
import { startFleet } from "@/store/fleet";

const mount = document.getElementById("app");
if (mount === null) throw new Error("loom: the page has no #app element");

// The worker registers at every launch, first, whether or not the gate shows: it updates and
// the push check runs either way (spec §5). The Add to Home gate starts no stream and no route
// (Part 3 settlement 15): on an iOS browser tab that has not chosen the browser only AddToHome is
// drawn, and the app starts when "Continue in the browser anyway" is pressed. needsInstall() is
// read once, so after Continue the app starts whatever it would now say.
registerWorker();

const root = createRoot(mount);

let stopHash: (() => void) | undefined;
let stopNotices: (() => void) | undefined;
let stopFleet: (() => void) | undefined;
let stopPairLink: (() => void) | undefined;
let stopUnpaired: (() => void) | undefined;
let started = false;

function start(): void {
  if (started) return;
  started = true;
  stopHash = watchHash();
  stopNotices = watchNotices();
  stopPairLink = watchPairLink();
  stopFleet = startFleet();
  stopUnpaired = onUnpaired(goToPairing);

  // Base UI's CSPProvider keeps its components from writing <style> elements,
  // which the app's Content-Security-Policy refuses (spec §33).
  root.render(
    <StrictMode>
      <CSPProvider disableStyleElements>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </CSPProvider>
    </StrictMode>,
  );
}

if (needsInstall()) {
  root.render(
    <StrictMode>
      <CSPProvider disableStyleElements>
        <AddToHome onContinue={start} />
      </CSPProvider>
    </StrictMode>,
  );
} else {
  start();
}

if (import.meta.hot) {
  import.meta.hot.dispose(() => {
    stopHash?.();
    stopNotices?.();
    stopPairLink?.();
    stopFleet?.();
    stopUnpaired?.();
  });
}
