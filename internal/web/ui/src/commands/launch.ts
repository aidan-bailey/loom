// The new-session command (spec §23, Part 3 settlement 7): the body of POST /api/v1/sessions as
// the classic app builds it (newsession.mjs), and what the daemon's answer does. The daemon's
// checks stay the authority: its 400 and 409 words are the toast, and a refused create leaves
// the form as typed.
import type { CreateBody, LaunchDefaults } from "@/api/gen";
import { api } from "@/api/request";
import { defineCommand } from "@/app/commands";
import { hrefFor } from "@/app/routes";
import { showMessage, showOutcome } from "@/app/toasts";
import { usePending } from "@/store/pending";

// LaunchPicks are the four launch options the form picks from; "" is the "(default)" of a list.
export interface LaunchPicks {
  account: string;
  model: string;
  permission_mode: string;
  effort: string;
}

// LaunchForm is what the new-session form holds when Start session is pressed.
export interface LaunchForm {
  workspace: number;
  title: string;
  prompt: string;
  picks: LaunchPicks;
}

const PICKS = ["account", "model", "permission_mode", "effort"] as const;

// launchBody builds the POST /api/v1/sessions body: the workspace, the trimmed title, the prompt
// trimmed at its end only (so does the daemon), and each launch option that differs from its
// default.
export function launchBody(form: LaunchForm, defaults: LaunchDefaults): CreateBody {
  const body: CreateBody = {
    workspace: form.workspace,
    title: form.title.trim(),
    prompt: form.prompt.trimEnd(),
  };
  for (const key of PICKS) {
    if (form.picks[key] !== defaults[key]) body[key] = form.picks[key];
  }
  return body;
}

defineCommand<{ body: CreateBody }>({
  id: "session.create",
  title: "Start session",
  when: (ctx) => ctx.panel === "new",
  run: async (_ctx, args) => {
    const outcome = await api.create(args.body);
    // A 201 names the session before the snapshot lists it: its views say "Loading…" meanwhile.
    if (outcome.kind === "ok" && outcome.status === 201) {
      const id = outcome.value.id;
      usePending.getState().mark(id);
      window.location.assign(hrefFor("turns", { id: String(id) }));
      return;
    }
    // A 202: the start outlived the wait, and its outcome comes on the stream.
    if (outcome.kind === "pending") {
      showMessage("Still starting; the session will show in the fleet.");
      window.location.assign(hrefFor("fleet"));
      return;
    }
    showOutcome(outcome, "That");
  },
});
