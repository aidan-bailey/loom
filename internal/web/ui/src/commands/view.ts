// The desktop's two view toggles. They are offline commands: they act on nothing in loom, so they
// run while the snapshot is stale (spec §16). Only desktop/DesktopShell.tsx imports this module,
// never commands/index.ts, so the phone's first load does not carry it.
import { defineCommand } from "@/app/commands";
import { useDesktop } from "@/app/desktopState";

defineCommand({
  id: "view.toggleSidebar",
  title: "Show or hide the sidebar",
  offline: true,
  palette: true,
  when: () => true,
  run: () => {
    const { sidebar, setSidebar } = useDesktop.getState();
    setSidebar(!sidebar);
    return Promise.resolve();
  },
});

defineCommand({
  id: "view.toggleInspector",
  title: "Show or hide the inspector",
  offline: true,
  palette: true,
  when: () => true,
  run: () => {
    const { inspector, setInspector } = useDesktop.getState();
    setInspector(!inspector);
    return Promise.resolve();
  },
});
