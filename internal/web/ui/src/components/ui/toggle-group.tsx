import { Toggle as TogglePrimitive } from "@base-ui/react/toggle";
import { ToggleGroup as ToggleGroupPrimitive } from "@base-ui/react/toggle-group";
import type * as React from "react";
import { cn } from "@/lib/utils";

function ToggleGroup<Value extends string = string>({
  className,
  ...props
}: ToggleGroupPrimitive.Props<Value>) {
  return (
    <ToggleGroupPrimitive
      data-slot="toggle-group"
      className={(state) =>
        cn(
          "inline-flex w-full rounded-[9px] border border-border bg-card p-0.5",
          typeof className === "function" ? className(state) : className,
        )
      }
      {...props}
    />
  );
}

function ToggleGroupItem({ className, ...props }: React.ComponentProps<typeof TogglePrimitive>) {
  return (
    <TogglePrimitive
      data-slot="toggle-group-item"
      className={(state) =>
        cn(
          "relative flex flex-1 items-center justify-center rounded-[7px] py-1 text-xs text-faint outline-none focus-visible:ring-2 focus-visible:ring-ring data-pressed:bg-input data-pressed:text-white after:absolute after:top-1/2 after:left-1/2 after:h-full after:min-h-[44px] after:w-full after:min-w-[44px] after:-translate-x-1/2 after:-translate-y-1/2 after:content-['']",
          typeof className === "function" ? className(state) : className,
        )
      }
      {...props}
    />
  );
}

export { ToggleGroup, ToggleGroupItem };
