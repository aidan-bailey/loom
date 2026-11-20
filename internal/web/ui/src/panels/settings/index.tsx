// The settings panel: the head names the screen and goes back to the fleet; the body is a lazy
// chunk, so a first launch never loads it (spec Part 3 settlement 6).
import { lazy, type ReactElement } from "react";

import { definePanel } from "@/app/panels";
import { hrefFor } from "@/app/routes";
import { ContextRow } from "@/app/shell";

// SettingsHead is the settings screen's context row: its title and the way back to the fleet.
export function SettingsHead(): ReactElement {
  return <ContextRow back={hrefFor("fleet")} title="Settings" />;
}

const SettingsBody = lazy(() => import("./SettingsBody"));

export const settingsPanel = definePanel({
  id: "settings",
  title: () => "Settings",
  Head: SettingsHead,
  Body: SettingsBody,
});
