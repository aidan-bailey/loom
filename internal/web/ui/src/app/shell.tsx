import { Component, type ErrorInfo, type ReactElement, type ReactNode } from "react";

import { Button } from "@/components/ui/button";

import { panelFor, type PanelId, type PanelProps } from "./panels";
import { hrefFor } from "./routes";
import { switchApp } from "./switchApp";

// SCREEN_NAMES is what each screen is called where the app names it: the
// placeholder's heading and an error boundary's message.
const SCREEN_NAMES: Record<PanelId, string> = {
  fleet: "Fleet",
  turns: "Conversation",
  info: "Info",
  changes: "Changes",
  mockup: "Mockup",
  mockupFull: "Mockup",
  agent: "Subagent",
  job: "Job",
  flow: "Flow",
  flowStory: "Flow story",
  flowDoc: "Flow document",
  flows: "Flows",
  new: "New session",
  settings: "Settings",
  pair: "Pairing",
};

// resetKey changes with the route, so a boundary that caught an error starts
// over once the route is another one.
function resetKey(panel: PanelId, props: PanelProps): string {
  return `${panel}/${JSON.stringify(props.params)}`;
}

interface BoundaryProps {
  what: string;
  // fallback replaces the failed subtree; without it the boundary shows what
  // failed and a reload.
  fallback?: ReactNode;
  children: ReactNode;
}

interface BoundaryState {
  error: Error | null;
}

// Boundary keeps one broken view from blanking the app (spec §29): the
// header, the footer and every other panel keep working.
class Boundary extends Component<BoundaryProps, BoundaryState> {
  state: BoundaryState = { error: null };

  static getDerivedStateFromError(error: unknown): BoundaryState {
    return { error: error instanceof Error ? error : new Error(String(error)) };
  }

  componentDidCatch(error: unknown, info: ErrorInfo): void {
    console.error(`loom: ${this.props.what} failed to draw`, error, info.componentStack);
  }

  render(): ReactNode {
    const { error } = this.state;
    if (error === null) return this.props.children;
    if (this.props.fallback !== undefined) return this.props.fallback;
    return (
      <div
        role="alert"
        className="m-4 flex flex-col items-start gap-2 rounded-lg border border-error p-4"
      >
        <p className="font-semibold">{this.props.what} failed to draw.</p>
        <p className="font-mono text-xs break-words text-muted-foreground">{error.message}</p>
        <Button
          variant="secondary"
          onClick={() => {
            window.location.reload();
          }}
        >
          Reload
        </Button>
      </div>
    );
  }
}

// Placeholder draws a route no panel is registered for: it names the screen
// and offers the way back to the classic app (spec §7).
export function Placeholder({ panel }: { panel: PanelId }): ReactElement {
  const name = SCREEN_NAMES[panel];
  return (
    <section className="flex flex-col items-start gap-3 px-4 py-8">
      {panel === "fleet" ? null : (
        <a href={hrefFor("fleet")} className="text-sm text-muted-foreground">
          ‹ fleet
        </a>
      )}
      <h1 className="text-lg font-semibold">{name}</h1>
      <p className="text-muted-foreground">
        {name} is not in the new app yet. It is in the classic one.
      </p>
      <Button
        variant="secondary"
        onClick={() => {
          void switchApp("classic");
        }}
      >
        Back to classic
      </Button>
    </section>
  );
}

// PanelHost draws the panel registered under `panel` inside an error
// boundary, or the placeholder when none is.
export function PanelHost({ panel, props }: { panel: PanelId; props: PanelProps }): ReactElement {
  const entry = panelFor(panel);
  if (entry === undefined) return <Placeholder panel={panel} />;
  return (
    <Boundary key={resetKey(panel, props)} what={SCREEN_NAMES[panel]}>
      <entry.Body {...props} />
    </Boundary>
  );
}
