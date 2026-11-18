import { Drawer as DrawerPrimitive } from "@base-ui/react/drawer";
import type * as React from "react";
import { cn } from "@/lib/utils";

// Base UI's own Drawer, not shadcn's vaul one: vaul appends a <style> element when it loads, which
// the CSP (default-src 'self') blocks.
function Drawer({ ...props }: React.ComponentProps<typeof DrawerPrimitive.Root>) {
  return <DrawerPrimitive.Root data-slot="drawer" swipeDirection="down" {...props} />;
}

function DrawerContent({
  className,
  children,
  ...props
}: React.ComponentProps<typeof DrawerPrimitive.Popup>) {
  return (
    <DrawerPrimitive.Portal>
      <DrawerPrimitive.Backdrop
        data-slot="drawer-backdrop"
        className="fixed inset-0 min-h-dvh bg-black/60 transition-opacity duration-300 data-ending-style:opacity-0 data-starting-style:opacity-0"
      />
      <DrawerPrimitive.Viewport
        data-slot="drawer-viewport"
        className="fixed inset-0 flex items-end justify-center"
      >
        <DrawerPrimitive.Popup
          data-slot="drawer-content"
          className={(state) =>
            cn(
              "max-h-[90dvh] w-full overflow-y-auto overscroll-contain rounded-t-2xl border border-border bg-card px-4 pt-2 pb-[max(1rem,env(safe-area-inset-bottom))] text-card-foreground outline-none transition-transform duration-300 [transform:translateY(var(--drawer-swipe-movement-y,0px))] data-ending-style:[transform:translateY(100%)] data-starting-style:[transform:translateY(100%)]",
              typeof className === "function" ? className(state) : className,
            )
          }
          {...props}
        >
          <div
            aria-hidden="true"
            className="mx-auto mb-3 h-1 w-9 rounded-full bg-input"
            data-slot="drawer-handle"
          />
          <DrawerPrimitive.Content data-slot="drawer-body">{children}</DrawerPrimitive.Content>
        </DrawerPrimitive.Popup>
      </DrawerPrimitive.Viewport>
    </DrawerPrimitive.Portal>
  );
}

function DrawerHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div data-slot="drawer-header" className={cn("flex flex-col gap-1.5", className)} {...props} />
  );
}

function DrawerTitle({ className, ...props }: React.ComponentProps<typeof DrawerPrimitive.Title>) {
  return (
    <DrawerPrimitive.Title
      data-slot="drawer-title"
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

function DrawerDescription({
  className,
  ...props
}: React.ComponentProps<typeof DrawerPrimitive.Description>) {
  return (
    <DrawerPrimitive.Description
      data-slot="drawer-description"
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

function DrawerFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="drawer-footer"
      className={cn("mt-3 flex flex-col gap-2", className)}
      {...props}
    />
  );
}

function DrawerClose({ ...props }: React.ComponentProps<typeof DrawerPrimitive.Close>) {
  return <DrawerPrimitive.Close data-slot="drawer-close" {...props} />;
}

export {
  Drawer,
  DrawerContent,
  DrawerHeader,
  DrawerTitle,
  DrawerDescription,
  DrawerFooter,
  DrawerClose,
};
