// The settings body (a chunk of its own): this phone, the way back to the classic app and the
// recent notices. A port of the classic settings.mjs (spec Part 3 settlement 9).
import { useEffect, useReducer, useState, type ReactElement, type ReactNode } from "react";

import type { Device } from "@/api/gen";
import { CommandButton } from "@/app/CommandButton";
import {
  commandById,
  commandEnabled,
  runCommand,
  useCommandBusy,
  useCommandContext,
  type Command,
} from "@/app/commands";
import { usePushStatus } from "@/app/push";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";
import { ago } from "@/panels/fleet/SessionRow";
import { useFleet } from "@/store/fleet";

const NOTE = "text-sm text-muted-foreground";

// Section is one titled group of the screen.
function Section({ title, children }: { title: string; children: ReactNode }): ReactElement {
  return (
    <section className="flex flex-col gap-3 border-b border-border px-4 py-4">
      <h2 className="text-sm font-semibold text-muted-foreground">{title}</h2>
      {children}
    </section>
  );
}

// Row is a label with its value or control on the right, at the 44px touch height.
function Row({ label, children }: { label: string; children: ReactNode }): ReactElement {
  return (
    <div className="flex min-h-[44px] items-center justify-between gap-3">
      <span>{label}</span>
      {children}
    </div>
  );
}

// The classic app's words for the push control's states that draw no button.
const PUSH_WORDS = {
  unsupported:
    "Notifications need loom on the Home Screen. Tap Share, then Add to Home Screen, and open loom from there.",
  denied:
    "Notifications are blocked for loom. Allow them in the phone's Settings, under Notifications, then loom.",
  on: "Notifications are on for this phone.",
} as const;

// MAX_DELAY is the longest delay setTimeout takes (2^31 - 1 ms); a longer wait is re-armed.
const MAX_DELAY = 2_147_483_647;

// commandOf is the registered command with this id; the commands module registers it, so a miss
// is a bug and fails loudly, as CommandButton does.
function commandOf(id: string): Command<unknown> {
  const command = commandById(id);
  if (command === undefined) throw new Error(`loom: unknown command ${id}`);
  return command;
}

// now reads this phone's clock; kept apart from the components, which the purity lint holds to
// render the same for the same props.
function now(): number {
  return Date.now();
}

// pushLine is what the card says when the push subscription stopped working, and "" while it
// works or was never made (device.push is "", "ok", "expired" or "failing: <status>").
function pushLine(push: string): string {
  if (push === "expired") return "Notifications stopped; turn them on again.";
  if (push.startsWith("failing")) {
    return `Notifications are failing (${push.replace(/^failing:\s*/, "")}); turn them on again.`;
  }
  return "";
}

// clock words a time as the phone's own hours and minutes.
function clock(iso: string): string {
  return new Date(iso).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

// PushControl is the push control: its state from the phone and the device, and the turn-on's
// buttons. "Turning on…" is the command's in-flight state, which is module-level, so a visit
// elsewhere and back finds it still waiting and takes no second tap.
function PushControl({ device }: { device: Device }): ReactElement | null {
  const ctx = useCommandContext();
  const status = usePushStatus(device);
  const turning = useCommandBusy(commandOf("push.enable"), ctx, undefined);

  if (turning) {
    // subscribe() can take minutes: after an unsubscribe iOS took over two.
    return (
      <div className="flex flex-col items-start gap-1">
        <CommandButton id="push.enable" label="Turning on…" />
        <p className={NOTE}>The phone can take a minute or two.</p>
      </div>
    );
  }
  switch (status) {
    case "loading":
      return null;
    case "unsupported":
    case "denied":
    case "on":
      return <p className={NOTE}>{PUSH_WORDS[status]}</p>;
    case "again":
      return (
        <div className="flex">
          <CommandButton id="push.enable" label="Turn notifications on again" />
        </div>
      );
    case "off":
      return (
        <div className="flex">
          <CommandButton id="push.enable" />
        </div>
      );
  }
}

// NotifySwitch is one of the device's notification toggles. A Switch is not a button, so it runs
// device.update through runCommand with the one field it sets. It shows the value asked for from
// the tap until the write settles, then the stored one, so a refusal snaps it back; it is off
// while its write is in flight and while the command is off (a stale snapshot).
function NotifySwitch({
  field,
  label,
  checked,
}: {
  field: "waiting" | "finished";
  label: string;
  checked: boolean;
}): ReactElement {
  const ctx = useCommandContext();
  const update = commandOf("device.update");
  const [asked, setAsked] = useState<boolean | null>(null);
  const busy = useCommandBusy(update, ctx, { [field]: checked });

  const change = (next: boolean): void => {
    setAsked(next);
    void runCommand(update, ctx, { [field]: next }).finally(() => setAsked(null));
  };

  return (
    <label className="flex min-h-[44px] items-center justify-between gap-3">
      <span>{label}</span>
      <Switch
        aria-label={label}
        checked={asked ?? checked}
        disabled={busy || !commandEnabled(update, ctx)}
        onCheckedChange={change}
      />
    </label>
  );
}

// usePaused reports whether the pause that ends at `iso` is still running, and draws again when it
// ends: classic keeps "Paused until" past the end.
function usePaused(iso: string): boolean {
  const until = Date.parse(iso);
  const [ticks, redraw] = useReducer((n: number) => n + 1, 0);
  const paused = !Number.isNaN(until) && until > now();

  useEffect(() => {
    if (!paused) return undefined;
    // A wait past MAX_DELAY ends early and is armed again by the redraw it causes.
    const timer = setTimeout(redraw, Math.min(Math.max(until - now(), 0), MAX_DELAY));
    return () => clearTimeout(timer);
  }, [paused, until, ticks]);

  return paused;
}

// PauseRow is "Paused until <time>" with Resume now, or "Not paused" with Pause for an hour.
function PauseRow({ pausedUntil }: { pausedUntil: string }): ReactElement {
  const paused = usePaused(pausedUntil);
  return (
    <Row label={paused ? `Paused until ${clock(pausedUntil)}` : "Not paused"}>
      {paused ? (
        <CommandButton
          id="device.update"
          args={{ pause: "none" }}
          label="Resume now"
          variant="secondary"
        />
      ) : (
        <CommandButton
          id="device.update"
          args={{ pause: "1h" }}
          label="Pause for an hour"
          variant="secondary"
        />
      )}
    </Row>
  );
}

// Notifications is the push control, its failing line, the two toggles and the pause row.
function Notifications({ device }: { device: Device }): ReactElement {
  const line = pushLine(device.push);
  return (
    <Section title="Notifications">
      <PushControl device={device} />
      {line === "" ? null : <p className="text-sm text-error">{line}</p>}
      <NotifySwitch
        field="waiting"
        label="When a session needs you"
        checked={device.notify.waiting}
      />
      <NotifySwitch
        field="finished"
        label="When a session finishes"
        checked={device.notify.finished}
      />
      <PauseRow pausedUntil={device.notify.paused_until} />
    </Section>
  );
}

// replyLines is the editor's replies: one a line, each trimmed, blank lines dropped. The daemon
// trims again, drops a duplicate after its first and refuses more than 12.
function replyLines(text: string): string[] {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");
}

// QuickReplies edits the list every paired phone offers above its reply box. The box shows the
// stored list until the person types, then the draft; the command's onSaved, which it calls only
// after a 200, clears the draft, so a refused list stays in the box for another try.
function QuickReplies({ stored }: { stored: string[] | null }): ReactElement {
  const ctx = useCommandContext();
  const save = commandOf("quickReplies.save");
  const busy = useCommandBusy(save, ctx, { replies: null });
  const [draft, setDraft] = useState<string | null>(null);
  const shown = draft ?? (stored ?? []).join("\n");
  const saved = (): void => setDraft(null);

  return (
    <Section title="Quick replies">
      <p className={NOTE}>
        One a line, at most 12. Every paired phone offers them above its reply box.
      </p>
      <Textarea
        aria-label="Quick replies"
        value={shown}
        disabled={busy || !commandEnabled(save, ctx)}
        onChange={(e) => setDraft(e.target.value)}
      />
      <div className="flex gap-2">
        <CommandButton
          id="quickReplies.save"
          args={{ replies: null, onSaved: saved }}
          label="Reset to defaults"
          variant="secondary"
        />
        <CommandButton
          id="quickReplies.save"
          args={{ replies: replyLines(shown), onSaved: saved }}
          disabled={draft === null}
        />
      </div>
    </Section>
  );
}

export default function SettingsBody(): ReactElement {
  const device = useFleet((s) => s.snapshot?.device);
  const host = useFleet((s) => s.snapshot?.host ?? "");
  const build = useFleet((s) => s.snapshot?.build ?? "");
  const notices = useFleet((s) => s.snapshot?.notices ?? null);
  const replies = useFleet((s) => s.snapshot?.quick_replies ?? null);

  if (device === undefined) {
    return <p className="px-4 py-6 text-muted-foreground">Loading…</p>;
  }
  // Newest first: the snapshot keeps them oldest first and drops the oldest at its cap, so an
  // index shifts; a notice's key is its time and text, and a repeat of both counts up.
  const seen = new Map<string, number>();
  const recent = (notices ?? [])
    .map((notice) => {
      const base = `${notice.at}|${notice.text}`;
      const repeat = seen.get(base) ?? 0;
      seen.set(base, repeat + 1);
      return { notice, key: `${base}|${repeat}` };
    })
    .reverse();

  return (
    <div className="flex flex-col pb-8">
      <Notifications device={device} />
      <QuickReplies stored={replies} />
      <Section title="This phone">
        <Row label="Paired as">
          <span className="min-w-0 truncate">{device.name}</span>
        </Row>
        <Row label="Host">
          <span className="min-w-0 truncate">{host}</span>
        </Row>
        <Row label="Build">
          <span className="min-w-0 truncate font-mono text-sm">{build}</span>
        </Row>
        <div className="flex">
          <CommandButton id="device.unpair" variant="destructive" />
        </div>
      </Section>

      <Section title="App">
        <div className="flex">
          <CommandButton id="app.backToClassic" variant="secondary" />
        </div>
      </Section>

      <Section title="Recent notices">
        {recent.length === 0 ? (
          <p className={NOTE}>None.</p>
        ) : (
          <ul className="flex flex-col gap-2">
            {recent.map(({ notice, key }) => (
              <li key={key} className={cn("text-sm", notice.error && "text-error")}>
                <span className="mr-2 text-faint">{ago(notice.at)}</span>
                {notice.text}
              </li>
            ))}
          </ul>
        )}
      </Section>
    </div>
  );
}
