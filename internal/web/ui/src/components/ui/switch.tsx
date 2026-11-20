import { Switch as SwitchPrimitive } from "@base-ui/react/switch";
import type { ComponentProps, ReactElement } from "react";

import { cn } from "@/lib/utils";

// Switch is shadcn's Switch on Base UI: a 44 by 26 track coloured by the tokens, and a 44px hit
// area through the same pseudo-element ToggleGroupItem uses.
function Switch({
  className,
  ...props
}: ComponentProps<typeof SwitchPrimitive.Root>): ReactElement {
  return (
    <SwitchPrimitive.Root
      data-slot="switch"
      className={(state) =>
        cn(
          "relative inline-flex h-[26px] w-11 shrink-0 items-center rounded-full bg-input p-0.5 outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring data-disabled:opacity-50 data-checked:bg-primary data-unchecked:bg-input after:absolute after:top-1/2 after:left-1/2 after:h-full after:min-h-11 after:w-full after:min-w-11 after:-translate-x-1/2 after:-translate-y-1/2 after:content-['']",
          typeof className === "function" ? className(state) : className,
        )
      }
      {...props}
    >
      <SwitchPrimitive.Thumb
        data-slot="switch-thumb"
        className="block size-[22px] rounded-full bg-foreground transition-transform data-checked:translate-x-[18px] data-unchecked:translate-x-0"
      />
    </SwitchPrimitive.Root>
  );
}

export { Switch };
