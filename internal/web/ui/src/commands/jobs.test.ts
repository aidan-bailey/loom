import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/request";
import { commandEnabled, runCommand } from "@/app/commands";
import { askConfirm } from "@/app/ConfirmSheet";

import "./jobs";
import {
  askedSheet,
  command,
  context,
  deferred,
  makeJob,
  makeSession,
  noticesOk,
  resetFleet,
  seed,
  spyOnAdd,
} from "./testkit";

vi.mock("@/app/ConfirmSheet", async (original) => ({
  ...(await original<typeof import("@/app/ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));

let add: ReturnType<typeof spyOnAdd>;

beforeEach(() => {
  add = spyOnAdd();
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.mocked(askConfirm).mockReset();
  resetFleet();
});

const CONTROLS: [string, string][] = [
  ["job.park", "park"],
  ["job.unpark", "unpark"],
  ["job.pause", "pause"],
  ["job.resume", "resume"],
];

describe("the job commands' availability", () => {
  it.each(["job.start", ...CONTROLS.map(([id]) => id)])(
    "%s is on with a session on screen, and off with none or while the snapshot is stale",
    (id) => {
      const session = makeSession();
      expect(commandEnabled(command(id), context(session))).toBe(true);
      expect(commandEnabled(command(id), context(undefined))).toBe(false);
      expect(commandEnabled(command(id), context(session, { stale: true, link: "offline" }))).toBe(
        false,
      );
    },
  );
});

describe("park, unpark, pause and resume", () => {
  it.each(CONTROLS)("%s controls the job with no sheet", async (id, action) => {
    const control = vi.spyOn(api, "jobControl").mockResolvedValue(noticesOk());

    await runCommand(command(id), context(makeSession()), { job: 214 });

    expect(askConfirm).not.toHaveBeenCalled();
    expect(control).toHaveBeenCalledOnce();
    expect(control).toHaveBeenCalledWith(1, 214, action);
  });

  it("toasts a refusal in the model's words", async () => {
    vi.spyOn(api, "jobControl").mockResolvedValue({
      kind: "refused",
      status: 409,
      message: "job 214 is not running",
    });

    await runCommand(command("job.pause"), context(makeSession()), { job: 214 });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({ description: "job 214 is not running", type: "error" }),
    );
  });

  it("says a 202's outcome will follow", async () => {
    vi.spyOn(api, "jobControl").mockResolvedValue({ kind: "pending" });

    await runCommand(command("job.park"), context(makeSession()), { job: 214 });

    expect(add).toHaveBeenCalledWith(
      expect.objectContaining({
        description: "Park is still running. Its outcome will follow.",
        type: "info",
      }),
    );
  });

  it("keys each control by its job: another job's runs beside it, the same job's runs once", async () => {
    const gate = deferred<ReturnType<typeof noticesOk>>();
    const control = vi.spyOn(api, "jobControl").mockReturnValue(gate.promise);
    const ctx = context(makeSession());

    const first = runCommand(command("job.pause"), ctx, { job: 1 });
    const again = runCommand(command("job.pause"), ctx, { job: 1 });
    const other = runCommand(command("job.pause"), ctx, { job: 2 });
    gate.resolve(noticesOk());
    await Promise.all([first, again, other]);

    expect(control.mock.calls).toEqual([
      [1, 1, "pause"],
      [1, 2, "pause"],
    ]);
  });
});

describe("job.start", () => {
  it("asks 'Start job N now?', naming what the job waits for", async () => {
    const session = makeSession({
      jobs: [makeJob({ id: 216, waiting: "memory: 6 GiB free of 8 GiB" })],
    });
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);

    await runCommand(command("job.start"), context(session), { job: 216 });

    expect(askedSheet()).toEqual({
      title: "Start job 216 now?",
      body: "It waits: memory: 6 GiB free of 8 GiB. It starts past the queue's fit; the ladder takes it last if memory runs short.",
      action: "Start now",
      destructive: false,
    });
  });

  it("leaves out the wait when the job names none, and for a job the session does not list", async () => {
    const session = makeSession({ jobs: [makeJob({ id: 216, waiting: "" })] });
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const plain = "It starts past the queue's fit; the ladder takes it last if memory runs short.";

    await runCommand(command("job.start"), context(session), { job: 216 });
    expect(askedSheet().body).toBe(plain);

    vi.mocked(askConfirm).mockClear();
    await runCommand(command("job.start"), context(session), { job: 999 });
    expect(askedSheet().title).toBe("Start job 999 now?");
    expect(askedSheet().body).toBe(plain);
  });

  it("starts the job on a yes", async () => {
    const session = makeSession({ jobs: [makeJob({ id: 216 })] });
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue("");
    const control = vi.spyOn(api, "jobControl").mockResolvedValue(noticesOk());

    await runCommand(command("job.start"), context(session), { job: 216 });

    expect(control).toHaveBeenCalledOnce();
    expect(control).toHaveBeenCalledWith(1, 216, "start");
  });

  it("starts nothing when the sheet is cancelled", async () => {
    const session = makeSession({ jobs: [makeJob({ id: 216 })] });
    seed([session]);
    vi.mocked(askConfirm).mockResolvedValue(null);
    const control = vi.spyOn(api, "jobControl").mockResolvedValue(noticesOk());

    await runCommand(command("job.start"), context(session), { job: 216 });

    expect(control).not.toHaveBeenCalled();
  });
});
