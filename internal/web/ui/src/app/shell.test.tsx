import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { definePanel, type PanelProps } from "./panels";
import { PanelHost, Placeholder } from "./shell";
import { switchApp } from "./switchApp";

vi.mock("./switchApp", () => ({ switchApp: vi.fn(() => Promise.resolve("switched")) }));

const props: PanelProps = { sessionId: 5, params: { id: "5" }, search: {} };

afterEach(() => {
  vi.mocked(switchApp).mockClear();
});

describe("Placeholder", () => {
  it("names the screen, links back to the fleet and offers the classic app", async () => {
    render(<Placeholder panel="flows" />);

    expect(screen.getByRole("heading", { name: "Flows" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /fleet/ })).toHaveAttribute("href", "#/");

    await userEvent.click(screen.getByRole("button", { name: "Back to classic" }));
    expect(switchApp).toHaveBeenCalledWith("classic");
  });
});

describe("PanelHost", () => {
  it("draws the placeholder for a panel nothing registered", () => {
    render(<PanelHost panel="flowDoc" props={props} />);
    expect(screen.getByRole("heading", { name: "Flow document" })).toBeInTheDocument();
  });

  it("draws a registered panel's body with its props", () => {
    definePanel({
      id: "info",
      title: () => "Info",
      Body: (p: PanelProps) => <p>session {p.sessionId}</p>,
    });
    render(<PanelHost panel="info" props={props} />);
    expect(screen.getByText("session 5")).toBeInTheDocument();
  });

  it("shows what failed and a reload for a panel that throws, and recovers on another route", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    try {
      definePanel({
        id: "changes",
        title: () => "Changes",
        Body: (p: PanelProps) => {
          if (p.params.id === "1") throw new Error("boom");
          return <p>fine</p>;
        },
      });
      const { rerender } = render(
        <PanelHost panel="changes" props={{ sessionId: 1, params: { id: "1" }, search: {} }} />,
      );

      const alert = screen.getByRole("alert");
      expect(alert).toHaveTextContent("Changes failed to draw");
      expect(alert).toHaveTextContent("boom");
      expect(screen.getByRole("button", { name: "Reload" })).toBeInTheDocument();

      // Another session's changes is another route: the boundary starts over.
      rerender(
        <PanelHost panel="changes" props={{ sessionId: 2, params: { id: "2" }, search: {} }} />,
      );
      expect(screen.getByText("fine")).toBeInTheDocument();
      expect(screen.queryByRole("alert")).toBeNull();
    } finally {
      spy.mockRestore();
    }
  });
});
