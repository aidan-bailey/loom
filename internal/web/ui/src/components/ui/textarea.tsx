import type { ComponentProps, ReactElement } from "react";

import { cn } from "@/lib/utils";

// Textarea is shadcn's Textarea on a plain <textarea>: text at 16px (iOS zooms
// into a smaller field), a 44px floor, and `field-sizing: content` so it grows
// with its text to 120px, past which it scrolls.
function Textarea({ className, ...props }: ComponentProps<"textarea">): ReactElement {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        "field-sizing-content max-h-[120px] min-h-[44px] w-full resize-none overflow-y-auto rounded-lg border border-input bg-background px-3 py-2.5 text-[16px] text-foreground outline-none placeholder:text-faint focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50",
        className,
      )}
      {...props}
    />
  );
}

export { Textarea };
