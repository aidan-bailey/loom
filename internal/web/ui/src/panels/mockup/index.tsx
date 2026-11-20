// The mockup panels: the companion's page on the phone, as the fourth tab for a session that has
// one, and a full-screen view with only its frame. Both bodies live in one chunk, loaded when a
// mockup is opened.
import { lazy, type ReactElement } from "react";

import type { Session } from "@/api/gen";
import { definePanel } from "@/app/panels";
import { SessionHead } from "@/session/SessionHead";

// MockupMark is the tab's mark: a dot while the mockup is fresh.
export function MockupMark({ session }: { session: Session }): ReactElement | null {
  if (session.mockup === null || !session.mockup.fresh) return null;
  return (
    <span
      role="img"
      aria-label="fresh"
      className="ml-1.5 inline-block size-2 rounded-full bg-attention"
    />
  );
}

// Two dynamic imports of one module: Vite builds one chunk, MockupBody-<hash>.js.
const MockupBody = lazy(() => import("./MockupBody"));
const MockupFullBody = lazy(() =>
  import("./MockupBody").then((m) => ({ default: m.MockupFullBody })),
);

export const mockupPanel = definePanel({
  id: "mockup",
  title: () => "Mockup",
  appliesTo: (session) => session.mockup !== null,
  tab: { order: 4, label: "mockup", Mark: MockupMark },
  Head: SessionHead,
  Body: MockupBody,
});

export const mockupFullPanel = definePanel({
  id: "mockupFull",
  title: () => "Mockup",
  Body: MockupFullBody,
});
