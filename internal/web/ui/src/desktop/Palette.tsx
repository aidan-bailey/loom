// The palette (spec §18, §20, Part 2 settlements): a Dialog around an inline Base UI Autocomplete
// over palette.ts's groups. Enter or a click opens a session or a place, or closes the palette and
// then runs the command through runCommand with the route's context. A module of the desktop chunk.
import { Autocomplete } from "@base-ui/react/autocomplete";
import { useMemo, useState, type ReactElement } from "react";

import { allCommands, runCommand, useCommandContext } from "@/app/commands";
import { sessionTabs } from "@/app/panels";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { cn } from "@/lib/utils";
import { useFleet } from "@/store/fleet";

import { paletteGroups, type PaletteGroup, type PaletteItem, type PaletteTab } from "./palette";
import { usePalette } from "./paletteState";

// Body is drawn inside the popup, which unmounts when closed, so its query is fresh each opening;
// it also resets the query when `open` becomes true, so a reopen during the closing transition
// starts empty too.
function Body(): ReactElement {
  const open = usePalette((s) => s.open);
  const setOpen = usePalette((s) => s.setOpen);
  const ctx = useCommandContext();
  const snapshot = useFleet((s) => s.snapshot);
  const [query, setQuery] = useState("");
  // Adjusted while rendering, as React documents for state that follows a prop: an effect would
  // draw the old query for one frame first.
  const [wasOpen, setWasOpen] = useState(open);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) setQuery("");
  }

  const tabs = useMemo<PaletteTab[]>(
    () =>
      ctx.session === undefined
        ? []
        : sessionTabs(ctx.session).map((panel) => ({
            id: panel.id,
            label: panel.tab?.label ?? panel.id,
          })),
    [ctx.session],
  );
  const groups = useMemo(
    () => paletteGroups({ query, snapshot, commands: allCommands(), ctx, tabs }),
    [query, snapshot, ctx, tabs],
  );

  // choose acts on one item: a session or a place goes to its link; a command closes the palette
  // first, so its confirmation shows alone, and then runs as a button's does.
  const choose = (item: PaletteItem): void => {
    if (item.kind === "command") {
      if (!item.enabled) return;
      setOpen(false);
      void runCommand(item.command, ctx, undefined);
      return;
    }
    setOpen(false);
    window.location.assign(item.href);
  };

  return (
    <Autocomplete.Root
      inline
      open
      filter={null}
      autoHighlight
      items={groups}
      value={query}
      onValueChange={setQuery}
    >
      <Autocomplete.Input
        data-slot="palette-input"
        placeholder="Search sessions, commands, places"
        className="w-full border-b border-border bg-transparent px-3 py-2 text-sm text-card-foreground outline-none placeholder:text-muted-foreground"
      />
      {groups.length === 0 ? (
        <p className="px-3 py-2 text-sm text-muted-foreground">Nothing matches</p>
      ) : (
        <Autocomplete.List className="max-h-[60dvh] overflow-y-auto p-1">
          {(group: PaletteGroup) => (
            <Autocomplete.Group key={group.label} items={group.items}>
              <Autocomplete.GroupLabel className="px-2 py-1 text-xs text-muted-foreground">
                {group.label}
              </Autocomplete.GroupLabel>
              <Autocomplete.Collection>
                {(item: PaletteItem) => (
                  <Autocomplete.Item
                    key={item.key}
                    value={item}
                    data-slot="palette-item"
                    data-kind={item.kind}
                    disabled={item.kind === "command" && !item.enabled}
                    onClick={() => choose(item)}
                    className={cn(
                      "flex cursor-default items-baseline justify-between gap-3 rounded-md px-2 py-1.5 text-sm data-highlighted:bg-accent",
                      item.kind === "command" && !item.enabled && "opacity-50",
                    )}
                  >
                    <span className="block min-w-0 truncate">{item.title}</span>
                    {item.kind !== "command" && item.hint !== "" ? (
                      <span className="block min-w-0 truncate text-xs text-muted-foreground">
                        {item.hint}
                      </span>
                    ) : null}
                  </Autocomplete.Item>
                )}
              </Autocomplete.Collection>
            </Autocomplete.Group>
          )}
        </Autocomplete.List>
      )}
    </Autocomplete.Root>
  );
}

export function Palette(): ReactElement {
  const open = usePalette((s) => s.open);
  const setOpen = usePalette((s) => s.setOpen);
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent
        data-slot="palette"
        className="max-w-xl p-0"
        // The palette's closing returns focus to nothing, so a confirmation that opens right after
        // it keeps its own focus (Cancel, for a destructive ask) and the page behind is not focused.
        finalFocus={false}
      >
        <DialogTitle className="sr-only">Command palette</DialogTitle>
        <Body />
      </DialogContent>
    </Dialog>
  );
}
