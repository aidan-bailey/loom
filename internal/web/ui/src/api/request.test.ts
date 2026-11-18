import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Notice } from "./gen";
import { api, http, isStoppingText, onNotices, onStopping, onUnpaired } from "./request";

const fetchMock = vi.fn<typeof fetch>();

function reply(status: number, body?: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const notice: Notice = { text: "stash kept: abc123", error: false, at: "2026-11-18T10:00:00Z" };

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("http.send", () => {
  it.each([200, 201])("answers %i as ok with the body and its notices", async (status) => {
    const body = { id: 4, notices: [notice] };
    fetchMock.mockResolvedValueOnce(reply(status, body));
    const out = await http.send("POST", "/api/v1/sessions", undefined, { title: "x" });
    expect(out).toEqual({ kind: "ok", status, value: body, notices: [notice] });
  });

  it("answers an ok body without a notices key with an empty list", async () => {
    fetchMock.mockResolvedValueOnce(reply(200, { a: 1 }));
    const out = await http.send("GET", "/api/v1/flows", undefined, undefined);
    expect(out).toEqual({ kind: "ok", status: 200, value: { a: 1 }, notices: [] });
  });

  it("answers 204 as ok with no value and no notices", async () => {
    fetchMock.mockResolvedValueOnce(reply(204));
    const out = await http.send("DELETE", "/api/v1/device", undefined, undefined);
    expect(out).toEqual({ kind: "ok", status: 204, value: undefined, notices: [] });
  });

  it("fails a 200 whose body is not JSON", async () => {
    fetchMock.mockResolvedValueOnce(new Response("<html>", { status: 200 }));
    const out = await http.send("GET", "/api/v1/flows", undefined, undefined);
    expect(out).toEqual({
      kind: "failed",
      status: 200,
      message: "Unreadable answer from loom.",
    });
  });

  it("answers 202 as pending", async () => {
    fetchMock.mockResolvedValueOnce(reply(202, { pending: true }));
    const out = await http.send("POST", "/api/v1/sessions/7/kill", undefined, undefined);
    expect(out).toEqual({ kind: "pending" });
  });

  it("answers 401 as unpaired", async () => {
    fetchMock.mockResolvedValueOnce(reply(401, { error: "not paired" }));
    const out = await http.send("GET", "/api/v1/flows", undefined, undefined);
    expect(out).toEqual({ kind: "unpaired" });
  });

  it.each([
    [503, "stopping"],
    [504, "timeout"],
  ])("answers %i as %s with the body's error", async (status, kind) => {
    fetchMock.mockResolvedValueOnce(reply(status, { error: "loom says so" }));
    const out = await http.send("GET", "/api/v1/flows", undefined, undefined);
    expect(out).toEqual({ kind, message: "loom says so" });
  });

  it.each([400, 404, 405, 409, 429])(
    "answers %i as refused with the body's error",
    async (status) => {
      fetchMock.mockResolvedValueOnce(reply(status, { error: "no such session" }));
      const out = await http.send("POST", "/api/v1/sessions/9/pause", undefined, undefined);
      expect(out).toEqual({ kind: "refused", status, message: "no such session" });
    },
  );

  it("answers any other status as failed with its status and the body's error", async () => {
    fetchMock.mockResolvedValueOnce(reply(500, { error: "boom" }));
    const out = await http.send("GET", "/api/v1/flows", undefined, undefined);
    expect(out).toEqual({ kind: "failed", status: 500, message: "boom" });
  });

  it("words a failure with no JSON body as the classic app does", async () => {
    fetchMock.mockResolvedValueOnce(new Response("<html>bad gateway</html>", { status: 502 }));
    const out = await http.send("GET", "/api/v1/flows", undefined, undefined);
    expect(out).toEqual({ kind: "failed", status: 502, message: "That didn't work." });
  });

  it("answers a network failure as failed with status 0", async () => {
    fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    const out = await http.send("GET", "/api/v1/flows", undefined, undefined);
    expect(out).toEqual({ kind: "failed", status: 0, message: "Can't reach loom." });
  });

  it("sends the query in the URL and no body or content type on a GET", async () => {
    fetchMock.mockResolvedValueOnce(reply(200, {}));
    await http.send(
      "GET",
      "/api/v1/sessions/7/turns",
      { conversation: "abc", before: "9" },
      undefined,
    );
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/sessions/7/turns?conversation=abc&before=9", {
      method: "GET",
      credentials: "same-origin",
      headers: {},
    });
  });

  it("adds no question mark for an empty query", async () => {
    fetchMock.mockResolvedValueOnce(reply(200, {}));
    await http.send("GET", "/api/v1/flows", {}, undefined);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/flows",
      expect.objectContaining({ method: "GET" }),
    );
  });

  it("sends a body as JSON with its content type", async () => {
    fetchMock.mockResolvedValueOnce(reply(200, {}));
    await http.send("POST", "/api/v1/sessions/7/send", undefined, { text: "hi" });
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/sessions/7/send", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: '{"text":"hi"}',
    });
  });
});

describe("onNotices and onUnpaired", () => {
  it("hands a response's notices to every listener once", async () => {
    const a = vi.fn();
    const b = vi.fn();
    const offA = onNotices(a);
    const offB = onNotices(b);
    fetchMock.mockResolvedValueOnce(reply(200, { notices: [notice] }));
    await http.send("POST", "/api/v1/sessions/7/kill", undefined, undefined);
    expect(a).toHaveBeenCalledOnce();
    expect(a).toHaveBeenCalledWith([notice]);
    expect(b).toHaveBeenCalledOnce();
    expect(b).toHaveBeenCalledWith([notice]);
    offA();
    offB();
  });

  it("tells the listeners nothing for a response without notices", async () => {
    const listener = vi.fn();
    const off = onNotices(listener);
    fetchMock.mockResolvedValueOnce(reply(200, { notices: [] }));
    await http.send("POST", "/api/v1/sessions/7/pause", undefined, undefined);
    expect(listener).not.toHaveBeenCalled();
    off();
  });

  it("stops telling a listener once it unsubscribes", async () => {
    const listener = vi.fn();
    onNotices(listener)();
    fetchMock.mockResolvedValueOnce(reply(200, { notices: [notice] }));
    await http.send("POST", "/api/v1/sessions/7/kill", undefined, undefined);
    expect(listener).not.toHaveBeenCalled();
  });

  it("keeps telling the other listeners when one throws", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const after = vi.fn();
    const offBad = onNotices(() => {
      throw new Error("listener bug");
    });
    const offAfter = onNotices(after);
    fetchMock.mockResolvedValueOnce(reply(200, { notices: [notice] }));
    await http.send("POST", "/api/v1/sessions/7/kill", undefined, undefined);
    expect(after).toHaveBeenCalledOnce();
    offBad();
    offAfter();
  });

  it("fires the unpaired listeners on a 401, and not an unsubscribed one", async () => {
    const live = vi.fn();
    const gone = vi.fn();
    const offLive = onUnpaired(live);
    onUnpaired(gone)();
    fetchMock.mockResolvedValueOnce(reply(401, { error: "not paired" }));
    await http.send("GET", "/api/v1/flows", undefined, undefined);
    expect(live).toHaveBeenCalledOnce();
    expect(gone).not.toHaveBeenCalled();
    offLive();
  });

  it.each([
    "the loom daemon is unavailable",
    "the loom daemon is unavailable: connection lost",
    "loom is stopping",
  ])(
    "tells the stopping listeners of a 503 that says %j, and not an unsubscribed one",
    async (text) => {
      const live = vi.fn();
      const gone = vi.fn();
      const offLive = onStopping(live);
      onStopping(gone)();
      fetchMock.mockResolvedValueOnce(reply(503, { error: text }));
      const out = await http.send("GET", "/api/v1/flows", undefined, undefined);
      expect(out).toEqual({ kind: "stopping", message: text });
      expect(live).toHaveBeenCalledOnce();
      expect(gone).not.toHaveBeenCalled();
      offLive();
    },
  );

  it.each(["push is not available", "web: not enabled; run loom web enable first"])(
    "tells the stopping listeners nothing of a 503 that says %j",
    async (text) => {
      const listener = vi.fn();
      const off = onStopping(listener);
      fetchMock.mockResolvedValueOnce(reply(503, { error: text }));
      const out = await http.send("GET", "/api/v1/push/key", undefined, undefined);
      expect(out).toEqual({ kind: "stopping", message: text });
      expect(listener).not.toHaveBeenCalled();
      off();
    },
  );
});

describe("isStoppingText", () => {
  it("knows the two texts a stopping daemon answers with", () => {
    expect(isStoppingText("the loom daemon is unavailable")).toBe(true);
    expect(isStoppingText("the loom daemon is unavailable: connection lost")).toBe(true);
    expect(isStoppingText("loom is stopping")).toBe(true);
  });

  it("does not take any other 503 text for a stopping daemon", () => {
    expect(isStoppingText("push is not available")).toBe(false);
    expect(isStoppingText("")).toBe(false);
  });
});

describe("api", () => {
  it("is the generated client over http", async () => {
    fetchMock.mockResolvedValueOnce(reply(200, { notices: [] }));
    const out = await api.pause(7);
    expect(out.kind).toBe("ok");
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/sessions/7/pause",
      expect.objectContaining({ method: "POST", credentials: "same-origin" }),
    );
  });

  it("puts a GET's query in the URL", async () => {
    fetchMock.mockResolvedValueOnce(reply(200, {}));
    await api.turns(7, { conversation: "abc" });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/sessions/7/turns?conversation=abc",
      expect.objectContaining({ method: "GET" }),
    );
  });
});
