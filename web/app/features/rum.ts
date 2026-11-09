// Real-user metrics: one beacon per page view to POST /rum, sent when the
// page is first hidden, with its Core Web Vitals (measured by web-vitals),
// its loading timings and coarse context (see "Observability" in
// docs/decisions.md). It carries no identifiers: the page's path, which the
// server turns into its route pattern and drops, another site's referrer as
// a host only, and tag or data-* names, never text or URLs. Nothing is
// stored in the browser, and nothing is sent without sendBeacon. A page
// restored from the back/forward cache is a new view, sent when it's next
// hidden.
import { onCLS, onFCP, onINP, onLCP, onTTFB } from "web-vitals";

// What one view has measured so far.
type View = {
  restored: boolean; // from the back/forward cache
  sent: boolean;
  width: number;
  installed: boolean;
  ttfb?: number;
  fcp?: number;
  lcp?: number;
  inp?: number;
  cls?: number;
  lcpElement?: string;
  inpTarget?: string;
};

// What an interaction's target is named by: the control it's in (an icon's
// <svg> names its button), else the target itself.
const CONTROLS = "a, button, input, select, textarea, summary, label";

export function setupRUM(): void {
  if (typeof navigator.sendBeacon !== "function") return;

  const start = (restored: boolean): View => ({
    restored,
    sent: false,
    width: innerWidth,
    installed: matchMedia("(display-mode: standalone)").matches,
  });
  let view = start(false);

  // A restore starts a new view before web-vitals reports the restored
  // page's metrics (its TTFB at once): capture listeners on window run in
  // the order they were added, and this one is added before web-vitals'.
  addEventListener(
    "pageshow",
    (e) => {
      if (e.persisted) view = start(true);
    },
    { capture: true },
  );

  // Every change, so a view always holds the latest values. web-vitals
  // settles them when the page is hidden, in a visibilitychange listener
  // that captures on window, so it runs before the one on document below.
  const opts = { reportAllChanges: true };
  onTTFB((m) => {
    view.ttfb = m.value;
  }, opts);
  onFCP((m) => {
    view.fcp = m.value;
  }, opts);
  onLCP((m) => {
    view.lcp = m.value;
    view.lcpElement = m.entries.at(-1)?.element?.localName;
  }, opts);
  onINP((m) => {
    view.inp = m.value;
    view.inpTarget = targetName(m.entries);
  }, opts);
  onCLS((m) => {
    view.cls = m.value;
  }, opts);

  const send = () => {
    if (view.sent) return;
    view.sent = true;
    navigator.sendBeacon("/rum", fields(view));
  };
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") send();
  });
  // Where the page is closed without visibilitychange.
  addEventListener("pagehide", send);
}

// fields returns the beacon's fields (rumForm in internal/server/rum.go),
// leaving out what the browser didn't report. A restored view has no
// navigation of its own, so no loading timings, protocol or referrer.
function fields(view: View): URLSearchParams {
  const nav = view.restored
    ? undefined
    : (performance.getEntriesByType("navigation")[0] as PerformanceNavigationTiming | undefined);
  const values: Record<string, string | number | undefined> = {
    path: location.pathname,
    nav: view.restored ? "restore" : navigationType(nav),
    width: view.width,
    installed: view.installed ? 1 : 0,
    connection: navigator.connection?.effectiveType,
    protocol: nav?.nextHopProtocol,
    referrer: view.restored ? undefined : referrerHost(),
    ttfb: ms(view.ttfb),
    fcp: ms(view.fcp),
    lcp: ms(view.lcp),
    inp: ms(view.inp),
    cls: view.cls === undefined ? undefined : Math.round(view.cls * 10_000) / 10_000,
    dcl: nav && ms(sinceActivation(nav, nav.domContentLoadedEventEnd)),
    load: nav && ms(sinceActivation(nav, nav.loadEventEnd)),
    server: ms(nav?.serverTiming?.find((t) => t.name === "app")?.duration),
    transfer: nav?.transferSize,
    lcp_element: view.lcpElement,
    inp_target: view.inpTarget,
  };
  const params = new URLSearchParams();
  for (const [name, value] of Object.entries(values)) {
    if (value !== undefined && value !== "") params.set(name, String(value));
  }
  return params;
}

// navigationType is the navigation entry's type, or "prerender" for a
// prerendered page (which the entry calls "navigate").
function navigationType(nav: PerformanceNavigationTiming | undefined): string {
  if (!nav) return "navigate";
  return (nav.activationStart ?? 0) > 0 ? "prerender" : nav.type;
}

// sinceActivation returns a navigation timing from when a prerendered page
// was shown (as web-vitals measures), or undefined before it happened.
function sinceActivation(nav: PerformanceNavigationTiming, t: number): number | undefined {
  return t > 0 ? Math.max(t - (nav.activationStart ?? 0), 0) : undefined;
}

// ms rounds a time to a tenth of a millisecond.
function ms(t: number | undefined): number | undefined {
  return t === undefined ? undefined : Math.round(t * 10) / 10;
}

// referrerHost returns the host of the page that linked here, unless it's
// one of ours.
function referrerHost(): string | undefined {
  if (!document.referrer) return undefined;
  try {
    const host = new URL(document.referrer).hostname;
    return host === location.hostname ? undefined : host;
  } catch {
    return undefined;
  }
}

// targetName names the slowest interaction's target by its control's first
// data-* attribute (an enhancement's hook), else its tag name.
function targetName(entries: PerformanceEventTiming[]): string | undefined {
  for (const entry of entries) {
    if (!(entry.target instanceof Element)) continue;
    const el = entry.target.closest(CONTROLS) ?? entry.target;
    return el.getAttributeNames().find((name) => name.startsWith("data-")) ?? el.localName;
  }
  return undefined;
}
