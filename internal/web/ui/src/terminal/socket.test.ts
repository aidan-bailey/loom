import { beforeEach, describe, expect, it } from "vitest";
import {
  CLOSE_BAD_FRAME,
  CLOSE_EXITED,
  CLOSE_NO_ATTACH,
  CLOSE_OFF,
  CLOSE_REVOKED,
  CLOSE_SESSION,
  CLOSE_SLOW,
  CLOSE_STOPPING,
  FRAME_MAX,
  binaryBytes,
  describeClose,
  openSocket,
  sessionIdFromHash,
  socketURL,
  textBytes,
  type Closed,
  type Size,
  type SocketState,
  type TerminalSocket,
  type WireSocket,
} from "./socket";

const SOCKET_URL = "ws://loom.test/api/v1/sessions/7/terminal";

// A WebSocket the test drives: it records what the page sends and plays the daemon's frames.
class FakeSocket implements WireSocket {
  static made: FakeSocket[] = [];
  binaryType: BinaryType = "blob";
  onopen: ((ev: Event) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onclose: ((ev: CloseEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  sent: (string | Uint8Array)[] = [];
  closedWith: number | undefined = undefined;

  constructor(readonly url: string) {
    FakeSocket.made.push(this);
  }

  static last(): FakeSocket {
    const ws = FakeSocket.made.at(-1);
    if (ws === undefined) throw new Error("openSocket made no socket");
    return ws;
  }

  send(data: string | Uint8Array): void {
    this.sent.push(data);
  }

  close(code?: number): void {
    this.closedWith = code;
  }

  open(): void {
    this.onopen?.(new Event("open"));
  }

  say(data: string | ArrayBuffer): void {
    this.onmessage?.(new MessageEvent("message", { data }));
  }

  end(code: number, reason: string): void {
    this.onclose?.(new CloseEvent("close", { code, reason }));
  }
}

interface Rig {
  ws: FakeSocket;
  sock: TerminalSocket;
  states: { state: SocketState; closed: Closed | undefined }[];
  data: Uint8Array[];
}

function rig(size: () => Size = () => ({ cols: 120, rows: 40 })): Rig {
  const states: Rig["states"] = [];
  const data: Uint8Array[] = [];
  const sock = openSocket(
    SOCKET_URL,
    size,
    {
      state: (state, closed) => {
        states.push({ state, closed });
      },
      data: (bytes) => {
        data.push(bytes);
      },
    },
    (url) => new FakeSocket(url),
  );
  return { ws: FakeSocket.last(), sock, states, data };
}

// A rig whose socket is open and whose daemon has said it is live.
function liveRig(): Rig {
  const r = rig();
  r.ws.open();
  r.ws.say('{"state":"live"}');
  return r;
}

// What the page sent after its first frame, binary frames only.
function inputFrames(ws: FakeSocket): Uint8Array[] {
  return ws.sent.slice(1).filter((frame): frame is Uint8Array => typeof frame !== "string");
}

beforeEach(() => {
  FakeSocket.made = [];
});

describe("openSocket", () => {
  it("sends the size as its first frame, read when the socket opens", () => {
    let size: Size = { cols: 80, rows: 24 };
    const { ws } = rig(() => size);
    expect(ws.binaryType).toBe("arraybuffer");
    expect(ws.sent).toEqual([]);

    size = { cols: 132, rows: 50 };
    ws.open();
    expect(ws.sent).toEqual(['{"resize":{"cols":132,"rows":50}}']);
  });

  it("is live only when the daemon says so", () => {
    const { ws, states } = rig();
    expect(states).toEqual([{ state: "connecting", closed: undefined }]);

    ws.open();
    expect(states.map((s) => s.state)).toEqual(["connecting"]);

    for (const text of ['{"state":"other"}', "not json", "{}", "null", '"live"']) ws.say(text);
    expect(states.map((s) => s.state)).toEqual(["connecting"]);

    ws.say('{"state":"live"}');
    expect(states.map((s) => s.state)).toEqual(["connecting", "live"]);
  });

  it("hands each binary frame to the terminal as bytes, before and after live", () => {
    const { ws, data } = rig();
    ws.open();
    ws.say(new Uint8Array([104, 105]).buffer);
    ws.say('{"state":"live"}');
    ws.say(new Uint8Array([10, 13]).buffer);
    expect(data.map((b) => Array.from(b))).toEqual([
      [104, 105],
      [10, 13],
    ]);
  });

  it("ends closed with the close's code and reason and sends nothing after", () => {
    const { ws, sock, states } = liveRig();
    ws.end(4003, "the session was paused or killed");
    expect(states.at(-1)).toEqual({
      state: "closed",
      closed: { code: 4003, reason: "the session was paused or killed" },
    });

    const before = ws.sent.length;
    expect(sock.send(new Uint8Array([97]))).toBe(false);
    sock.resize(100, 30);
    expect(ws.sent.length).toBe(before);

    ws.end(1006, "");
    expect(states.filter((s) => s.state === "closed")).toHaveLength(1);
  });

  it("sends nothing before the socket has opened", () => {
    const { ws, sock } = rig();
    expect(sock.send(new Uint8Array([97]))).toBe(false);
    sock.resize(100, 30);
    expect(ws.sent).toEqual([]);

    ws.open();
    expect(ws.sent).toEqual(['{"resize":{"cols":120,"rows":40}}']);
    expect(sock.send(new Uint8Array([97]))).toBe(true);
    expect(inputFrames(ws)).toEqual([new Uint8Array([97])]);
  });

  it("splits input into binary frames of at most 64 KiB", () => {
    const { ws, sock } = liveRig();
    const big = new Uint8Array(150_000).map((_, i) => i % 251);
    expect(sock.send(big)).toBe(true);

    const frames = inputFrames(ws);
    expect(frames.map((f) => f.length)).toEqual([FRAME_MAX, FRAME_MAX, 150_000 - 2 * FRAME_MAX]);
    const joined = new Uint8Array(150_000);
    let at = 0;
    for (const frame of frames) {
      joined.set(frame, at);
      at += frame.length;
    }
    expect(joined).toEqual(big);
  });

  it("sends exactly 64 KiB as one frame and one byte more as two", () => {
    const exact = liveRig();
    exact.sock.send(new Uint8Array(FRAME_MAX));
    expect(inputFrames(exact.ws).map((f) => f.length)).toEqual([FRAME_MAX]);

    const over = liveRig();
    over.sock.send(new Uint8Array(FRAME_MAX + 1));
    expect(inputFrames(over.ws).map((f) => f.length)).toEqual([FRAME_MAX, 1]);

    const empty = liveRig();
    expect(empty.sock.send(new Uint8Array(0))).toBe(true);
    expect(inputFrames(empty.ws)).toEqual([]);
  });

  it("keeps every size inside the daemon's bounds", () => {
    const { ws, sock } = liveRig();
    sock.resize(120, 40);
    sock.resize(1000, 1000);
    sock.resize(1, 0);
    sock.resize(Number.NaN, 120.7);
    expect(ws.sent.slice(1)).toEqual([
      '{"resize":{"cols":120,"rows":40}}',
      '{"resize":{"cols":500,"rows":200}}',
      '{"resize":{"cols":2,"rows":1}}',
      '{"resize":{"cols":2,"rows":120}}',
    ]);
  });

  it("clamps the first frame's size too", () => {
    const { ws } = rig(() => ({ cols: 9999, rows: -4 }));
    ws.open();
    expect(ws.sent).toEqual(['{"resize":{"cols":500,"rows":1}}']);
  });

  it("closes with a normal close, ends closed with code 1000 and sends nothing after", () => {
    const { ws, sock, states } = liveRig();
    sock.close();
    expect(ws.closedWith).toBe(1000);
    expect(states.at(-1)).toEqual({ state: "closed", closed: { code: 1000, reason: "" } });

    // The browser may later report 1006 for a close it was asked for (a page frozen into the
    // back-forward cache): the state stays as `close()` left it.
    ws.end(1006, "");
    expect(states.at(-1)).toEqual({ state: "closed", closed: { code: 1000, reason: "" } });
    expect(states.filter((s) => s.state === "closed")).toHaveLength(1);

    const before = ws.sent.length;
    expect(sock.send(new Uint8Array([97]))).toBe(false);
    expect(ws.sent.length).toBe(before);
  });

  it("ends closed with code 1000 when closed before the socket has opened", () => {
    const { ws, sock, states } = rig();
    sock.close();
    expect(ws.closedWith).toBe(1000);
    expect(states.at(-1)).toEqual({ state: "closed", closed: { code: 1000, reason: "" } });

    // `pagehide` before the open: the browser reports 1006 for the connection it abandoned.
    ws.end(1006, "");
    expect(states.at(-1)).toEqual({ state: "closed", closed: { code: 1000, reason: "" } });
    expect(states.filter((s) => s.state === "closed")).toHaveLength(1);
  });
});

describe("close codes", () => {
  it("are the codes the daemon sends", () => {
    expect([
      CLOSE_STOPPING,
      CLOSE_OFF,
      CLOSE_REVOKED,
      CLOSE_SESSION,
      CLOSE_EXITED,
      CLOSE_SLOW,
      CLOSE_BAD_FRAME,
      CLOSE_NO_ATTACH,
    ]).toEqual([1001, 4001, 4002, 4003, 4004, 4005, 4006, 4007]);
  });
});

describe("input bytes", () => {
  it("encodes typed text as UTF-8", () => {
    expect(Array.from(textBytes("aé"))).toEqual([97, 0xc3, 0xa9]);
  });

  it("reads xterm's binary string one byte a character", () => {
    expect(Array.from(binaryBytes("\x00\xffa"))).toEqual([0, 255, 97]);
  });
});

describe("socketURL", () => {
  it("is wss on an https page and ws otherwise, on the page's own host", () => {
    const path = "/api/v1/sessions/7/terminal";
    expect(socketURL(path, { protocol: "https:", host: "loom.example.com" })).toBe(
      "wss://loom.example.com/api/v1/sessions/7/terminal",
    );
    expect(socketURL(path, { protocol: "http:", host: "127.0.0.1:5670" })).toBe(
      "ws://127.0.0.1:5670/api/v1/sessions/7/terminal",
    );
  });
});

describe("sessionIdFromHash", () => {
  it.each([
    ["#7", 7],
    ["#1234", 1234],
    ["", null],
    ["#", null],
    ["#0", null],
    ["#abc", null],
    ["#7x", null],
    ["#-3", null],
    ["#/s/7", null],
    ["#99999999999999999999", null],
  ])("reads %j as %j", (hash, want) => {
    expect(sessionIdFromHash(hash)).toBe(want);
  });
});

describe("describeClose", () => {
  it("shows the daemon's reason as it is", () => {
    expect(describeClose({ code: 4001, reason: "the terminal is off on this host" })).toBe(
      "the terminal is off on this host",
    );
  });

  it("says a close the page asked for itself was its own, and to reload", () => {
    const text = describeClose({ code: 1000, reason: "" });
    expect(text).toContain("Reload");
    expect(text).not.toContain("refused");
    expect(text).not.toContain("loom web terminal on");
  });

  it("says a close with no reason from the other side was a refusal or a loss, and names the likely causes", () => {
    const text = describeClose({ code: 1006, reason: "" });
    expect(text).toContain("1006");
    expect(text).toContain("refused or lost");
    expect(text).toContain("loom web terminal on");
    expect(text).toContain("not running");
    expect(text).toContain("too many terminals");
    expect(text).not.toContain("Reload");
  });
});
