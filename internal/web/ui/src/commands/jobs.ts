// The job commands: start, park, unpark, pause and resume a work-queue job of the session on
// screen, each keyed by the job's id so a control on one job never holds one on another (spec
// Part 2 decision 7). The daemon decides what each state allows; the job view draws only the
// buttons its state offers (job.mjs `controls`).
import { api } from "@/api/request";
import { defineCommand, type CommandContext } from "@/app/commands";
import { showOutcome } from "@/app/toasts";

import { requireSession } from "./session";

interface JobArgs {
  job: number;
}

// control asks the daemon for `action` on the job; `what` names it in a toast.
async function control(ctx: CommandContext, job: number, action: string, what: string) {
  showOutcome(await api.jobControl(requireSession(ctx).id, job, action), what);
}

const key = (args: JobArgs): string => String(args.job);
const when = (ctx: CommandContext): boolean => ctx.session !== undefined;

const QUEUE_NOTE = "It starts past the queue's fit; the ladder takes it last if memory runs short.";

defineCommand<JobArgs>({
  id: "job.start",
  title: "Start now",
  key,
  when,
  confirm: (ctx, args) => {
    const waiting = ctx.session?.jobs?.find((job) => job.id === args.job)?.waiting ?? "";
    return {
      title: `Start job ${args.job} now?`,
      body: waiting === "" ? QUEUE_NOTE : `It waits: ${waiting}. ${QUEUE_NOTE}`,
      action: "Start now",
      destructive: false,
    };
  },
  run: (ctx, args) => control(ctx, args.job, "start", "Start"),
});

defineCommand<JobArgs>({
  id: "job.park",
  title: "Park",
  key,
  when,
  run: (ctx, args) => control(ctx, args.job, "park", "Park"),
});

defineCommand<JobArgs>({
  id: "job.unpark",
  title: "Unpark",
  key,
  when,
  run: (ctx, args) => control(ctx, args.job, "unpark", "Unpark"),
});

defineCommand<JobArgs>({
  id: "job.pause",
  title: "Pause",
  key,
  when,
  run: (ctx, args) => control(ctx, args.job, "pause", "Pause"),
});

defineCommand<JobArgs>({
  id: "job.resume",
  title: "Resume",
  key,
  when,
  run: (ctx, args) => control(ctx, args.job, "resume", "Resume"),
});
