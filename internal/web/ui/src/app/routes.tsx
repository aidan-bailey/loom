import {
  createHashHistory,
  createRootRoute,
  createRoute,
  createRouter,
} from "@tanstack/react-router";
import { useMemo, useSyncExternalStore, type ReactElement } from "react";

import type { PanelId, PanelProps } from "./panels";
import { AppShell, PanelHost } from "./shell";

// RouteState is a hash resolved: the panel it opens, the path values it
// holds (id, agent, job, story, doc) and its search (plan, at).
export interface RouteState {
  panel: PanelId;
  params: Record<string, string>;
  search: Record<string, string>;
}

// AGENT_ID is what an agent id may hold for the app to link to its screen
// (the classic app's session.mjs says the same).
export const AGENT_ID = /^[A-Za-z0-9_-]+$/;

// normalizeHash is a hash in the app's own form: `#s/<id>…` (as a push and
// the classic app write it) becomes `#/s/<id>…`, as does every slashless
// address (`#flows`), and the conversation's old address `#/s/<id>/turns`
// becomes `#/s/<id>`. "", "#" and "#/" are left as they are.
export function normalizeHash(hash: string): string {
  const slashed = /^#[^/]/.test(hash) ? "#/" + hash.slice(1) : hash;
  return slashed.replace(/^#\/s\/(\d+)\/turns$/, "#/s/$1");
}

function plain(panel: PanelId): RouteState {
  return { panel, params: {}, search: {} };
}

function decode(segment: string): string {
  try {
    return decodeURIComponent(segment);
  } catch {
    return segment;
  }
}

// routeOf is the panel a hash opens (the classic URLs in either form, the
// conversation's old address included), with its path values and search.
// Anything it does not know is the fleet, as in the classic app.
export function routeOf(hash: string): RouteState {
  const h = normalizeHash(hash).replace(/^#\/?/, "");
  switch (h) {
    case "new":
    case "settings":
    case "pair":
    case "flows":
      return plain(h);
    case "flow-board":
      return plain("flowBoard");
  }

  const flow = h.match(/^s\/(\d+)\/flow(?:\/(doc\/([^/?]+)|[^/?]+))?(?:\?(.*))?$/);
  if (flow) {
    const id = flow[1] ?? "";
    const query = new URLSearchParams(flow[4] ?? "");
    const plan = query.get("plan") ?? "";
    if (flow[3] !== undefined) {
      return {
        panel: "flowDoc",
        params: { id, doc: decode(flow[3]) },
        search: { plan, at: query.get("at") ?? "" },
      };
    }
    if (flow[2] !== undefined) {
      return { panel: "flowStory", params: { id, story: decode(flow[2]) }, search: { plan } };
    }
    return { panel: "flow", params: { id }, search: { plan } };
  }

  const agent = h.match(/^s\/(\d+)\/agent\/([^/]+)$/);
  if (agent && AGENT_ID.test(agent[2] ?? "")) {
    return { panel: "agent", params: { id: agent[1] ?? "", agent: agent[2] ?? "" }, search: {} };
  }

  const job = h.match(/^s\/(\d+)\/job\/([1-9]\d*)$/);
  if (job) {
    return { panel: "job", params: { id: job[1] ?? "", job: job[2] ?? "" }, search: {} };
  }

  const main = h.match(/^s\/(\d+)(?:\/(turns|diff|mockup|info))?(\/full)?$/);
  if (main && (main[3] === undefined || main[2] === "mockup")) {
    const params = { id: main[1] ?? "" };
    if (main[3] !== undefined) return { panel: "mockupFull", params, search: {} };
    const named: Partial<Record<string, PanelId>> = {
      diff: "changes",
      mockup: "mockup",
      info: "info",
    };
    return { panel: named[main[2] ?? ""] ?? "turns", params, search: {} };
  }

  return plain("fleet");
}

// hrefFor is the app's own link to a panel, `#/…`: params holds the path
// values the panel takes (id, agent, job, story, doc) and search its search
// (plan, at), an empty search value left out.
export function hrefFor(
  panel: PanelId,
  params: Record<string, string> = {},
  search: Record<string, string> = {},
): string {
  const seg = (key: string): string => encodeURIComponent(params[key] ?? "");
  const session = `/s/${seg("id")}`;
  const paths: Record<PanelId, string> = {
    fleet: "/",
    turns: session,
    info: `${session}/info`,
    changes: `${session}/diff`,
    mockup: `${session}/mockup`,
    mockupFull: `${session}/mockup/full`,
    agent: `${session}/agent/${seg("agent")}`,
    job: `${session}/job/${seg("job")}`,
    flow: `${session}/flow`,
    flowStory: `${session}/flow/${seg("story")}`,
    flowDoc: `${session}/flow/doc/${seg("doc")}`,
    flows: "/flows",
    flowBoard: "/flow-board",
    new: "/new",
    settings: "/settings",
    pair: "/pair",
  };
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(search)) {
    if (value !== "") query.set(key, value);
  }
  const text = query.toString();
  return "#" + paths[panel] + (text === "" ? "" : "?" + text);
}

// panelProps is what a panel is handed for a route.
export function panelProps(route: RouteState): PanelProps {
  return {
    sessionId: route.params.id === undefined ? undefined : Number(route.params.id),
    params: route.params,
    search: route.search,
  };
}

// settleHash rewrites a slashless or legacy hash in place (`#s/5/turns` is
// `#/s/5`), so the router first reads the app's own form (spec §27). No hash
// entry is added.
export function settleHash(): void {
  const next = normalizeHash(window.location.hash);
  if (next !== window.location.hash) window.history.replaceState(null, "", next);
}

// watchHash does the same for a hash set while the app runs (a push link
// opened in an app that is already open): `location.replace` swaps the
// history entry and fires the `hashchange` the router follows. It returns
// its stop.
export function watchHash(): () => void {
  const onChange = (): void => {
    const next = normalizeHash(window.location.hash);
    if (next !== window.location.hash) window.location.replace(next);
  };
  window.addEventListener("hashchange", onChange);
  return () => {
    window.removeEventListener("hashchange", onChange);
  };
}

// goToPairing opens the pairing route: main.tsx runs it when a request or the
// stream answers 401.
export function goToPairing(): void {
  window.location.hash = hrefFor("pair");
}

// PATHS is each panel's route in the router's own pattern (`$name` is a path
// value). routeOf, not these patterns, decides which panel a hash opens;
// they only give the router something to match.
const PATHS: Record<PanelId, string> = {
  fleet: "/",
  turns: "/s/$id",
  info: "/s/$id/info",
  changes: "/s/$id/diff",
  mockup: "/s/$id/mockup",
  mockupFull: "/s/$id/mockup/full",
  agent: "/s/$id/agent/$agent",
  job: "/s/$id/job/$job",
  flow: "/s/$id/flow",
  flowStory: "/s/$id/flow/$story",
  flowDoc: "/s/$id/flow/doc/$doc",
  flows: "/flows",
  flowBoard: "/flow-board",
  new: "/new",
  settings: "/settings",
  pair: "/pair",
};

// Search is plain strings, written as hrefFor writes it, so a router that
// canonicalises the URL finds nothing to change.
function parseSearch(search: string): Record<string, string> {
  return Object.fromEntries(new URLSearchParams(search));
}

function stringifySearch(search: Record<string, unknown>): string {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(search)) {
    if (typeof value === "string" && value !== "") query.set(key, value);
  }
  const text = query.toString();
  return text === "" ? "" : "?" + text;
}

// RoutedPanel is every route's screen: the panel the hash opens.
function RoutedPanel(): ReactElement {
  const route = useRoute();
  return <PanelHost panel={route.panel} props={panelProps(route)} />;
}

settleHash();

const rootRoute = createRootRoute({ component: () => <AppShell /> });

// router is the app's router: hash history, the shell as the root route and
// one route per panel. A hash no pattern matches draws the fleet's route,
// which is what routeOf answers for it.
export const router = createRouter({
  routeTree: rootRoute.addChildren(
    Object.values(PATHS).map((path) =>
      createRoute({ getParentRoute: () => rootRoute, path, component: RoutedPanel }),
    ),
  ),
  history: createHashHistory(),
  parseSearch,
  stringifySearch,
  defaultNotFoundComponent: RoutedPanel,
});

function subscribeToHash(onChange: () => void): () => void {
  window.addEventListener("hashchange", onChange);
  window.addEventListener("popstate", onChange);
  const stopHistory = router.history.subscribe(onChange);
  return () => {
    window.removeEventListener("hashchange", onChange);
    window.removeEventListener("popstate", onChange);
    stopHistory();
  };
}

// useRoute is the current route as routeOf gives it, re-rendering on each
// navigation.
export function useRoute(): RouteState {
  const hash = useSyncExternalStore(
    subscribeToHash,
    () => window.location.hash,
    () => "",
  );
  return useMemo(() => routeOf(hash), [hash]);
}
