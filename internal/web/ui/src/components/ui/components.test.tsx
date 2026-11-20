import { readFileSync } from "node:fs";
import { CSPProvider } from "@base-ui/react/csp-provider";
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
import { Input } from "./input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "./select";
import { Skeleton } from "./skeleton";
import { Switch } from "./switch";
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

describe("Input", () => {
  it("Input: a plain input that takes its props, at 16px with a 44px floor, and a caller's class wins", async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(<Input placeholder="title" onChange={fn} />);
    const input = screen.getByPlaceholderText("title");
    expect(input.tagName).toBe("INPUT");
    expect(input).toHaveAttribute("data-slot", "input");
    expect(input.className).toContain("text-[16px]");
    expect(input.className).toContain("min-h-11");
    await user.type(input, "hi");
    expect(input).toHaveValue("hi");
    expect(fn).toHaveBeenCalledTimes(2);

    render(<Input className="min-h-14" placeholder="tall" type="password" id="pw" />);
    const tall = screen.getByPlaceholderText("tall");
    expect(tall.className).toContain("min-h-14");
    expect(tall.className).not.toContain("min-h-11");
    expect(tall).toHaveAttribute("type", "password");
    expect(tall).toHaveAttribute("id", "pw");
  });
});

describe("Switch", () => {
  it("Switch: a click turns it on, reports the new value, and a controlled one shows the value it is given", async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    const fn2 = vi.fn();
    render(
      <>
        <Switch aria-label="Needs you" defaultChecked={false} onCheckedChange={fn} />
        <Switch aria-label="Finished" checked={false} onCheckedChange={fn2} />
      </>,
    );
    const uncontrolled = screen.getByRole("switch", { name: "Needs you" });
    expect(uncontrolled).toHaveAttribute("aria-checked", "false");
    await user.click(uncontrolled);
    expect(uncontrolled).toHaveAttribute("aria-checked", "true");
    expect(fn.mock.calls[0]?.[0]).toBe(true);
    await user.click(uncontrolled);
    expect(uncontrolled).toHaveAttribute("aria-checked", "false");
    expect(fn.mock.calls[1]?.[0]).toBe(false);

    const controlled = screen.getByRole("switch", { name: "Finished" });
    await user.click(controlled);
    expect(fn2.mock.calls[0]?.[0]).toBe(true);
    expect(controlled).toHaveAttribute("aria-checked", "false");
  });

  it("Switch: a disabled one takes no click, and its hit area reaches 44px", async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(
      <>
        <Switch disabled onCheckedChange={fn} aria-label="Off" />
        <Switch aria-label="On" />
      </>,
    );
    const off = screen.getByRole("switch", { name: "Off" });
    await user.click(off);
    expect(fn).not.toHaveBeenCalled();
    expect(off).toHaveAttribute("aria-checked", "false");
    const on = screen.getByRole("switch", { name: "On" });
    expect(on.className).toContain("after:min-h-11");
    expect(on.className).toContain("after:min-w-11");
    expect(on).toHaveAttribute("data-slot", "switch");
  });
});

function SampleSelect({
  value,
  onValueChange,
  disabled,
  triggerClassName,
}: {
  value: string;
  onValueChange: (value: string | null) => void;
  disabled?: boolean;
  triggerClassName?: string;
}) {
  return (
    <CSPProvider disableStyleElements>
      <Select
        items={{ a: "Alpha", b: "Bravo", c: "Charlie" }}
        value={value}
        onValueChange={onValueChange}
        disabled={disabled}
      >
        <SelectTrigger aria-label="Pick" id="pick" className={triggerClassName}>
          <SelectValue placeholder="(default)" />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectLabel>Active</SelectLabel>
            <SelectItem value="a">Alpha</SelectItem>
            <SelectItem value="b">Bravo</SelectItem>
          </SelectGroup>
          <SelectGroup>
            <SelectLabel>Other</SelectLabel>
            <SelectItem value="c">Charlie</SelectItem>
          </SelectGroup>
        </SelectContent>
      </Select>
    </CSPProvider>
  );
}

describe("Select", () => {
  it("Select: the trigger shows the chosen label, opening lists the groups, and picking an item reports its value", async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    const { rerender } = render(<SampleSelect value="b" onValueChange={fn} />);
    expect(screen.getByRole("combobox", { name: "Pick" })).toHaveTextContent("Bravo");
    rerender(<SampleSelect value="" onValueChange={fn} />);
    const trigger = screen.getByRole("combobox", { name: "Pick" });
    expect(trigger).toHaveTextContent("(default)");

    await user.click(trigger);
    const list = await screen.findByRole("listbox");
    expect(within(list).getByText("Active")).toBeInTheDocument();
    expect(within(list).getByText("Other")).toBeInTheDocument();
    expect(
      within(list)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["Alpha", "Bravo", "Charlie"]);
    await user.click(within(list).getByRole("option", { name: "Charlie" }));
    expect(fn.mock.calls[0]?.[0]).toBe("c");
  });

  it("Select: a disabled one does not open", async () => {
    const user = userEvent.setup();
    render(<SampleSelect value="a" onValueChange={vi.fn()} disabled />);
    await user.click(screen.getByRole("combobox", { name: "Pick" }));
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  it("Select: its trigger, items and label keep the phone sizes, the slots and the caller's props", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<SampleSelect value="a" onValueChange={vi.fn()} />);
    const trigger = screen.getByRole("combobox", { name: "Pick" });
    expect(trigger).toHaveAttribute("data-slot", "select-trigger");
    expect(trigger).toHaveAttribute("id", "pick");
    expect(trigger.className).toContain("min-h-11");
    expect(trigger.className).toContain("text-[16px]");

    rerender(<SampleSelect value="a" onValueChange={vi.fn()} triggerClassName="w-40" />);
    const narrow = screen.getByRole("combobox", { name: "Pick" });
    expect(narrow.className).toContain("w-40");
    expect(narrow.className).not.toContain("w-full");

    await user.click(narrow);
    const list = await screen.findByRole("listbox");
    for (const option of within(list).getAllByRole("option")) {
      expect(option).toHaveAttribute("data-slot", "select-item");
      expect(option.className).toContain("min-h-11");
    }
    expect(within(list).getByText("Active")).toHaveAttribute("data-slot", "select-label");
  });
});
