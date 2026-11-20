import { readFileSync } from "node:fs";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { cn } from "@/lib/utils";
import { Badge } from "./badge";
import { Button } from "./button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "./collapsible";
import {
  Drawer,
  DrawerClose,
  DrawerContent,
  DrawerDescription,
  DrawerFooter,
  DrawerHeader,
  DrawerTitle,
} from "./drawer";
import { Skeleton } from "./skeleton";
import { Textarea } from "./textarea";
import { ToggleGroup, ToggleGroupItem } from "./toggle-group";
import { Toaster, toastManager } from "./toast";

describe("cn", () => {
  it("cn joins its inputs, drops falsy ones and lets the last conflicting utility win", () => {
    expect(cn("a", false, ["b", undefined], "c")).toBe("a b c");
    expect(cn("p-2", "p-4")).toBe("p-4");
    expect(cn("text-faint", "text-muted-foreground")).toBe("text-muted-foreground");
  });
});

describe("Button", () => {
  it("Button: each variant and size has its own classes, and every size keeps the 44px hit area", () => {
    const variants = ["default", "secondary", "ghost", "outline", "destructive"] as const;
    const classes = variants.map((variant) => {
      const { unmount } = render(<Button variant={variant}>{variant}</Button>);
      const className = screen.getByRole("button").className;
      unmount();
      return className;
    });
    expect(new Set(classes).size).toBe(variants.length);
    expect(classes[0]).toContain("bg-primary");
    expect(classes[4]).toContain("text-destructive");

    for (const size of ["default", "sm", "lg", "icon"] as const) {
      const { unmount } = render(<Button size={size}>{size}</Button>);
      const className = screen.getByRole("button").className;
      expect(className).toContain("after:min-h-11");
      expect(className).toContain("after:min-w-11");
      unmount();
    }

    render(<Button>plain</Button>);
    expect(screen.getByRole("button")).toHaveAttribute("data-slot", "button");
  });

  it("Button: a click reaches onClick, and a disabled button gets none", async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(
      <>
        <Button onClick={fn}>on</Button>
        <Button onClick={fn} disabled>
          off
        </Button>
      </>,
    );
    await user.click(screen.getByText("on"));
    expect(fn).toHaveBeenCalledTimes(1);
    await user.click(screen.getByText("off"));
    expect(fn).toHaveBeenCalledTimes(1);
  });
});

describe("Badge", () => {
  it("Badge: the seven variants differ, the default is neutral, and it renders its children", () => {
    const variants = {
      attention: "text-attention",
      turn: "text-[#8cb8e8]",
      working: "text-working",
      error: "text-error",
      workspace: "text-workspace",
      docs: "text-docs",
      neutral: "text-muted-foreground",
    } as const;
    const classes: Record<string, string> = {};
    for (const [variant, token] of Object.entries(variants)) {
      const { unmount } = render(
        <Badge variant={variant as keyof typeof variants}>{variant}</Badge>,
      );
      const className = screen.getByText(variant).className;
      expect(className).toContain(token);
      classes[variant] = className;
      unmount();
    }
    expect(new Set(Object.values(classes)).size).toBe(7);
    expect(classes.turn).toContain("bg-turn");

    render(<Badge>plain</Badge>);
    expect(screen.getByText("plain").className).toBe(classes.neutral);
  });
});

describe("ToggleGroup", () => {
  it("ToggleGroup: a press calls onValueChange with the new array and a single group replaces the old value", async () => {
    const user = userEvent.setup();
    const single = vi.fn();
    const { unmount } = render(
      <ToggleGroup defaultValue={["all"]} onValueChange={single}>
        <ToggleGroupItem value="all">all</ToggleGroupItem>
        <ToggleGroupItem value="loom">loom</ToggleGroupItem>
        <ToggleGroupItem value="flow">flow</ToggleGroupItem>
      </ToggleGroup>,
    );
    await user.click(screen.getByText("loom"));
    expect(single.mock.calls[0][0]).toEqual(["loom"]);
    expect(screen.getByText("loom")).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText("all")).toHaveAttribute("aria-pressed", "false");
    unmount();

    const multi = vi.fn();
    render(
      <ToggleGroup multiple defaultValue={["all"]} onValueChange={multi}>
        <ToggleGroupItem value="all">all</ToggleGroupItem>
        <ToggleGroupItem value="loom">loom</ToggleGroupItem>
      </ToggleGroup>,
    );
    await user.click(screen.getByText("loom"));
    expect(multi.mock.calls[0][0]).toEqual(["all", "loom"]);
  });
});

describe("Collapsible", () => {
  it("Collapsible: the content shows only while open", async () => {
    const user = userEvent.setup();
    render(
      <Collapsible>
        <CollapsibleTrigger>toggle</CollapsibleTrigger>
        <CollapsibleContent>folded</CollapsibleContent>
      </Collapsible>,
    );
    expect(screen.queryByText("folded")).toBeNull();
    await user.click(screen.getByText("toggle"));
    expect(await screen.findByText("folded")).toBeInTheDocument();
    await user.click(screen.getByText("toggle"));
    await waitFor(() => expect(screen.queryByText("folded")).toBeNull());
  });
});

describe("Skeleton", () => {
  it("Skeleton: renders a div with the pulse and the muted surface", () => {
    render(<Skeleton data-testid="sk" />);
    const el = screen.getByTestId("sk");
    expect(el).toHaveAttribute("data-slot", "skeleton");
    expect(el.className).toContain("animate-pulse");
    expect(el.className).toContain("bg-muted");
  });
});

describe("Textarea", () => {
  it("Textarea: a plain textarea that takes its props, at 16px, growing with its content to 120px", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Textarea placeholder="reply" onChange={onChange} />);
    const el = screen.getByPlaceholderText("reply");
    expect(el.tagName).toBe("TEXTAREA");
    expect(el).toHaveAttribute("data-slot", "textarea");
    expect(el).toHaveClass("field-sizing-content", "max-h-[120px]", "text-[16px]");
    await user.type(el, "hi");
    expect(el).toHaveValue("hi");
    expect(onChange).toHaveBeenCalledTimes(2);
  });

  it("Textarea: a caller's class wins over the one it conflicts with", () => {
    render(<Textarea placeholder="tall" className="max-h-40" />);
    const el = screen.getByPlaceholderText("tall");
    expect(el).toHaveClass("max-h-40");
    expect(el).not.toHaveClass("max-h-[120px]");
  });
});

function SampleDrawer({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
}) {
  return (
    <Drawer open={open} onOpenChange={onOpenChange}>
      <DrawerContent>
        <DrawerHeader>
          <DrawerTitle>Kill fix-pane-cursor?</DrawerTitle>
          <DrawerDescription>The worktree is stashed first.</DrawerDescription>
        </DrawerHeader>
        <DrawerFooter>
          <DrawerClose>Cancel</DrawerClose>
        </DrawerFooter>
      </DrawerContent>
    </Drawer>
  );
}

describe("Drawer", () => {
  it("Drawer: it shows its title and description while open, none while closed, and its close control reports false", async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    const { unmount } = render(<SampleDrawer open onOpenChange={fn} />);
    expect(await screen.findByRole("dialog")).toHaveAccessibleName("Kill fix-pane-cursor?");
    expect(screen.getByText("The worktree is stashed first.")).toBeInTheDocument();
    await user.click(screen.getByText("Cancel"));
    expect(fn.mock.calls[0][0]).toBe(false);
    unmount();

    render(<SampleDrawer open={false} onOpenChange={vi.fn()} />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

describe("Toaster", () => {
  it("Toaster: a toast added through toastManager shows, an error one is styled, and its close control dismisses it", async () => {
    const user = userEvent.setup();
    render(<Toaster />);
    act(() => {
      toastManager.add({
        title: "Killed fix-pane-cursor",
        description: "Stashed as a1b2c3d",
        timeout: 0,
      });
      toastManager.add({ title: "Could not push", type: "error", timeout: 0 });
    });
    const killed = await screen.findByText("Killed fix-pane-cursor");
    expect(screen.getByText("Stashed as a1b2c3d")).toBeInTheDocument();
    const failed = await screen.findByText("Could not push");
    expect(failed.className).toContain("text-error");
    expect(killed.className).not.toContain("text-error");

    const root = killed.closest("[data-slot='toast']") as HTMLElement;
    await user.click(within(root).getByRole("button", { name: "Dismiss" }));
    await waitFor(() => expect(screen.queryByText("Killed fix-pane-cursor")).toBeNull());
    expect(screen.getByText("Could not push")).toBeInTheDocument();
  });
});

describe("no <style> element", () => {
  it("No component injects a <style> element, on import or in use", async () => {
    expect(document.querySelectorAll("style").length).toBe(0);
    render(
      <>
        <Button>b</Button>
        <Badge>g</Badge>
        <ToggleGroup defaultValue={["a"]}>
          <ToggleGroupItem value="a">a</ToggleGroupItem>
        </ToggleGroup>
        <Collapsible defaultOpen>
          <CollapsibleTrigger>t</CollapsibleTrigger>
          <CollapsibleContent>c</CollapsibleContent>
        </Collapsible>
        <SampleDrawer open onOpenChange={vi.fn()} />
        <Skeleton />
        <Textarea />
        <Toaster />
      </>,
    );
    act(() => {
      toastManager.add({ title: "hello", timeout: 0 });
    });
    await screen.findByText("hello");
    await screen.findByRole("dialog", { name: "Kill fix-pane-cursor?" });
    expect(document.querySelectorAll("style").length).toBe(0);
  });
});

// Vitest replaces a stylesheet's text with "" (also under ?raw), so the file is read from disk.
const appCss = readFileSync("src/styles/app.css", "utf8");

describe("the scrollbar rules", () => {
  it("app.css carries the two .base-ui-disable-scrollbar rules Base UI no longer injects", () => {
    expect(appCss).toMatch(/\.base-ui-disable-scrollbar\s*\{[^}]*scrollbar-width:\s*none/);
    expect(appCss).toMatch(
      /\.base-ui-disable-scrollbar::-webkit-scrollbar\s*\{[^}]*display:\s*none/,
    );
  });
});
