import { Select as SelectPrimitive } from "@base-ui/react/select";
import { Check, ChevronDown } from "lucide-react";
import type { ComponentProps, ReactElement } from "react";

import { cn } from "@/lib/utils";

// Select is shadcn's Select on Base UI: the trigger and the items keep form text at 16px and a
// 44px floor, and the popup is a portal above the sticky header and the drawers.
const Select = SelectPrimitive.Root;

function SelectTrigger({
  className,
  children,
  ...props
}: ComponentProps<typeof SelectPrimitive.Trigger>): ReactElement {
  return (
    <SelectPrimitive.Trigger
      data-slot="select-trigger"
      className={(state) =>
        cn(
          "flex min-h-11 w-full items-center justify-between gap-2 rounded-lg border border-input bg-background px-3 text-[16px] text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50",
          typeof className === "function" ? className(state) : className,
        )
      }
      {...props}
    >
      {children}
      <ChevronDown aria-hidden className="size-4 shrink-0 text-faint" />
    </SelectPrimitive.Trigger>
  );
}

function SelectValue({
  className,
  ...props
}: ComponentProps<typeof SelectPrimitive.Value>): ReactElement {
  return (
    <SelectPrimitive.Value
      data-slot="select-value"
      className={(state) =>
        cn(
          "data-placeholder:text-faint",
          typeof className === "function" ? className(state) : className,
        )
      }
      {...props}
    />
  );
}

function SelectContent({
  className,
  children,
  ...props
}: ComponentProps<typeof SelectPrimitive.Popup>): ReactElement {
  return (
    <SelectPrimitive.Portal>
      <SelectPrimitive.Positioner className="z-50">
        <SelectPrimitive.Popup
          data-slot="select-content"
          className={(state) =>
            cn(
              "max-h-72 min-w-(--anchor-width) overflow-y-auto rounded-lg border border-border bg-popover text-popover-foreground shadow-lg",
              typeof className === "function" ? className(state) : className,
            )
          }
          {...props}
        >
          <SelectPrimitive.List>{children}</SelectPrimitive.List>
        </SelectPrimitive.Popup>
      </SelectPrimitive.Positioner>
    </SelectPrimitive.Portal>
  );
}

function SelectGroup({
  className,
  ...props
}: ComponentProps<typeof SelectPrimitive.Group>): ReactElement {
  return (
    <SelectPrimitive.Group
      data-slot="select-group"
      className={(state) => cn(typeof className === "function" ? className(state) : className)}
      {...props}
    />
  );
}

function SelectLabel({
  className,
  ...props
}: ComponentProps<typeof SelectPrimitive.GroupLabel>): ReactElement {
  return (
    <SelectPrimitive.GroupLabel
      data-slot="select-label"
      className={(state) =>
        cn(
          "px-3 py-2 text-xs text-faint",
          typeof className === "function" ? className(state) : className,
        )
      }
      {...props}
    />
  );
}

function SelectItem({
  className,
  children,
  ...props
}: ComponentProps<typeof SelectPrimitive.Item>): ReactElement {
  return (
    <SelectPrimitive.Item
      data-slot="select-item"
      className={(state) =>
        cn(
          "flex min-h-11 items-center gap-2 px-3 text-[16px] outline-none data-highlighted:bg-accent data-highlighted:text-accent-foreground data-disabled:opacity-50",
          typeof className === "function" ? className(state) : className,
        )
      }
      {...props}
    >
      <SelectPrimitive.ItemText className="flex-1">{children}</SelectPrimitive.ItemText>
      <SelectPrimitive.ItemIndicator>
        <Check aria-hidden className="size-4" />
      </SelectPrimitive.ItemIndicator>
    </SelectPrimitive.Item>
  );
}

export { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue };
