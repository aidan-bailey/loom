// The new-session form: a port of the classic screen (newsession.mjs) onto Select, Input and the
// session.create command. It is a lazy chunk: a first launch never loads it.
import { useState, type KeyboardEvent, type ReactElement } from "react";

import type { LaunchDefaults, Workspace } from "@/api/gen";
import { useLaunchDefaults } from "@/api/reads";
import { commandById, runCommand, useCommandContext } from "@/app/commands";
import { CommandButton } from "@/app/CommandButton";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { launchBody, type LaunchPicks } from "@/commands/launch";
import { MAX_MESSAGE_BYTES, utf8Length } from "@/lib/utf8";
import { FILTER_KEY } from "@/panels/fleet";
import { useFleet, useLink } from "@/store/fleet";

const LABEL = "text-xs font-semibold tracking-wide text-muted-foreground uppercase";

interface Option {
  value: string;
  label: string;
}

// storedFilter is the workspace ID the fleet's filter holds, "" for none: the fleet keeps it in
// localStorage as a JSON string, and anything else (a throw included) is none.
function storedFilter(): string {
  try {
    const value: unknown = JSON.parse(window.localStorage.getItem(FILTER_KEY) ?? '""');
    return typeof value === "string" ? value : "";
  } catch {
    return "";
  }
}

// firstWorkspace is the form's first choice: the fleet's filter, else the first shown workspace,
// else the first one, else none ("").
function firstWorkspace(workspaces: Workspace[]): string {
  const filter = storedFilter();
  const named = workspaces.find((w) => String(w.id) === filter);
  const chosen = named ?? workspaces.find((w) => w.shown !== false) ?? workspaces[0];
  return chosen === undefined ? "" : String(chosen.id);
}

function workspaceOption(w: Workspace): Option {
  return { value: String(w.id), label: w.label !== "" ? w.label : w.name };
}

// optionsOf is a list with the default put first when the list lacks it; "" reads "(default)".
function optionsOf(list: string[] | null, fallback: string): Option[] {
  const values = list ?? [];
  const all = values.includes(fallback) ? values : [fallback, ...values];
  return all.map((value) => ({ value, label: value === "" ? "(default)" : value }));
}

function Field({
  label,
  htmlFor,
  children,
}: {
  label: string;
  htmlFor?: string;
  children: ReactElement | ReactElement[];
}): ReactElement {
  return (
    <div className="flex flex-col gap-1.5">
      {htmlFor === undefined ? (
        <span className={LABEL}>{label}</span>
      ) : (
        <label htmlFor={htmlFor} className={LABEL}>
          {label}
        </label>
      )}
      {children}
    </div>
  );
}

function Picker({
  label,
  value,
  options,
  onChange,
  children,
}: {
  label: string;
  value: string;
  options: Option[];
  onChange: (value: string) => void;
  children?: ReactElement | ReactElement[];
}): ReactElement {
  return (
    <Field label={label}>
      <Select items={options} value={value} onValueChange={(v) => onChange(v ?? "")}>
        <SelectTrigger aria-label={label}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {children ??
            options.map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {o.label}
              </SelectItem>
            ))}
        </SelectContent>
      </Select>
    </Field>
  );
}

function WorkspacePicker({
  workspaces,
  value,
  onChange,
}: {
  workspaces: Workspace[];
  value: string;
  onChange: (value: string) => void;
}): ReactElement {
  const item = (w: Workspace): ReactElement => {
    const o = workspaceOption(w);
    return (
      <SelectItem key={o.value} value={o.value}>
        {o.label}
      </SelectItem>
    );
  };
  const grouped = workspaces.some((w) => w.shown === false);
  return (
    <Picker
      label="Workspace"
      value={value}
      options={workspaces.map(workspaceOption)}
      onChange={onChange}
    >
      {grouped
        ? [
            <SelectGroup key="active">
              <SelectLabel>Active</SelectLabel>
              {workspaces.filter((w) => w.shown !== false).map(item)}
            </SelectGroup>,
            <SelectGroup key="other">
              <SelectLabel>Other workspaces</SelectLabel>
              {workspaces.filter((w) => w.shown === false).map(item)}
            </SelectGroup>,
          ]
        : workspaces.map(item)}
    </Picker>
  );
}

function LaunchOptions({
  defaults,
  picks,
  onPick,
}: {
  defaults: LaunchDefaults;
  picks: LaunchPicks;
  onPick: (key: keyof LaunchPicks, value: string) => void;
}): ReactElement {
  return (
    <Collapsible>
      <CollapsibleTrigger className="flex min-h-11 w-full items-center text-sm text-muted-foreground">
        Launch options
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="flex flex-col gap-4 pt-2">
          {defaults.accounts !== null && defaults.accounts.length > 0 ? (
            <Picker
              label="Account"
              value={picks.account}
              options={optionsOf(defaults.accounts, defaults.account)}
              onChange={(v) => onPick("account", v)}
            />
          ) : null}
          <Picker
            label="Model"
            value={picks.model}
            options={optionsOf(defaults.models, defaults.model)}
            onChange={(v) => onPick("model", v)}
          />
          <Picker
            label="Permission mode"
            value={picks.permission_mode}
            options={optionsOf(defaults.permission_modes, defaults.permission_mode)}
            onChange={(v) => onPick("permission_mode", v)}
          />
          <Picker
            label="Effort"
            value={picks.effort}
            options={optionsOf(defaults.efforts, defaults.effort)}
            onChange={(v) => onPick("effort", v)}
          />
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

function Form({ workspaces }: { workspaces: Workspace[] }): ReactElement {
  const [workspace, setWorkspace] = useState(() => firstWorkspace(workspaces));
  const [title, setTitle] = useState("");
  const [prompt, setPrompt] = useState("");
  // The user's picks on top of the defaults; a workspace change clears them. Nothing else does:
  // the defaults are held for the page's lifetime, so a stream that drops and returns keeps them.
  const [overrides, setOverrides] = useState<Partial<LaunchPicks>>({});
  const live = useLink().state === "live";
  const ctx = useCommandContext();
  const { defaults, error, retry } = useLaunchDefaults(
    workspace === "" ? undefined : Number(workspace),
    live,
  );

  const tooLong = utf8Length(prompt.trimEnd()) > MAX_MESSAGE_BYTES;
  const listed = workspaces.some((w) => String(w.id) === workspace);
  const ready = defaults !== undefined && listed && title.trim() !== "" && !tooLong;
  const picks: LaunchPicks | undefined =
    defaults === undefined
      ? undefined
      : {
          account: overrides.account ?? defaults.account,
          model: overrides.model ?? defaults.model,
          permission_mode: overrides.permission_mode ?? defaults.permission_mode,
          effort: overrides.effort ?? defaults.effort,
        };
  const body =
    ready && picks !== undefined
      ? launchBody({ workspace: Number(workspace), title, prompt, picks }, defaults)
      : undefined;

  const changeWorkspace = (id: string): void => {
    setWorkspace(id);
    setOverrides({});
  };
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>): void => {
    if (e.key !== "Enter" || e.nativeEvent.isComposing || body === undefined) return;
    const command = commandById("session.create");
    if (command === undefined) return;
    void runCommand(command, ctx, { body });
  };

  return (
    <div className="flex flex-col gap-4 p-4">
      {workspaces.length === 0 ? (
        <p className="text-sm text-muted-foreground">No workspace is open.</p>
      ) : (
        <WorkspacePicker workspaces={workspaces} value={workspace} onChange={changeWorkspace} />
      )}
      <Field label="Title" htmlFor="new-title">
        <Input
          id="new-title"
          value={title}
          autoCapitalize="none"
          autoComplete="off"
          onChange={(e) => setTitle(e.target.value)}
          onKeyDown={onKeyDown}
        />
      </Field>
      <Field label="Prompt" htmlFor="new-prompt">
        <Textarea
          id="new-prompt"
          rows={6}
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
        />
      </Field>
      {tooLong ? (
        <p role="alert" className="text-[12px] text-error">
          Too long: a prompt is at most 64 KiB.
        </p>
      ) : null}
      {error !== "" ? (
        <div className="flex flex-col gap-2">
          <p role="alert" className="text-sm text-error">
            Could not load the launch defaults.
          </p>
          <Button variant="secondary" onClick={retry}>
            Retry
          </Button>
        </div>
      ) : null}
      {defaults !== undefined && picks !== undefined ? (
        <LaunchOptions
          defaults={defaults}
          picks={picks}
          onPick={(key, value) => setOverrides((o) => ({ ...o, [key]: value }))}
        />
      ) : null}
      <div className="flex flex-col">
        <CommandButton
          id="session.create"
          args={body === undefined ? undefined : { body }}
          disabled={!ready}
          size="lg"
        />
      </div>
    </div>
  );
}

// NewSessionBody is the new-session screen. The form mounts when the first snapshot lands, so its
// first workspace comes from that snapshot.
export default function NewSessionBody(): ReactElement {
  const snapshot = useFleet((s) => s.snapshot);
  if (snapshot === null) return <p className="p-4 text-sm text-muted-foreground">Loading…</p>;
  return <Form workspaces={snapshot.workspaces ?? []} />;
}
