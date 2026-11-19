import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { registerWorker, shellBuild, upgradeShell } from "./worker";

const m = vi.hoisted(() => ({
  cookieApp: vi.fn(),
  reload: vi.fn(),
  register: vi.fn(),
  getRegistration: vi.fn(),
  postPush: vi.fn(),
  deletePush: vi.fn(),
  showMessage: vi.fn(),
}));

vi.mock("./switchApp", () => ({ cookieApp: m.cookieApp, page: { reload: m.reload } }));
vi.mock("@/api/request", () => ({ api: { postPush: m.postPush, deletePush: m.deletePush } }));
vi.mock("./toasts", () => ({ showMessage: m.showMessage }));

let listeners: { type: string; fn: () => void }[];

interface WorkersFake {
  controller?: object | null;
  ready?: Promise<unknown>;
}

function installWorkers(parts: WorkersFake = {}): void {
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: {
      controller: parts.controller ?? null,
      ready: parts.ready ?? new Promise(() => undefined),
      register: m.register,
      getRegistration: m.getRegistration,
      addEventListener: (type: string, fn: () => void) => {
        listeners.push({ type, fn });
      },
    },
  });
}

const settle = () => vi.advanceTimersByTimeAsync(0);

beforeEach(() => {
  vi.resetAllMocks();
  vi.useFakeTimers();
  listeners = [];
  m.cookieApp.mockReturnValue("next");
  m.register.mockResolvedValue({});
  installWorkers();
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  Reflect.deleteProperty(navigator, "serviceWorker");
  sessionStorage.clear();
  document.head.replaceChildren();
});

describe("registerWorker", () => {
  it.each(["classic", "next"])("registers the worker for the cookie's app (%s)", (app) => {
    m.cookieApp.mockReturnValue(app);
    registerWorker();
    expect(m.register).toHaveBeenCalledTimes(1);
    expect(m.register).toHaveBeenCalledWith(`/sw.js?ui=${app}`);
  });

  it("reloads when a new worker takes over a page that was already controlled", () => {
    installWorkers({ controller: {} });
    registerWorker();
    expect(listeners.map((l) => l.type)).toEqual(["controllerchange"]);
    listeners[0]?.fn();
    expect(m.reload).toHaveBeenCalledTimes(1);
  });

  it("does not reload when the first worker takes over a page that had none", () => {
    registerWorker();
    listeners[0]?.fn();
    expect(m.reload).not.toHaveBeenCalled();
  });

  it("does nothing where there is no service worker", () => {
    Reflect.deleteProperty(navigator, "serviceWorker");
    expect(() => {
      registerWorker();
    }).not.toThrow();
    expect(m.register).not.toHaveBeenCalled();
  });

  it("leaves a failed registration unremarked: the next launch tries again", async () => {
    m.register.mockRejectedValue(new Error("offline"));
    registerWorker();
    await settle();
    expect(m.reload).not.toHaveBeenCalled();
  });
});

describe("shellBuild", () => {
  it("is the loom-build meta tag's content", () => {
    const meta = document.createElement("meta");
    meta.name = "loom-build";
    meta.content = "b-9";
    document.head.append(meta);
    expect(shellBuild()).toBe("b-9");
  });

  it("is empty when the page has no such tag", () => {
    expect(shellBuild()).toBe("");
  });
});

describe("upgradeShell", () => {
  it("asks the registration to update, and reloads if nothing took over within 5 s", async () => {
    const update = vi.fn().mockResolvedValue(undefined);
    m.getRegistration.mockResolvedValue({ update });
    upgradeShell("b-2");
    await settle();
    expect(update).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(4999);
    expect(m.reload).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(m.reload).toHaveBeenCalledTimes(1);
  });

  it("still reloads after 5 s when the update itself fails", async () => {
    m.getRegistration.mockResolvedValue({ update: () => Promise.reject(new Error("offline")) });
    upgradeShell("b-2");
    await vi.advanceTimersByTimeAsync(5000);
    expect(m.reload).toHaveBeenCalledTimes(1);
  });

  it("asks once per build, and again for the next one", async () => {
    m.getRegistration.mockResolvedValue({ update: () => Promise.resolve() });
    upgradeShell("b-2");
    upgradeShell("b-2");
    await settle();
    expect(m.getRegistration).toHaveBeenCalledTimes(1);
    expect(sessionStorage.getItem("loom-next.upgrade")).toBe("b-2");
    upgradeShell("b-3");
    await settle();
    expect(m.getRegistration).toHaveBeenCalledTimes(2);
    expect(sessionStorage.getItem("loom-next.upgrade")).toBe("b-3");
  });

  it("does nothing for a build this tab already asked about", async () => {
    sessionStorage.setItem("loom-next.upgrade", "b-2");
    upgradeShell("b-2");
    await vi.advanceTimersByTimeAsync(10000);
    expect(m.getRegistration).not.toHaveBeenCalled();
    expect(m.reload).not.toHaveBeenCalled();
  });

  it("reloads at once when there is no registration to update", async () => {
    m.getRegistration.mockResolvedValue(undefined);
    upgradeShell("b-2");
    await settle();
    expect(m.reload).toHaveBeenCalledTimes(1);
  });

  it("reloads when asking for the registration fails", async () => {
    m.getRegistration.mockRejectedValue(new Error("no"));
    upgradeShell("b-2");
    await settle();
    expect(m.reload).toHaveBeenCalledTimes(1);
  });

  it("reloads at once where there is no service worker", () => {
    Reflect.deleteProperty(navigator, "serviceWorker");
    upgradeShell("b-2");
    expect(m.reload).toHaveBeenCalledTimes(1);
  });

  it("still asks when sessionStorage is unavailable, but leaves the reload to the worker", async () => {
    vi.stubGlobal("sessionStorage", {
      getItem: () => {
        throw new Error("denied");
      },
      setItem: () => {
        throw new Error("denied");
      },
    });
    const update = vi.fn().mockResolvedValue(undefined);
    m.getRegistration.mockResolvedValue({ update });
    upgradeShell("b-2");
    await settle();
    expect(m.getRegistration).toHaveBeenCalledTimes(1);
    expect(update).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(10000);
    expect(m.reload).not.toHaveBeenCalled();
  });
});
