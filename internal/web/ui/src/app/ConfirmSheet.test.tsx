import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Check } from "lucide-react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { fixtureSnapshot } from "@/fixtures";
import { useFleet } from "@/store/fleet";

import { CommandButton } from "./CommandButton";
import { defineCommand, type CommandContext } from "./commands";
import { askConfirm, ConfirmSheet, type ConfirmSpec } from "./ConfirmSheet";

const kill: ConfirmSpec = {
  title: "Kill session",
  body: "Removes the worktree /w/x and ends the tmux session loom_x.",
  action: "Kill",
  destructive: true,
};

// ask opens the sheet with spec and returns the promise askConfirm gave.
function ask(spec: ConfirmSpec): Promise<boolean> {
  let answer = Promise.resolve(false);
  act(() => {
    answer = askConfirm(spec);
  });
  return answer;
}

describe("ConfirmSheet", () => {
  it("draws nothing while nobody asks", () => {
    render(<ConfirmSheet />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("names what the command does, and resolves true on its action", async () => {
    render(<ConfirmSheet />);
    const answer = ask(kill);

    expect(await screen.findByText("Kill session")).toBeInTheDocument();
    expect(screen.getByText(/Removes the worktree \/w\/x/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Kill" }));
    expect(await answer).toBe(true);
    await waitFor(() => {
      expect(screen.queryByText("Kill session")).toBeNull();
    });
  });

  it("resolves false on Cancel", async () => {
    render(<ConfirmSheet />);
    const answer = ask(kill);

    await userEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    expect(await answer).toBe(false);
  });

  it("resolves false on Escape", async () => {
    render(<ConfirmSheet />);
    const answer = ask(kill);
    await screen.findByText("Kill session");

    await userEvent.keyboard("{Escape}");
    expect(await answer).toBe(false);
  });

  it("lets a newer ask replace an open one, which resolves false", async () => {
    render(<ConfirmSheet />);
    const first = ask(kill);
    await screen.findByText("Kill session");

    const second = ask({
      title: "Push",
      body: "Pushes the branch.",
      action: "Push",
      destructive: false,
    });

    expect(await first).toBe(false);
    expect(await screen.findByText("Pushes the branch.")).toBeInTheDocument();
    expect(screen.queryByText("Kill session")).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Push" }));
    expect(await second).toBe(true);
  });
});

afterEach(() => {
  useFleet.setState({ snapshot: null, stale: true, link: "connecting", updated: 0 });
  window.history.replaceState(null, "", window.location.pathname);
});

function live(): void {
  useFleet.setState({ snapshot: fixtureSnapshot(), stale: false, link: "live" });
}

describe("CommandButton", () => {
  it("draws the command's title and runs it with the arguments on a click", async () => {
    live();
    const run = vi.fn<(ctx: CommandContext, n: number) => Promise<void>>(() => Promise.resolve());
    defineCommand<number>({ id: "test.button.run", title: "Do it", when: () => true, run });

    render(<CommandButton id="test.button.run" args={7} />);
    await userEvent.click(screen.getByRole("button", { name: "Do it" }));

    expect(run).toHaveBeenCalledOnce();
    expect(run.mock.calls[0]?.[1]).toBe(7);
  });

  it("takes a title that reads the context, and a label over the title", () => {
    live();
    defineCommand({
      id: "test.button.title",
      title: (ctx) => `Panel ${ctx.panel}`,
      when: () => true,
      run: () => Promise.resolve(),
    });
    window.history.replaceState(null, "", "#/settings");

    const { rerender } = render(<CommandButton id="test.button.title" />);
    expect(screen.getByRole("button", { name: "Panel settings" })).toBeInTheDocument();

    rerender(<CommandButton id="test.button.title" label="Go" />);
    expect(screen.getByRole("button", { name: "Go" })).toBeInTheDocument();
  });

  it("is an icon button named by the title when its size is icon", () => {
    live();
    defineCommand({
      id: "test.button.icon",
      title: "Mark",
      icon: Check,
      when: () => true,
      run: () => Promise.resolve(),
    });

    render(<CommandButton id="test.button.icon" size="icon" />);

    expect(screen.getByRole("button", { name: "Mark" }).textContent).toBe("");
  });

  it("is off while the snapshot is stale, and when its own when says no", async () => {
    const run = vi.fn(() => Promise.resolve());
    defineCommand({ id: "test.button.off", title: "Off", when: () => true, run });
    defineCommand({ id: "test.button.no", title: "No", when: () => false, run });

    // The store is stale until its first snapshot.
    const { unmount } = render(<CommandButton id="test.button.off" />);
    expect(screen.getByRole("button", { name: "Off" })).toBeDisabled();
    unmount();

    live();
    render(<CommandButton id="test.button.no" />);
    expect(screen.getByRole("button", { name: "No" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "No" }));
    expect(run).not.toHaveBeenCalled();
  });

  it("stays on while the snapshot is stale for an offline command", () => {
    defineCommand({
      id: "test.button.offline",
      title: "Offline",
      offline: true,
      when: () => true,
      run: () => Promise.resolve(),
    });

    render(<CommandButton id="test.button.offline" />);

    expect(screen.getByRole("button", { name: "Offline" })).toBeEnabled();
  });

  it("stays off while its command runs, so a double tap runs it once", async () => {
    live();
    let finish: () => void = () => {};
    const run = vi.fn(
      () =>
        new Promise<void>((resolve) => {
          finish = resolve;
        }),
    );
    defineCommand({ id: "test.button.busy", title: "Busy", when: () => true, run });

    render(<CommandButton id="test.button.busy" />);
    const button = screen.getByRole("button", { name: "Busy" });
    await userEvent.click(button);
    await userEvent.click(button);

    expect(run).toHaveBeenCalledOnce();
    expect(button).toBeDisabled();

    act(() => {
      finish();
    });
    await waitFor(() => {
      expect(button).toBeEnabled();
    });
  });

  it("opens the sheet for a command with a confirm, and runs it only on the action", async () => {
    live();
    const run = vi.fn<(ctx: CommandContext, name: string) => Promise<void>>(() =>
      Promise.resolve(),
    );
    defineCommand<string>({
      id: "test.button.confirm",
      title: "Kill",
      when: () => true,
      confirm: (_ctx, name) => ({
        title: `Kill ${name}`,
        body: "Removes the worktree.",
        action: "Kill it",
        destructive: true,
      }),
      run,
    });

    render(
      <>
        <ConfirmSheet />
        <CommandButton id="test.button.confirm" args="x" />
      </>,
    );

    await userEvent.click(screen.getByRole("button", { name: "Kill" }));
    expect(await screen.findByText("Kill x")).toBeInTheDocument();
    expect(run).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(screen.queryByText("Kill x")).toBeNull();
    });
    expect(run).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "Kill" }));
    await userEvent.click(await screen.findByRole("button", { name: "Kill it" }));
    await waitFor(() => {
      expect(run).toHaveBeenCalledOnce();
    });
    expect(run.mock.calls[0]?.[1]).toBe("x");
  });

  it("fails loudly for a command nothing defined", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      expect(() => render(<CommandButton id="test.button.nothing" />)).toThrow("unknown command");
    } finally {
      spy.mockRestore();
    }
  });
});
