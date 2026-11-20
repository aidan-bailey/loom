// The settings body (a chunk of its own): this phone, the way back to the classic app and the
// recent notices. A port of the classic settings.mjs (spec Part 3 settlement 9).
import type { ReactElement, ReactNode } from "react";

import { CommandButton } from "@/app/CommandButton";
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
    <div className="flex min-h-11 items-center justify-between gap-3">
      <span>{label}</span>
      {children}
    </div>
  );
}

export default function SettingsBody(): ReactElement {
  const device = useFleet((s) => s.snapshot?.device);
  const host = useFleet((s) => s.snapshot?.host ?? "");
  const build = useFleet((s) => s.snapshot?.build ?? "");
  const notices = useFleet((s) => s.snapshot?.notices ?? null);

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
