// The changes panel's body, a port of the classic Changes: the sub-line, then a fold per changed
// file with its hunks, and the reply box. Every line of a diff is drawn as a text node.
import { useState, type ReactElement } from "react";

import type { File, Hunk, Patch, Session } from "@/api/gen";
import { usePatch } from "@/api/reads";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { ReplyBox } from "@/conversation/ReplyBox";
import { cn } from "@/lib/utils";
import { SubLine } from "@/session/SubLine";

// statusTone is the colour of a file's status letter; a letter this build does not know is muted.
function statusTone(status: string): string {
  switch (status) {
    case "A":
      return "text-working";
    case "D":
      return "text-error";
    case "R":
    case "C":
      return "text-turn";
    default:
      return "text-muted-foreground";
  }
}

// splitPath is a path's name (after its last "/") and its directory (up to and including it, or
// "./" for a path with none).
function splitPath(path: string): { name: string; dir: string } {
  const at = path.lastIndexOf("/");
  if (at < 0) return { name: path, dir: "./" };
  return { name: path.slice(at + 1), dir: path.slice(0, at + 1) };
}

function Counts(props: { added: number; removed: number }): ReactElement {
  const { added, removed } = props;
  return (
    <span className="flex-none font-mono text-xs">
      {added > 0 ? <span className="text-working">+{added}</span> : null}
      {added > 0 && removed > 0 ? " " : null}
      {removed > 0 ? <span className="text-error">−{removed}</span> : null}
    </span>
  );
}

// reasonFor is why a file has no hunks to draw, "" when it has some. The daemon's own bounds
// (5,000 lines a file, 1 MiB in all) come as `omitted`; a value from a newer daemon is drawn as sent.
function reasonFor(file: File): string {
  if (file.omitted === "lines") {
    return `Too long to show here (${file.added + file.removed} changed lines).`;
  }
  if (file.omitted === "budget") return "Not shown: the diff is over 1 MiB.";
  // `omitted` is typed to the values this build knows; a newer daemon may send another.
  const other: string = file.omitted;
  if (other !== "") return `Not shown (${other}).`;
  if (file.binary) return "Binary file.";
  if ((file.hunks ?? []).length === 0) return "No text changes.";
  return "";
}

// lineTone is a diff line's row class by its first character.
function lineTone(line: string): string {
  switch (line[0]) {
    case "+":
      return "bg-diff-add";
    case "-":
      return "bg-diff-del";
    case "\\":
      return "text-muted-foreground";
    default:
      return "";
  }
}

const ROW = "min-h-5 whitespace-pre px-2";

function HunkView(props: { hunk: Hunk }): ReactElement {
  const { hunk } = props;
  const header = /^(@@[^@]*@@)(.*)$/.exec(hunk.header);
  const cut = new Set(hunk.cut ?? []);
  return (
    <>
      <div className={ROW}>
        {header === null ? (
          hunk.header
        ) : (
          <>
            <span className="text-turn">{header[1]}</span>
            <span className="text-muted-foreground">{header[2]}</span>
          </>
        )}
      </div>
      {(hunk.lines ?? []).map((line, i) => (
        <div key={i} className={cn(ROW, lineTone(line))}>
          {line}
          {cut.has(i) ? <span className="text-muted-foreground">…</span> : null}
        </div>
      ))}
    </>
  );
}

function FileBody(props: { file: File }): ReactElement {
  const { file } = props;
  const reason = reasonFor(file);
  if (reason !== "") return <p className="px-1 py-2 text-xs text-muted-foreground">{reason}</p>;
  return (
    <div className="overflow-x-auto rounded-lg border border-border bg-card font-mono text-[12px] leading-5">
      <div className="w-max min-w-full">
        {(file.hunks ?? []).map((hunk, i) => (
          <HunkView key={i} hunk={hunk} />
        ))}
      </div>
    </div>
  );
}

function FileFold(props: {
  file: File;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}): ReactElement {
  const { file, open, onOpenChange } = props;
  const { name, dir } = splitPath(file.path);
  const copied = (file.status === "R" || file.status === "C") && file.old_path !== "";
  return (
    <Collapsible open={open} onOpenChange={onOpenChange}>
      <CollapsibleTrigger className="flex min-h-11 w-full items-center gap-2 text-left">
        <span className={cn("font-mono font-bold", statusTone(file.status))}>{file.status}</span>
        <span className="min-w-0 flex-1">
          <span className="font-mono">{name}</span>{" "}
          <span className="truncate text-xs text-muted-foreground">
            {copied ? `from ${file.old_path}` : dir}
          </span>
        </span>
        {file.binary ? (
          <span className="flex-none text-xs text-muted-foreground">binary</span>
        ) : (
          <Counts added={file.added} removed={file.removed} />
        )}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <FileBody file={file} />
      </CollapsibleContent>
    </Collapsible>
  );
}

function sums(files: File[]): { added: number; removed: number } {
  let added = 0;
  let removed = 0;
  for (const file of files) {
    added += file.added;
    removed += file.removed;
  }
  return { added, removed };
}

function PatchLine(props: { patch: Patch }): ReactElement {
  const files = props.patch.files ?? [];
  const { added, removed } = sums(files);
  return (
    <div className="flex flex-wrap gap-x-3 px-4 py-2 text-xs text-muted-foreground">
      <span>{files.length === 1 ? "1 file" : `${files.length} files`}</span>
      <span>
        <span className="text-working">+{added}</span>{" "}
        <span className="text-error">−{removed}</span>
      </span>
      {props.patch.base === "" ? null : <span>since {props.patch.base.slice(0, 7)}</span>}
    </div>
  );
}

// ChangesMark is the tab's mark: the session's line counts while either is above 0.
export function ChangesMark(props: { session: Session }): ReactElement {
  const { added, removed } = props.session.diff;
  if (added <= 0 && removed <= 0) return <></>;
  return (
    <span className="ml-1.5 font-mono text-xs">
      <span className="text-working">+{added}</span> <span className="text-error">−{removed}</span>
    </span>
  );
}

// Changes is a session's diff: read through usePatch, a fold per file, the open set kept across
// refreshes.
export function Changes(props: { session: Session }): ReactElement {
  const { session } = props;
  const { patch, error, reload } = usePatch({
    session: session.id,
    signature: `${session.status}:${session.diff.added}:${session.diff.removed}`,
    live: session.status === "Running",
  });
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set());
  const [seeded, setSeeded] = useState(false);
  if (!seeded && patch !== undefined) {
    setSeeded(true);
    const first = patch.files?.[0];
    if (patch.files?.length === 1 && first !== undefined) setOpen(new Set([first.path]));
  }
  const files = patch?.files ?? [];
  const setFile = (path: string, on: boolean): void => {
    const next = new Set(open);
    if (on) next.add(path);
    else next.delete(path);
    setOpen(next);
  };
  return (
    <div className="flex flex-col">
      {patch === undefined ? <SubLine session={session} /> : <PatchLine patch={patch} />}
      <div className="flex flex-col gap-3 px-4 pb-6">
        {patch === undefined ? (
          error === "" ? (
            <p className="text-muted-foreground">Loading…</p>
          ) : (
            <div className="flex flex-col items-start gap-2">
              <p className="text-error">{error}</p>
              <Button variant="secondary" onClick={reload}>
                retry
              </Button>
            </div>
          )
        ) : files.length === 0 ? (
          <p className="text-center text-muted-foreground">
            {patch.base === "" ? "No changes." : `No changes since ${patch.base.slice(0, 7)}.`}
          </p>
        ) : (
          files.map((file, i) => (
            <FileFold
              key={`${i}:${file.path}`}
              file={file}
              open={open.has(file.path)}
              onOpenChange={(on) => {
                setFile(file.path, on);
              }}
            />
          ))
        )}
        {patch !== undefined && error !== "" ? (
          <p className="text-xs text-muted-foreground">
            Could not refresh: {error} ·{" "}
            <button type="button" onClick={reload}>
              retry
            </button>
          </p>
        ) : null}
        <ReplyBox session={session} />
      </div>
    </div>
  );
}
