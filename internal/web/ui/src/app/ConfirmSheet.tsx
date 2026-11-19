import { useState, useSyncExternalStore, type ReactElement } from "react";

import { Button } from "@/components/ui/button";
import {
  Drawer,
  DrawerContent,
  DrawerDescription,
  DrawerFooter,
  DrawerHeader,
  DrawerTitle,
} from "@/components/ui/drawer";

// ConfirmSpec is what a sheet says: what the command does (`body` may run
// to several lines: the worktree's path, the tmux session), the action's
// label, and whether the action is destructive (drawn as such).
export interface ConfirmSpec {
  title: string;
  body: string;
  action: string;
  destructive: boolean;
}

interface Ask {
  spec: ConfirmSpec;
  resolve: (confirmed: boolean) => void;
}

// current is the open ask: at most one sheet is open at a time.
let current: Ask | null = null;
const listeners = new Set<() => void>();

function emit(): void {
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

// askConfirm opens the confirmation sheet and resolves true when the action
// is chosen, false when the sheet is cancelled, dismissed or replaced by a
// newer ask.
export function askConfirm(spec: ConfirmSpec): Promise<boolean> {
  current?.resolve(false);
  return new Promise<boolean>((resolve) => {
    current = { spec, resolve };
    emit();
  });
}

function answer(confirmed: boolean): void {
  const ask = current;
  if (ask === null) return;
  current = null;
  emit();
  ask.resolve(confirmed);
}

// ConfirmSheet is the one confirmation drawer: AppShell mounts it once, and
// every command with a `confirm` opens it through runCommand. The last spec
// stays drawn after an answer, until the drawer's close has finished and it
// unmounts its content, so the exit transition plays over the sheet and not
// over an empty one.
export function ConfirmSheet(): ReactElement {
  const ask = useSyncExternalStore(subscribe, () => current);
  const [last, setLast] = useState<ConfirmSpec | null>(null);
  if (ask !== null && ask.spec !== last) setLast(ask.spec);
  const spec = ask === null ? last : ask.spec;
  return (
    <Drawer
      open={ask !== null}
      onOpenChange={(open) => {
        if (!open) answer(false);
      }}
    >
      {spec === null ? null : (
        <DrawerContent>
          <DrawerHeader>
            <DrawerTitle>{spec.title}</DrawerTitle>
            <DrawerDescription className="break-words whitespace-pre-line">
              {spec.body}
            </DrawerDescription>
          </DrawerHeader>
          <DrawerFooter>
            <Button
              size="lg"
              variant={spec.destructive ? "destructive" : "default"}
              onClick={() => {
                answer(true);
              }}
            >
              {spec.action}
            </Button>
            <Button
              size="lg"
              variant="outline"
              onClick={() => {
                answer(false);
              }}
            >
              Cancel
            </Button>
          </DrawerFooter>
        </DrawerContent>
      )}
    </Drawer>
  );
}
