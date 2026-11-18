import { Toast } from "@base-ui/react/toast";
import { X } from "lucide-react";
import type { ReactElement } from "react";

// Base UI's own toast, not sonner: sonner appends a <style> element when it loads, which the CSP
// (default-src 'self') blocks. Code outside React adds a toast with toastManager.add({...}); a toast
// added with timeout: 0 stays until dismissed.
export const toastManager = Toast.createToastManager();

function ToastList(): ReactElement {
  const { toasts } = Toast.useToastManager();
  return (
    <>
      {toasts.map((toast) => (
        <Toast.Root
          key={toast.id}
          toast={toast}
          data-slot="toast"
          className="flex items-start gap-2 rounded-[10px] border border-border bg-secondary px-3 py-2 text-[12.5px] shadow-lg transition-[opacity,transform] duration-200 data-ending-style:opacity-0 data-starting-style:-translate-y-2 data-starting-style:opacity-0"
        >
          <Toast.Content className="flex min-w-0 flex-1 flex-col gap-0.5">
            <Toast.Title
              className={toast.type === "error" ? "font-semibold text-error" : "font-semibold"}
            />
            <Toast.Description className="text-muted-foreground" />
          </Toast.Content>
          <Toast.Close
            aria-label="Dismiss"
            // Base UI hides a collapsed stack's close buttons from assistive tech; this list
            // shows every toast in full, so each close control stays reachable.
            aria-hidden={false}
            className="relative inline-flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring after:absolute after:top-1/2 after:left-1/2 after:h-full after:min-h-11 after:w-full after:min-w-11 after:-translate-x-1/2 after:-translate-y-1/2 after:content-['']"
          >
            <X className="size-4" aria-hidden="true" />
          </Toast.Close>
        </Toast.Root>
      ))}
    </>
  );
}

export function Toaster(): ReactElement {
  return (
    <Toast.Provider toastManager={toastManager} limit={8}>
      <Toast.Portal>
        <Toast.Viewport className="fixed top-[max(0.5rem,env(safe-area-inset-top))] left-1/2 z-50 flex w-[min(100vw-1rem,24rem)] -translate-x-1/2 flex-col gap-2 outline-none">
          <ToastList />
        </Toast.Viewport>
      </Toast.Portal>
    </Toast.Provider>
  );
}
