import { act, render, screen, type RenderResult } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi, type MockInstance } from "vitest";

import "@/commands";
import type { Job, Session } from "@/api/gen";
import { useJobLog, type JobLogView } from "@/api/reads";
import { api } from "@/api/request";
import { askConfirm } from "@/app/ConfirmSheet";
import { definePanel, panelFor } from "@/app/panels";
import { elementPane, ScrollPaneProvider, type ScrollPane } from "@/app/scroll";
import { stubScroller, type Scroller } from "@/app/scrollkit";
import { PanelHost } from "@/app/shell";
import { makeJob, makeSession, noticesOk, resetFleet, seed } from "@/commands/testkit";
import { fixtureJobLog } from "@/fixtures";
import { useFleet } from "@/store/fleet";
import { usePending } from "@/store/pending";

import { JobHead } from "./index";
import JobBody from "./JobBody";

vi.mock("@/api/reads", async (original) => ({
  ...(await original<typeof import("@/api/reads")>()),
  useJobLog: vi.fn(),
}));
vi.mock("@/app/ConfirmSheet", async (original) => ({
  ...(await original<typeof import("@/app/ConfirmSheet")>()),
  askConfirm: vi.fn(),
}));

const NOW = Date.parse("2026-11-19T12:00:00Z");
const at = (ms: number): string => new Date(NOW - ms).toISOString();
const noLog: JobLogView = { log: undefined, gone: "", error: "" };

beforeEach(() => {
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(NOW);
  vi.mocked(useJobLog).mockReturnValue(noLog);
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.mocked(askConfirm).mockReset();
  resetFleet();
  window.history.replaceState(null, "", window.location.pathname);
});

function show(jobs: Job[], job: number, over: Partial<Session> = {}): RenderResult {
  seed([makeSession({ id: 1, status: "Running", group: "working", jobs, ...over })]);
  window.history.replaceState(null, "", `#/s/1/job/${job}`);
  return render(<JobBody sessionId={1} params={{ id: "1", job: String(job) }} search={{}} />);
}

describe("JobBody facts", () => {
  it("a running job says how long it has run, paused and forced", () => {
    const running = (over: Partial<Job>) =>
      makeJob({ id: 214, state: "running", started: at(130_000), ...over });
    const view = show([running({ paused: true, pause_reclaimed: false, forced: true })], 214);
    expect(
      screen.getByText("job 214 · running · 2m 10s · paused, CPU only · forced"),
    ).toBeInTheDocument();
    view.unmount();

    const reclaimed = show([running({ paused: true, pause_reclaimed: true })], 214);
    expect(screen.getByText("job 214 · running · 2m 10s · paused")).toBeInTheDocument();
    expect(screen.queryByText(/CPU only/)).toBeNull();
    reclaimed.unmount();

    show([running({})], 214);
    expect(screen.getByText("job 214 · running · 2m 10s")).toBeInTheDocument();
  });

  it("a queued or held job says its state and why it waits", () => {
    const view = show(
      [makeJob({ id: 216, state: "queued", waiting: "waiting for 6 free cores" })],
      216,
    );
    expect(screen.getByText("job 216 · queued · waiting for 6 free cores")).toBeInTheDocument();
    view.unmount();

    show([makeJob({ id: 215, state: "held", waiting: "" })], 215);
    expect(screen.getByText("job 215 · held")).toBeInTheDocument();
  });

  it("a finished job says how it ended, how long it ran and when", () => {
    const done = (over: Partial<Job>) =>
      makeJob({ id: 212, state: "done", exit: { code: 0 }, ...over });
    const first = show([done({ started: at(540_000), ended: at(240_000) })], 212);
    expect(screen.getByText("job 212 · exit 0 · ran 5m 0s · 4m ago")).toBeInTheDocument();
    first.unmount();

    const second = show([done({ started: at(70_000), ended: at(10_000) })], 212);
    expect(screen.getByText("job 212 · exit 0 · ran 1m 0s · now")).toBeInTheDocument();
    expect(screen.queryByText(/now ago/)).toBeNull();
    second.unmount();

    show([done({ started: "", ended: "" })], 212);
    expect(screen.getByText("job 212 · exit 0")).toBeInTheDocument();
    expect(screen.queryByText(/ran/)).toBeNull();
  });

  it("a job whose state this build does not know shows its raw state, a neutral glyph and no buttons", () => {
    show([makeJob({ id: 214, state: "paused" as Job["state"], exit: null })], 214);
    expect(screen.getByText("job 214 · paused")).toBeInTheDocument();
    expect(screen.getByText("?")).toBeInTheDocument();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("draws the command under a described job and the submitter of a subagent's job", () => {
    const view = show(
      [
        makeJob({
          description: "race tests",
          command: "go test -race ./ui/...",
          agent: "a1f00dcafe0001",
        }),
      ],
      214,
    );
    expect(screen.getByText("go test -race ./ui/...")).toBeInTheDocument();
    expect(screen.getByText("submitted by subagent a1f00dcafe0001")).toBeInTheDocument();
    view.unmount();

    show([makeJob({ description: "", command: "go vet ./...", agent: "" })], 214);
    expect(screen.queryByText("go vet ./...")).toBeNull();
    expect(screen.queryByText(/submitted by/)).toBeNull();
  });

  it("draws no facts for a job the session does not list, and still reads its log", () => {
    show([], 999);
    expect(screen.queryByText(/job 999 ·/)).toBeNull();
    expect(useJobLog).toHaveBeenCalledWith({ session: 1, job: 999, live: false });
  });
});

describe("JobBody controls", () => {
  const buttons = (): string[] => screen.queryAllByRole("button").map((b) => b.textContent ?? "");

  it.each([
    ["queued", { state: "queued" }, ["Start now", "Park"]],
    ["queued, parked", { state: "queued", parked: true }, ["Start now", "Unpark"]],
    ["held", { state: "held" }, ["Park"]],
    ["held, parked", { state: "held", parked: true }, ["Unpark"]],
    ["running", { state: "running" }, ["Pause"]],
    ["running, paused", { state: "running", paused: true }, ["Resume"]],
    ["done", { state: "done", exit: { code: 0 } }, []],
    ["failed", { state: "failed" }, []],
    ["cancelled", { state: "cancelled" }, []],
  ] as [string, Partial<Job>, string[]][])(
    "offers the buttons its state allows: %s",
    (_, over, want) => {
      show([makeJob({ id: 214, started: at(1000), ...over })], 214);
      expect(buttons()).toEqual(want);
    },
  );

  it("shows Pausing… or Resuming… in place of the buttons while a control is in flight", () => {
    const view = show(
      [makeJob({ id: 214, state: "running", control: "pause", control_error: "x" })],
      214,
    );
    expect(screen.getByText("Pausing…")).toBeInTheDocument();
    expect(screen.queryByRole("button")).toBeNull();
    expect(screen.queryByText("x")).toBeNull();
    view.unmount();

    show([makeJob({ id: 214, state: "running", control: "resume" })], 214);
    expect(screen.getByText("Resuming…")).toBeInTheDocument();
  });

  it("draws the last control's error under the buttons", () => {
    const view = show([makeJob({ id: 214, state: "running", control_error: "refused" })], 214);
    expect(screen.getByText("refused")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Pause" })).toBeInTheDocument();
    view.unmount();

    show([makeJob({ id: 214, state: "done", exit: { code: 0 }, control_error: "refused" })], 214);
    expect(screen.queryByText("refused")).toBeNull();
  });

  it("Start now asks first, naming what the job waits for, and then posts start", async () => {
    vi.mocked(askConfirm).mockResolvedValue("");
    const control = vi.spyOn(api, "jobControl").mockResolvedValue(noticesOk());
    show([makeJob({ id: 216, state: "queued", waiting: "waiting for 6 free cores" })], 216);

    await userEvent.click(screen.getByRole("button", { name: "Start now" }));

    const spec = vi.mocked(askConfirm).mock.calls[0]?.[0];
    expect(spec?.title).toBe("Start job 216 now?");
    expect(spec?.body).toContain("waiting for 6 free cores");
    expect(control).toHaveBeenCalledWith(1, 216, "start");
  });

  it("Start now posts nothing when the sheet is dismissed", async () => {
    vi.mocked(askConfirm).mockResolvedValue(null);
    const control = vi.spyOn(api, "jobControl").mockResolvedValue(noticesOk());
    show([makeJob({ id: 216, state: "queued" })], 216);

    await userEvent.click(screen.getByRole("button", { name: "Start now" }));

    expect(askConfirm).toHaveBeenCalled();
    expect(control).not.toHaveBeenCalled();
  });

  it("Pause posts pause for this job", async () => {
    const control = vi.spyOn(api, "jobControl").mockResolvedValue(noticesOk());
    show([makeJob({ id: 214, state: "running", started: at(1000) })], 214);

    await userEvent.click(screen.getByRole("button", { name: "Pause" }));

    expect(control).toHaveBeenCalledWith(1, 214, "pause");
    expect(askConfirm).not.toHaveBeenCalled();
  });

  it("the buttons are off while the snapshot is stale", () => {
    show([makeJob({ id: 214, state: "running", started: at(1000) })], 214);
    act(() => {
      useFleet.setState({ stale: true });
    });
    expect(screen.getByRole("button", { name: "Pause" })).toBeDisabled();
  });
});

describe("JobBody log", () => {
  it.each([
    ["running", true],
    ["queued", true],
    ["held", true],
    ["done", false],
    ["failed", false],
    ["cancelled", false],
    ["lost", false],
  ] as [Job["state"], boolean][])(
    "reads the log live while the job has yet to end: %s",
    (state, live) => {
      show([makeJob({ id: 214, state })], 214);
      expect(useJobLog).toHaveBeenCalledWith({ session: 1, job: 214, live });
    },
  );

  it("draws the log as text, never as HTML", () => {
    vi.mocked(useJobLog).mockReturnValue({
      ...noLog,
      log: { text: "<b>bold</b> & more", end: 18, cut: false },
    });
    const { container } = show([makeJob({ id: 214, state: "done", exit: { code: 0 } })], 214);
    expect(container.querySelector("pre")?.textContent).toBe("<b>bold</b> & more");
    expect(container.querySelector("b")).toBeNull();
  });

  it("says earlier output was cut, from the fixture's log", () => {
    const page = fixtureJobLog();
    const held = { text: page.text, end: page.end, cut: page.cut };
    vi.mocked(useJobLog).mockReturnValue({ ...noLog, log: { ...held, cut: true } });
    const view = show([makeJob({ id: 214 })], 214);
    expect(screen.getByText("Earlier output not shown.")).toBeInTheDocument();
    expect(screen.getByText(/TestPane_CursorDrawnAfterFrame/)).toBeInTheDocument();
    view.unmount();

    vi.mocked(useJobLog).mockReturnValue({ ...noLog, log: { ...held, cut: false } });
    show([makeJob({ id: 214 })], 214);
    expect(screen.queryByText("Earlier output not shown.")).toBeNull();
  });

  it("says there is no output yet while the job runs and no output once it ended", () => {
    vi.mocked(useJobLog).mockReturnValue({ ...noLog, log: { text: "", end: 0, cut: false } });
    const view = show([makeJob({ id: 214, state: "running" })], 214);
    expect(screen.getByText("No output yet.")).toBeInTheDocument();
    view.unmount();

    show([makeJob({ id: 214, state: "done", exit: { code: 0 } })], 214);
    expect(screen.getByText("No output.")).toBeInTheDocument();
  });

  it("says Loading… before the first read, the error when it failed, and why a job is gone", () => {
    const view = show([makeJob({ id: 214 })], 214);
    expect(screen.getByText("Loading…")).toBeInTheDocument();
    view.unmount();

    vi.mocked(useJobLog).mockReturnValue({ ...noLog, error: "Could not read the job's log." });
    const failed = show([makeJob({ id: 214 })], 214);
    expect(screen.getByText("Could not read the job's log.")).toBeInTheDocument();
    expect(screen.queryByText("Loading…")).toBeNull();
    failed.unmount();

    vi.mocked(useJobLog).mockReturnValue({
      ...noLog,
      gone: "This job is no longer in the queue.",
      log: { text: "held", end: 4, cut: false },
    });
    const { container } = show([makeJob({ id: 214 })], 214);
    expect(screen.getByText("This job is no longer in the queue.")).toBeInTheDocument();
    expect(container.querySelector("pre")).toBeNull();
  });

  it("keeps the held log and says the read failed when a later read fails", () => {
    vi.mocked(useJobLog).mockReturnValue({
      ...noLog,
      log: { text: "kept", end: 4, cut: false },
      error: "Could not read the job's log.",
    });
    show([makeJob({ id: 214 })], 214);
    expect(screen.getByText("kept")).toBeInTheDocument();
    expect(screen.getByText("Could not read the job's log.")).toBeInTheDocument();
  });

  it("draws This session is gone. for a session the snapshot does not list", () => {
    resetFleet();
    seed([]);
    render(<JobBody sessionId={1} params={{ id: "1", job: "214" }} search={{}} />);
    expect(screen.getByText("This session is gone.")).toBeInTheDocument();
    expect(useJobLog).not.toHaveBeenCalled();
  });
});

describe("JobBody following the log", () => {
  let scrollTo: MockInstance<typeof window.scrollTo>;
  beforeEach(() => {
    scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => undefined);
    Object.defineProperty(document.documentElement, "scrollHeight", {
      value: 5000,
      configurable: true,
    });
    Object.defineProperty(window, "innerHeight", { value: 800, configurable: true });
  });

  function placeReader(fromBottom: number): void {
    Object.defineProperty(window, "scrollY", {
      value: 5000 - 800 - fromBottom,
      configurable: true,
    });
    act(() => {
      window.dispatchEvent(new Event("scroll"));
    });
  }
  const more = (text: string): void => {
    vi.mocked(useJobLog).mockReturnValue({
      ...noLog,
      log: { text, end: text.length, cut: false },
    });
  };
  // A new element each time: React skips a rerender of the very same element.
  const element = (): ReactElement => (
    <JobBody sessionId={1} params={{ id: "1", job: "214" }} search={{}} />
  );

  it("scrolls to the bottom when the screen opens with output", () => {
    more("first");
    show([makeJob({ id: 214, state: "running" })], 214);
    expect(scrollTo).toHaveBeenCalledWith(0, 5000);
  });

  it("follows new output while the reader is at the bottom or within 40 px of it", () => {
    more("first");
    const view = show([makeJob({ id: 214, state: "running" })], 214);
    placeReader(0);
    scrollTo.mockClear();
    more("second");
    view.rerender(element());
    expect(scrollTo).toHaveBeenCalledWith(0, 5000);

    placeReader(40);
    scrollTo.mockClear();
    more("third");
    view.rerender(element());
    expect(scrollTo).toHaveBeenCalledWith(0, 5000);
  });

  it("leaves a reader who scrolled up more than 40 px where they are, and follows again once they are back", () => {
    more("first");
    const view = show([makeJob({ id: 214, state: "running" })], 214);
    placeReader(41);
    scrollTo.mockClear();
    more("second");
    view.rerender(element());
    expect(scrollTo).not.toHaveBeenCalled();

    placeReader(0);
    more("third");
    view.rerender(element());
    expect(scrollTo).toHaveBeenCalledWith(0, 5000);
  });
});

describe("JobBody following the log inside a pane", () => {
  let scrollTo: MockInstance<typeof window.scrollTo>;
  let s: Scroller;
  let pane: ScrollPane;
  beforeEach(() => {
    scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => undefined);
    Object.defineProperty(document.documentElement, "scrollHeight", {
      value: 9000,
      configurable: true,
    });
    Object.defineProperty(window, "innerHeight", { value: 800, configurable: true });
    s = stubScroller({ height: 5000, viewport: 600 });
    pane = elementPane(s.el, null);
  });
  afterEach(() => {
    expect(scrollTo).not.toHaveBeenCalled();
  });

  const more = (text: string): void => {
    vi.mocked(useJobLog).mockReturnValue({
      ...noLog,
      log: { text, end: text.length, cut: false },
    });
  };
  // A new element each time: React skips a rerender of the very same element.
  const element = (): ReactElement => (
    <ScrollPaneProvider pane={pane}>
      <JobBody sessionId={1} params={{ id: "1", job: "214" }} search={{}} />
    </ScrollPaneProvider>
  );
  const open = (): RenderResult => {
    seed([
      makeSession({
        id: 1,
        status: "Running",
        group: "working",
        jobs: [makeJob({ id: 214, state: "running" })],
      }),
    ]);
    window.history.replaceState(null, "", "#/s/1/job/214");
    return render(element());
  };

  it("opens at the bottom of the pane when the screen opens with output", () => {
    more("first");
    open();
    expect(s.scrolls).toEqual([5000]);
  });

  it("follows new output while the reader is within 40 px of the pane's bottom", () => {
    more("first");
    const view = open();
    s.place(5000 - 600 - 40);
    s.scrolls.length = 0;
    more("second");
    view.rerender(element());
    expect(s.scrolls).toEqual([5000]);
  });

  it("leaves a reader more than 40 px above the pane's bottom, and follows again once they are back", () => {
    more("first");
    const view = open();
    s.place(5000 - 600 - 41);
    s.scrolls.length = 0;
    more("second");
    view.rerender(element());
    expect(s.scrolls).toEqual([]);

    s.place(5000 - 600);
    more("third");
    view.rerender(element());
    expect(s.scrolls).toEqual([5000]);
  });
});

describe("JobHead", () => {
  const head = (job: string): RenderResult =>
    render(<JobHead sessionId={1} params={{ id: "1", job }} search={{}} />);

  it("titles the head by the description, else the command, else job N", () => {
    seed([
      makeSession({
        id: 1,
        jobs: [
          makeJob({ id: 214, description: "ui tests" }),
          makeJob({ id: 215, description: "", command: "go vet ./..." }),
        ],
      }),
    ]);
    window.history.replaceState(null, "", "#/s/1/job/214");
    const first = head("214");
    expect(screen.getByRole("heading", { name: "ui tests" })).toBeInTheDocument();
    first.unmount();
    const second = head("215");
    expect(screen.getByRole("heading", { name: "go vet ./..." })).toBeInTheDocument();
    second.unmount();
    head("999");
    expect(screen.getByRole("heading", { name: "job 999" })).toBeInTheDocument();
  });

  it("goes back to info, named by the info tab, and to the fleet without the session", () => {
    definePanel({
      id: "info",
      title: () => "Info",
      tab: { order: 3, label: "info" },
      Body: () => null,
    });
    seed([makeSession({ id: 1, jobs: [makeJob({ id: 214 })] })]);
    window.history.replaceState(null, "", "#/s/1/job/214");
    const view = head("214");
    const link = screen.getByRole("link", { name: "‹ info" });
    expect(link).toHaveAttribute("href", "#/s/1/info");
    view.unmount();

    resetFleet();
    head("214");
    expect(screen.getByRole("link")).toHaveAttribute("href", "#/");
    expect(screen.getByRole("heading").textContent).toBe("");
  });
});

describe("the job panel", () => {
  it("is registered with a head, a lazy body and no tab", () => {
    const panel = panelFor("job");
    expect(panel).toBeDefined();
    expect(panel?.Head).toBe(JobHead);
    expect(panel?.tab).toBeUndefined();
    expect(panel?.appliesTo).toBeUndefined();
    expect(panel?.title({ sessionId: 1, params: { job: "214" }, search: {} })).toBe("Job 214");
  });

  it("draws the screen through PanelHost once its chunk loads", async () => {
    seed([
      makeSession({
        id: 1,
        status: "Running",
        group: "working",
        jobs: [makeJob({ id: 214, state: "running", started: at(1000) })],
      }),
    ]);
    window.history.replaceState(null, "", "#/s/1/job/214");
    render(
      <PanelHost
        panel="job"
        props={{ sessionId: 1, params: { id: "1", job: "214" }, search: {} }}
      />,
    );
    expect(await screen.findByText(/job 214 · running/)).toBeInTheDocument();
  });
});

describe("pending session", () => {
  afterEach(() => {
    usePending.getState().clear();
  });

  const head = { sessionId: 9, params: { id: "9", job: "214" }, search: {} };

  it("JobHead titles a pending unlisted session Loading…", () => {
    seed([makeSession({ id: 1 })]);
    usePending.setState({ id: 9 });
    render(<JobHead {...head} />);
    expect(screen.getByRole("heading").textContent).toBe("Loading…");
    expect(screen.getByRole("link").getAttribute("href")).toBe("#/");
  });

  it("JobHead keeps its empty title for an unlisted session that is not pending, and for another session's pending id", () => {
    seed([makeSession({ id: 1 })]);
    const first = render(<JobHead {...head} />);
    expect(screen.getByRole("heading").textContent).toBe("");
    first.unmount();
    usePending.setState({ id: 8 });
    render(<JobHead {...head} />);
    expect(screen.getByRole("heading").textContent).toBe("");
  });
});
