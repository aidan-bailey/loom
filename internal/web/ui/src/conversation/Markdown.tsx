import type { ReactElement, ReactNode } from "react";
import type { MdNode } from "@/api/gen";
import { cn } from "@/lib/utils";

const ALIGN: Record<string, string> = {
  left: "text-left",
  right: "text-right",
  center: "text-center",
};

function kids(node: MdNode): ReactNode {
  return (node.c ?? []).map((child, i) => <Node key={i} node={child} />);
}

function Table({ node }: { node: MdNode }): ReactElement {
  const align = node.align ?? [];
  const rows = (node.c ?? []).map((row, i) =>
    row === null ? null : (
      <tr key={i}>
        {(row.c ?? []).map((cell, k) => {
          if (cell === null) return null;
          const cls = cn("border border-border px-2 py-1", ALIGN[align[k] ?? ""]);
          return cell.t === "th" ? (
            <th key={k} className={cls}>
              {kids(cell)}
            </th>
          ) : (
            <td key={k} className={cls}>
              {kids(cell)}
            </td>
          );
        })}
      </tr>
    ),
  );
  return (
    <div className="my-1.5 overflow-x-auto">
      <table className="border-collapse text-[13px]">
        <tbody>{rows}</tbody>
      </table>
    </div>
  );
}

function Node({ node }: { node: MdNode | null }): ReactElement | null {
  if (node === null) return null;
  switch (node.t) {
    case "text":
      return <>{node.v}</>;
    case "br":
      return <br />;
    case "p":
      return <p className="my-1.5">{kids(node)}</p>;
    case "ul":
      return <ul className="my-1.5 list-disc pl-5">{kids(node)}</ul>;
    case "ol":
      return (
        <ol
          className="my-1.5 list-decimal pl-5"
          start={typeof node.start === "number" ? node.start : undefined}
        >
          {kids(node)}
        </ol>
      );
    case "li":
      return <li>{kids(node)}</li>;
    case "quote":
      return (
        <blockquote className="my-1.5 border-l-2 border-border pl-3 text-muted-foreground">
          {kids(node)}
        </blockquote>
      );
    case "hr":
      return <hr className="my-2 border-border" />;
    case "em":
      return <em>{kids(node)}</em>;
    case "strong":
      return <strong>{kids(node)}</strong>;
    case "del":
      return <del>{kids(node)}</del>;
    case "code_inline":
      return <code className="font-mono text-[0.92em]">{node.v}</code>;
    case "h":
      return node.level === 1 || node.level === 2 ? (
        <h3 className="mt-2 text-[15px] font-semibold">{kids(node)}</h3>
      ) : (
        <h4 className="mt-2 text-sm font-semibold">{kids(node)}</h4>
      );
    case "code":
      return (
        <pre className="my-1.5 overflow-x-auto rounded-md bg-card p-2 font-mono text-[12.5px]">
          <code>{node.v}</code>
        </pre>
      );
    case "a":
      return typeof node.href === "string" && node.href !== "" ? (
        <a
          className="text-turn underline"
          href={node.href}
          target="_blank"
          rel="noopener noreferrer"
        >
          {kids(node)}
        </a>
      ) : (
        <span>{kids(node)}</span>
      );
    case "table":
      return <Table node={node} />;
    default:
      return <span>{kids(node)}</span>;
  }
}

/**
 * Markdown draws Claude's text from the tree the daemon builds, as elements and text nodes. It
 * never sets an element's inner HTML and never builds a string of markup: Claude's text can hold
 * markup, and a string put into the page would render it (links and forms included; the CSP stops
 * only its scripts). The link check is the daemon's (`safeHref`); this adds none.
 */
export function Markdown(props: { blocks: (MdNode | null)[] }): ReactElement {
  return (
    <div>
      {props.blocks.map((block, i) => (
        <Node key={i} node={block} />
      ))}
    </div>
  );
}
