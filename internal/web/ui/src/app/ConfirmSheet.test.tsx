import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

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
