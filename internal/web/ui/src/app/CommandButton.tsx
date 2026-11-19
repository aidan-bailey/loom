import type { ReactElement } from "react";

import { Button } from "@/components/ui/button";

import {
  commandById,
  commandEnabled,
  runCommand,
  useCommandBusy,
  useCommandContext,
} from "./commands";

// CommandButton draws a registered command as a button: its title (or
// `label`), its icon, off when the command is not enabled and while the
// command is in flight for the route's session (runCommand's in-flight set, so
// every button of the command goes off together and a double tap runs it
// once). Confirmation and toasts are runCommand's, so every command behaves
// alike.
export function CommandButton({
  id,
  args,
  variant,
  size,
  label,
}: {
  id: string;
  args?: unknown;
  variant?: "default" | "secondary" | "ghost" | "outline" | "destructive";
  size?: "default" | "sm" | "lg" | "icon";
  label?: string;
}): ReactElement {
  const ctx = useCommandContext();
  const command = commandById(id);
  if (command === undefined) throw new Error(`loom: unknown command ${id}`);
  const busy = useCommandBusy(command, ctx);

  const title = typeof command.title === "function" ? command.title(ctx) : command.title;
  const Icon = command.icon;

  const run = (): void => {
    void runCommand(command, ctx, args);
  };

  return (
    <Button
      variant={variant}
      size={size}
      disabled={busy || !commandEnabled(command, ctx)}
      aria-label={size === "icon" ? title : undefined}
      onClick={run}
    >
      {Icon === undefined ? null : <Icon aria-hidden="true" />}
      {size === "icon" ? null : (label ?? title)}
    </Button>
  );
}
