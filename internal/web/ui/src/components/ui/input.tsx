import type { ComponentProps, ReactElement } from "react";

import { cn } from "@/lib/utils";

// Input is shadcn's Input on a plain <input>: text at 16px (iOS zooms into a smaller field) and a
// 44px floor.
function Input({ className, ...props }: ComponentProps<"input">): ReactElement {
  return (
    <input
      data-slot="input"
      className={cn(
        "min-h-[44px] w-full rounded-lg border border-input bg-background px-3 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50",
        className,
      )}
      {...props}
    />
  );
}

export { Input };
