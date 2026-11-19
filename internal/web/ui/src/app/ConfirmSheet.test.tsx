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
function ask(spec: ConfirmSpec): Promise<string | null> {
  let answer = Promise.resolve<string | null>(null);
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

  it("names what the command does, and resolves the empty value on its action when it has no field", async () => {
    render(<ConfirmSheet />);
    const answer = ask(kill);

    expect(await screen.findByText("Kill session")).toBeInTheDocument();
    expect(screen.getByText(/Removes the worktree \/w\/x/)).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Kill" }));
    expect(await answer).toBe("");
    await waitFor(() => {
      expect(screen.queryByText("Kill session")).toBeNull();
    });
  });

  it("resolves null on Cancel", async () => {
    render(<ConfirmSheet />);
    const answer = ask(kill);

    await userEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    expect(await answer).toBeNull();
  });

  it("resolves null on Escape", async () => {
    render(<ConfirmSheet />);
    const answer = ask(kill);
    await screen.findByText("Kill session");

    await userEvent.keyboard("{Escape}");
    expect(await answer).toBeNull();
  });

  it("lets a newer ask replace an open one, which resolves null", async () => {
    render(<ConfirmSheet />);
    const first = ask(kill);
    await screen.findByText("Kill session");

    const second = ask({
      title: "Push",
      body: "Pushes the branch.",
      action: "Push",
      destructive: false,
    });

    expect(await first).toBeNull();
    expect(await screen.findByText("Pushes the branch.")).toBeInTheDocument();
    expect(screen.queryByText("Kill session")).toBeNull();

    await userEvent.click(screen.getByRole("button", { name: "Push" }));
    expect(await second).toBe("");
  });
});

// textSpec is a sheet with a text field whose limit is max bytes.
function textSpec(max: number): ConfirmSpec {
  return {
    title: "Deny this?",
    body: "Claude is told no.",
    action: "Deny",
    destructive: true,
    field: {
      kind: "text",
      label: "Tell Claude why",
      placeholder: "optional",
      max,
      tooLong: "That is over the limit.",
    },
  };
}

const merge: ConfirmSpec = {
  title: "Merge",
  body: "Merges one session into this one.",
  action: "Merge",
  destructive: false,
  field: {
    kind: "choice",
    label: "Merge from",
    options: [
      { value: "4", label: "fix-a (aidanb/fix-a)" },
      { value: "9", label: "fix-b (aidanb/fix-b)" },
    ],
  },
};

describe("ConfirmSheet's field", () => {
  it("a text field shows its label and placeholder and resolves what was typed, as typed", async () => {
    render(<ConfirmSheet />);
    const answer = ask(textSpec(100));

    const field = await screen.findByLabelText("Tell Claude why");
    expect(field).toHaveAttribute("placeholder", "optional");
    await userEvent.type(field, "a b ");
    await userEvent.click(screen.getByRole("button", { name: "Deny" }));

    expect(await answer).toBe("a b ");
  });

  it("a text field left empty resolves the empty string, which is an answer and not a cancel", async () => {
    render(<ConfirmSheet />);
    const answer = ask(textSpec(100));

    await userEvent.click(await screen.findByRole("button", { name: "Deny" }));

    expect(await answer).toBe("");
  });

  it("a text field counts UTF-8 bytes: at the limit the action is on, one byte past it the action is off and the too-long line shows", async () => {
    render(<ConfirmSheet />);
    const answer = ask(textSpec(4));
    const field = await screen.findByLabelText("Tell Claude why");

    await userEvent.type(field, "éé");
    expect(screen.getByRole("button", { name: "Deny" })).toBeEnabled();
    expect(screen.queryByText("That is over the limit.")).toBeNull();

    await userEvent.type(field, "a");
    expect(screen.getByRole("button", { name: "Deny" })).toBeDisabled();
    expect(screen.getByText("That is over the limit.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Deny" }));

    await userEvent.type(field, "{Backspace}");
    expect(screen.getByRole("button", { name: "Deny" })).toBeEnabled();
    expect(screen.queryByText("That is over the limit.")).toBeNull();

    // The disabled click resolved nothing: the sheet is still open to cancel.
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(await answer).toBeNull();
  });

  it("cancelling with text typed resolves null, not the text", async () => {
    render(<ConfirmSheet />);
    const answer = ask(textSpec(100));

    await userEvent.type(await screen.findByLabelText("Tell Claude why"), "abc");
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(await answer).toBeNull();
  });

  it("a choice field is a native select with its first option chosen, and resolves that option's value", async () => {
    render(<ConfirmSheet />);
    const answer = ask(merge);

    const select = await screen.findByLabelText("Merge from");
    expect(select.tagName).toBe("SELECT");
    expect(select).toHaveValue("4");
    expect(screen.getByRole("option", { name: "fix-b (aidanb/fix-b)" })).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Merge" }));

    expect(await answer).toBe("4");
  });

  it("a choice field resolves the option that was picked", async () => {
    render(<ConfirmSheet />);
    const answer = ask(merge);

    await userEvent.selectOptions(await screen.findByLabelText("Merge from"), "9");
    await userEvent.click(screen.getByRole("button", { name: "Merge" }));

    expect(await answer).toBe("9");
  });

  it("a newer ask starts with a field of its own, not the text typed into the one it replaced", async () => {
    render(<ConfirmSheet />);
    const first = ask(textSpec(100));
    await userEvent.type(await screen.findByLabelText("Tell Claude why"), "old");

    const second = ask({ ...textSpec(100), title: "Chat about this" });

    expect(await screen.findByText("Chat about this")).toBeInTheDocument();
    expect(screen.getByLabelText("Tell Claude why")).toHaveValue("");
    expect(await first).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Deny" }));
    expect(await second).toBe("");
  });

  it("a choice field with no options leaves the action off, with Cancel still open", async () => {
    render(<ConfirmSheet />);
    const answer = ask({ ...merge, field: { kind: "choice", label: "Merge from", options: [] } });

    expect(await screen.findByLabelText("Merge from")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Merge" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(await answer).toBeNull();
  });

  it("a sheet with no field draws no input", async () => {
    render(<ConfirmSheet />);
    const answer = ask(kill);
    await screen.findByText("Kill session");

    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByRole("combobox")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(await answer).toBeNull();
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

  it("is off while the caller's disabled is set, and a caller's false does not turn on what the command refuses", async () => {
    live();
    const run = vi.fn(() => Promise.resolve());
    defineCommand({ id: "test.button.callerOff", title: "Send", when: () => true, run });
    defineCommand({ id: "test.button.callerNo", title: "Never", when: () => false, run });

    const { rerender } = render(<CommandButton id="test.button.callerOff" disabled />);
    const button = screen.getByRole("button", { name: "Send" });
    expect(button).toBeDisabled();
    await userEvent.click(button);
    expect(run).not.toHaveBeenCalled();

    rerender(<CommandButton id="test.button.callerOff" disabled={false} />);
    expect(screen.getByRole("button", { name: "Send" })).toBeEnabled();
    rerender(<CommandButton id="test.button.callerOff" />);
    expect(screen.getByRole("button", { name: "Send" })).toBeEnabled();

    rerender(<CommandButton id="test.button.callerNo" disabled={false} />);
    expect(screen.getByRole("button", { name: "Never" })).toBeDisabled();
  });

  it("goes off only for its own key while a keyed command runs", async () => {
    live();
    const finishers: (() => void)[] = [];
    const run = vi.fn<(ctx: CommandContext, job: number) => Promise<void>>(
      () =>
        new Promise<void>((resolve) => {
          finishers.push(resolve);
        }),
    );
    defineCommand<number>({
      id: "test.button.keyed",
      title: "Park",
      when: () => true,
      key: (job) => `job${job}`,
      run,
    });

    render(
      <>
        <CommandButton id="test.button.keyed" args={1} label="Park 1" />
        <CommandButton id="test.button.keyed" args={2} label="Park 2" />
      </>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Park 1" }));

    expect(screen.getByRole("button", { name: "Park 1" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Park 2" })).toBeEnabled();
    await userEvent.click(screen.getByRole("button", { name: "Park 2" }));
    expect(run).toHaveBeenCalledTimes(2);

    act(() => {
      for (const finish of finishers) finish();
    });
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Park 1" })).toBeEnabled();
    });
  });

  it("draws and runs a keyed command given no args: the throwing key is no key, not a crash or an unhandled rejection", async () => {
    live();
    const run = vi.fn<(ctx: CommandContext, job: number) => Promise<void>>(() =>
      Promise.reject(new Error("no job")),
    );
    defineCommand<number>({
      id: "test.button.keyNoArgs",
      title: "Park",
      when: () => true,
      key: (job) => `job${job.toFixed()}`,
      run,
    });

    render(<CommandButton id="test.button.keyNoArgs" />);
    const button = screen.getByRole("button", { name: "Park" });
    expect(button).toBeEnabled();
    await userEvent.click(button);

    await waitFor(() => {
      expect(run).toHaveBeenCalledOnce();
    });
  });

  it("gives run the value typed into the sheet's field, and runs with an empty input when the confirm returns null", async () => {
    live();
    const run = vi.fn<(ctx: CommandContext, id: string, input: string) => Promise<void>>(() =>
      Promise.resolve(),
    );
    defineCommand<string>({
      id: "test.button.field",
      title: "Deny",
      when: () => true,
      confirm: (_ctx, id) =>
        id === "skip"
          ? null
          : {
              title: "Deny this?",
              body: "Claude is told no.",
              action: "Deny it",
              destructive: true,
              field: {
                kind: "text",
                label: "Tell Claude why",
                placeholder: "",
                max: 100,
                tooLong: "too long",
              },
            },
      run,
    });

    const { rerender } = render(
      <>
        <ConfirmSheet />
        <CommandButton id="test.button.field" args="ask-1" />
      </>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Deny" }));
    await userEvent.type(await screen.findByLabelText("Tell Claude why"), "not now");
    await userEvent.click(screen.getByRole("button", { name: "Deny it" }));
    await waitFor(() => {
      expect(run).toHaveBeenCalledOnce();
    });
    expect(run.mock.calls[0]?.[1]).toBe("ask-1");
    expect(run.mock.calls[0]?.[2]).toBe("not now");
    await waitFor(() => {
      expect(screen.queryByText("Deny this?")).toBeNull();
    });

    rerender(
      <>
        <ConfirmSheet />
        <CommandButton id="test.button.field" args="skip" />
      </>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Deny" }));
    await waitFor(() => {
      expect(run).toHaveBeenCalledTimes(2);
    });
    expect(run.mock.calls[1]?.[2]).toBe("");
    expect(screen.queryByText("Deny this?")).toBeNull();
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
