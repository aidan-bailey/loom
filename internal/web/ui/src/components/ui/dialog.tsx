import { Dialog as DialogPrimitive } from "@base-ui/react/dialog";
import type * as React from "react";
import { cn } from "@/lib/utils";

// Base UI's own Dialog, as drawer.tsx is Base UI's own Drawer: a library that appends a <style>
// element when it loads is blocked by the CSP (default-src 'self').
function Dialog({ ...props }: React.ComponentProps<typeof DialogPrimitive.Root>) {
  return <DialogPrimitive.Root data-slot="dialog" {...props} />;
}

function DialogContent({
  className,
  children,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Popup>) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Backdrop
        data-slot="dialog-backdrop"
        className="fixed inset-0 min-h-dvh bg-black/60 transition-opacity duration-150 data-ending-style:opacity-0 data-starting-style:opacity-0"
      />
      <DialogPrimitive.Viewport
        data-slot="dialog-viewport"
        className="fixed inset-0 flex items-center justify-center p-4"
      >
        <DialogPrimitive.Popup
          data-slot="dialog-content"
          className={(state) =>
            cn(
              "max-h-[90dvh] w-full max-w-md overflow-y-auto rounded-xl border border-border bg-card p-4 text-card-foreground outline-none transition-[opacity,transform] duration-150 data-ending-style:scale-[0.96] data-ending-style:opacity-0 data-starting-style:scale-[0.96] data-starting-style:opacity-0",
              typeof className === "function" ? className(state) : className,
            )
          }
          {...props}
        >
          {children}
        </DialogPrimitive.Popup>
      </DialogPrimitive.Viewport>
    </DialogPrimitive.Portal>
  );
}

function DialogTitle({ className, ...props }: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return (
    <DialogPrimitive.Title
      data-slot="dialog-title"
      className={(state) =>
        cn(
          "text-base font-semibold",
          typeof className === "function" ? className(state) : className,
        )
      }
      {...props}
    />
  );
}

function DialogDescription({
  className,
  ...props
}: React.ComponentProps<typeof DialogPrimitive.Description>) {
  return (
    <DialogPrimitive.Description
      data-slot="dialog-description"
      className={(state) =>
        cn(
          "text-sm text-muted-foreground",
          typeof className === "function" ? className(state) : className,
        )
      }
      {...props}
    />
  );
}

function DialogFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn("mt-4 flex flex-col gap-2 sm:flex-row-reverse sm:justify-start", className)}
      {...props}
    />
  );
}

export { Dialog, DialogContent, DialogTitle, DialogDescription, DialogFooter };
