import { CSPProvider } from "@base-ui/react/csp-provider";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import "./styles/app.css";
import "@/panels";

import { queryClient } from "@/api/queries";
import { onUnpaired } from "@/api/request";
import { goToPairing, router, watchHash } from "@/app/routes";
import { watchNotices } from "@/app/toasts";
import { registerWorker } from "@/app/worker";
import { startFleet } from "@/store/fleet";

const mount = document.getElementById("app");
if (mount === null) throw new Error("loom: the page has no #app element");

const stopHash = watchHash();
const stopNotices = watchNotices();
const stopFleet = startFleet();
const stopUnpaired = onUnpaired(goToPairing);
registerWorker();

// Base UI's CSPProvider keeps its components from writing <style> elements,
// which the app's Content-Security-Policy refuses (spec §33).
createRoot(mount).render(
  <StrictMode>
    <CSPProvider disableStyleElements>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </CSPProvider>
  </StrictMode>,
);

if (import.meta.hot) {
  import.meta.hot.dispose(() => {
    stopHash();
    stopNotices();
    stopFleet();
    stopUnpaired();
  });
}
