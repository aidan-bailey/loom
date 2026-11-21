import { useId, useState, useSyncExternalStore, type ReactElement } from "react";

import { Button } from "@/components/ui/button";
import {
  Drawer,
  DrawerContent,
  DrawerDescription,
  DrawerFooter,
  DrawerHeader,
  DrawerTitle,
} from "@/components/ui/drawer";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import { utf8Length } from "@/lib/utf8";

// TextField is a sheet's free text (Chat about this, Deny): `max` is its limit
// in UTF-8 bytes, past which the action is off and `tooLong` shows.
export interface TextField {
  kind: "text";
  label: string;
  placeholder: string;
  max: number;
  tooLong: string;
}

// ChoiceOption is one option of a ChoiceField: its `value` is what the sheet
// resolves, its `label` what the person reads.
export interface ChoiceOption {
  value: string;
  label: string;
}

// ChoiceField is a pick of one option (Merge's source session), drawn as a
// native <select> with the first option chosen.
export interface ChoiceField {
  kind: "choice";
  label: string;
  options: ChoiceOption[];
}

export type ConfirmField = TextField | ChoiceField;

// ConfirmSpec is what a sheet says: what the command does (`body` may run
// to several lines: the worktree's path, the tmux session), the action's
// label, whether the action is destructive (drawn as such), and at most one
// field whose value the action resolves.
export interface ConfirmSpec {
  title: string;
  body: string;
  action: string;
  destructive: boolean;
  field?: ConfirmField;
}

interface Ask {
  id: number;
  spec: ConfirmSpec;
  resolve: (value: string | null) => void;
}

// current is the open ask: at most one sheet is open at a time.
let current: Ask | null = null;
let seq = 0;
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

// askConfirm opens the confirmation sheet and resolves the field's value when
// the action is chosen (a text field's as typed, "" for a sheet with no
// field), and null when the sheet is cancelled, dismissed or replaced by a
// newer ask.
export function askConfirm(spec: ConfirmSpec): Promise<string | null> {
  current?.resolve(null);
  return new Promise<string | null>((resolve) => {
    seq += 1;
    current = { id: seq, spec, resolve };
    emit();
  });
}

// answer resolves the ask with that id, and does nothing when it is no longer
// the open one: a sheet's action, Cancel and dismissal answer their own ask,
// never a newer one that has since replaced it.
function answer(id: number, value: string | null): void {
  const ask = current;
  if (ask === null || ask.id !== id) return;
  current = null;
  emit();
  ask.resolve(value);
}

// testSeam reaches what a test needs and the module otherwise keeps to
// itself: the open ask's id, and `answer`, whose id check a replaced ask's
// buttons would otherwise never reach (they unmount with the ask).
export const testSeam = {
  openId: (): number | null => current?.id ?? null,
  answer,
};

// firstValue is the value a field starts with: a text field's empty text, a
// choice's first option.
function firstValue(field: ConfirmField | undefined): string {
  return field?.kind === "choice" ? (field.options[0]?.value ?? "") : "";
}

// ConfirmVariant is the form a sheet is drawn in: the phone's bottom drawer or
// the desktop's centred dialog.
export type ConfirmVariant = "drawer" | "dialog";

// Sheet is one ask's content in the form `variant` names. A newer ask replaces
// the text in place, inside the one DrawerContent or DialogContent, so the
// transition and focus trap do not restart.
function Sheet({ ask, variant }: { ask: Ask; variant: ConfirmVariant }): ReactElement {
  if (variant === "dialog") {
    return (
      <DialogContent>
        <div className="flex flex-col gap-1.5">
          <DialogTitle>{ask.spec.title}</DialogTitle>
          <DialogDescription className="break-words whitespace-pre-line">
            {ask.spec.body}
          </DialogDescription>
        </div>
        <AskBody key={ask.id} ask={ask} variant={variant} />
      </DialogContent>
    );
  }
  return (
    <DrawerContent>
      <DrawerHeader>
        <DrawerTitle>{ask.spec.title}</DrawerTitle>
        <DrawerDescription className="break-words whitespace-pre-line">
          {ask.spec.body}
        </DrawerDescription>
      </DrawerHeader>
      <AskBody key={ask.id} ask={ask} variant={variant} />
    </DrawerContent>
  );
}

// AskBody is the ask's field and buttons. Sheet keys it by the ask, so each
// ask starts with a field of its own; its buttons answer that ask by its id.
function AskBody({ ask, variant }: { ask: Ask; variant: ConfirmVariant }): ReactElement {
  const { spec } = ask;
  const Footer = variant === "dialog" ? DialogFooter : DrawerFooter;
  const [value, setValue] = useState(() => firstValue(spec.field));
  const fieldId = useId();
  const field = spec.field;
  const over = field !== undefined && field.kind === "text" && utf8Length(value) > field.max;
  // A choice with no options has nothing to resolve: its action stays off.
  const empty = field !== undefined && field.kind === "choice" && field.options.length === 0;
  return (
    <>
      {field === undefined ? null : (
        <div className="mt-3 flex flex-col gap-1.5">
          <label htmlFor={fieldId} className="text-[12.5px] font-semibold text-muted-foreground">
            {field.label}
          </label>
          {field.kind === "text" ? (
            <>
              <Textarea
                id={fieldId}
                value={value}
                placeholder={field.placeholder}
                onChange={(event) => {
                  setValue(event.target.value);
                }}
              />
              {over ? (
                <p role="alert" className="text-[12.5px] text-error">
                  {field.tooLong}
                </p>
              ) : null}
            </>
          ) : (
            <select
              id={fieldId}
              value={value}
              className="h-[44px] w-full rounded-lg border border-input bg-background px-3 text-[16px] text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"
              onChange={(event) => {
                setValue(event.target.value);
              }}
            >
              {field.options.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          )}
        </div>
      )}
      <Footer>
        <Button
          size="lg"
          variant={spec.destructive ? "destructive" : "default"}
          disabled={over || empty}
          onClick={() => {
            answer(ask.id, value);
          }}
        >
          {spec.action}
        </Button>
        <Button
          size="lg"
          variant="outline"
          onClick={() => {
            answer(ask.id, null);
          }}
        >
          Cancel
        </Button>
      </Footer>
    </>
  );
}

// ConfirmSheet is the one confirmation sheet, drawn as a drawer or a dialog by
// `variant`: AppShell mounts it once, and every command with a `confirm` opens
// it through runCommand. The last ask stays drawn after an answer, until the
// close has finished and it unmounts its content, so the exit transition plays
// over the sheet and not over an empty one. A variant that changes while an ask
// is open redraws the same ask in the other form.
export function ConfirmSheet({ variant }: { variant: ConfirmVariant }): ReactElement {
  const ask = useSyncExternalStore(subscribe, () => current);
  const [last, setLast] = useState<Ask | null>(null);
  if (ask !== null && ask !== last) setLast(ask);
  const shown = ask ?? last;
  const open = ask !== null;
  const onOpenChange = (next: boolean): void => {
    if (!next && ask !== null) answer(ask.id, null);
  };
  const sheet = shown === null ? null : <Sheet ask={shown} variant={variant} />;
  if (variant === "dialog") {
    return (
      <Dialog open={open} onOpenChange={onOpenChange}>
        {sheet}
      </Dialog>
    );
  }
  return (
    <Drawer open={open} onOpenChange={onOpenChange}>
      {sheet}
    </Drawer>
  );
}
