import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { MdNode } from "@/api/gen";
import { fixtureTurns } from "@/fixtures";
import { Markdown } from "./Markdown";

const text = (v: string): MdNode => ({ t: "text", v });
const p = (...c: (MdNode | null)[]): MdNode => ({ t: "p", c });

describe("Markdown", () => {
  it("Markdown: the fixture's tree draws each node as its element", () => {
    const items = (fixtureTurns().turns ?? []).flatMap((t) => t.items ?? []);
    const item = items.find((i) => i.kind === "text" && i.blocks?.[0]?.t === "h");
    const { container } = render(<Markdown blocks={item?.blocks ?? []} />);
    expect(screen.getByRole("heading", { level: 3, name: "Findings" })).toBeInTheDocument();
    expect(container.querySelector("em")).not.toBeNull();
    expect(container.querySelector("strong")).not.toBeNull();
    expect(screen.getByRole("link")).toBeInTheDocument();
    expect(container.querySelectorAll("ul > li")).toHaveLength(2);
    const ol = container.querySelector("ol");
    expect(ol).toHaveAttribute("start", "3");
    expect(within(ol as HTMLElement).getAllByRole("listitem")).toHaveLength(2);
    expect(container.querySelector("blockquote")).not.toBeNull();
    expect(container.querySelector("pre > code")).toHaveTextContent("p.drawCursor(frame)");
    expect(container.querySelector("hr")).not.toBeNull();
    expect(container.querySelector("table")).not.toBeNull();
    expect(container.querySelectorAll("tr")).toHaveLength(2);
    expect(container.querySelectorAll("th")).toHaveLength(2);
    expect(container.querySelectorAll("td")).toHaveLength(2);
  });

  it("Markdown: a heading of level 3 or more is an h4, and levels 1 and 2 are h3", () => {
    const blocks = [1, 2, 3, 6].map((level): MdNode => ({ t: "h", level, c: [text("x")] }));
    const { container } = render(<Markdown blocks={blocks} />);
    expect(container.querySelectorAll("h3")).toHaveLength(2);
    expect(container.querySelectorAll("h4")).toHaveLength(2);
  });

  it("Markdown: an ordered list without a start has no start attribute", () => {
    const { container } = render(
      <Markdown blocks={[{ t: "ol", c: [{ t: "li", c: [text("a")] }] }]} />,
    );
    expect(container.querySelector("ol")).not.toHaveAttribute("start");
  });

  it("Markdown: the inline kinds draw their elements", () => {
    const { container } = render(
      <Markdown
        blocks={[
          p(
            text("a"),
            { t: "br" },
            { t: "del", c: [text("gone")] },
            { t: "code_inline", v: "x()" },
          ),
        ]}
      />,
    );
    expect(container.querySelector("br")).not.toBeNull();
    expect(container.querySelector("del")).toHaveTextContent("gone");
    expect(container.querySelector("code")).toHaveTextContent("x()");
  });

  it("Markdown: a table cell takes its column's alignment as a class and no style attribute", () => {
    const cell = (t: string, v: string): MdNode => ({ t, c: [text(v)] });
    const table: MdNode = {
      t: "table",
      align: ["left", "right", "center"],
      c: [{ t: "tr", c: [cell("td", "a"), cell("td", "b"), cell("td", "c"), cell("td", "d")] }],
    };
    const { container } = render(<Markdown blocks={[table]} />);
    const cells = Array.from(container.querySelectorAll("td"));
    expect(cells[0]).toHaveClass("text-left");
    expect(cells[1]).toHaveClass("text-right");
    expect(cells[2]).toHaveClass("text-center");
    for (const c of ["text-left", "text-right", "text-center"]) expect(cells[3]).not.toHaveClass(c);
    expect(container.querySelectorAll("[style]")).toHaveLength(0);
  });

  it("Markdown: a link opens in a new tab and carries noopener noreferrer", () => {
    const { container } = render(
      <Markdown
        blocks={[
          p({ t: "a", href: "https://example.com/x", c: [text("go")] }),
          p({ t: "a", c: [text("plain")] }),
        ]}
      />,
    );
    const a = screen.getByRole("link", { name: "go" });
    expect(a).toHaveAttribute("href", "https://example.com/x");
    expect(a).toHaveAttribute("target", "_blank");
    expect(a).toHaveAttribute("rel", "noopener noreferrer");
    expect(container.querySelectorAll("a")).toHaveLength(1);
    const span = screen.getByText("plain");
    expect(span.tagName).toBe("SPAN");
    expect(span).not.toHaveAttribute("href");
  });

  it("Markdown: an unknown node is a span of its children", () => {
    const { container } = render(<Markdown blocks={[{ t: "mystery", c: [text("inside")] }]} />);
    expect(screen.getByText("inside").tagName).toBe("SPAN");
    expect(container.querySelector("mystery")).toBeNull();
  });

  it("Markdown: a null node draws nothing and its siblings still draw", () => {
    const { container } = render(<Markdown blocks={[null, p(text("kept")), null]} />);
    expect(screen.getByText("kept")).toBeInTheDocument();
    expect(container.firstElementChild?.children).toHaveLength(1);
    render(<Markdown blocks={[p(null, text("x"))]} />);
    expect(screen.getByText("x")).toBeInTheDocument();
  });

  it("Markdown: text holding markup renders as text", () => {
    const script = "<script>alert(1)</script><img src=x onerror=alert(1)>";
    const { container } = render(
      <Markdown
        blocks={[
          p(text(script)),
          { t: "code", v: "<b>bold</b>" },
          p({ t: "a", href: "https://example.com", c: [text("<i>x</i>")] }),
        ]}
      />,
    );
    expect(container.textContent).toContain(script);
    expect(container.textContent).toContain("<b>bold</b>");
    expect(container.textContent).toContain("<i>x</i>");
    expect(container.querySelector("script, img, b, i")).toBeNull();
  });

  it("Markdown: nothing carries a style attribute", () => {
    const items = (fixtureTurns().turns ?? []).flatMap((t) => t.items ?? []);
    const item = items.find((i) => i.kind === "text" && i.blocks?.[0]?.t === "h");
    const { container } = render(<Markdown blocks={item?.blocks ?? []} />);
    expect(container.querySelectorAll("[style]")).toHaveLength(0);
  });
});
