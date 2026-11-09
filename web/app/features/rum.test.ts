import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate, pageTransition } from "../../shared/test/setup";

isolate();

type Report = (metric: { value: number; entries: unknown[] }) => void;

// web-vitals is mocked: each on* function keeps its callback here, by
// metric, so a test reports values as the library would.
let reports: Record<string, Report>;
let visibility: DocumentVisibilityState;
let beacon: ReturnType<typeof vi.fn>;
let navigation: Partial<PerformanceNavigationTiming> | undefined;

beforeEach(() => {
  reports = {};
  vi.doMock("web-vitals", () =>
    Object.fromEntries(
      ["onTTFB", "onFCP", "onLCP", "onINP", "onCLS"].map((fn) => [
        fn,
        (report: Report, opts: { reportAllChanges?: boolean }) => {
          // Every change, or a view could miss the latest values.
          expect(opts).toEqual({ reportAllChanges: true });
          reports[fn.slice(2)] = report;
        },
      ]),
    ),
  );
  visibility = "visible";
  vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
  beacon = vi.fn(() => true);
  Object.defineProperty(navigator, "sendBeacon", { value: beacon, configurable: true });
  Object.defineProperty(navigator, "connection", { value: undefined, configurable: true });
  navigation = undefined;
  // web-vitals' types narrow getEntriesByType's overloads; the mock returns
  // only a navigation entry.
  const entries = (type: string) => (type === "navigation" && navigation ? [navigation] : []);
  vi.spyOn(performance, "getEntriesByType").mockImplementation(entries as typeof performance.getEntriesByType);
  vi.stubGlobal("innerWidth", 1280);
  history.replaceState(null, "", "/feeds/0b8e7d2c-4f1a-4a8e-9a57-2f1c3b7d9e10");
});

const start = async () => (await import("./rum")).setupRUM();
const hide = () => {
  visibility = "hidden";
  document.dispatchEvent(new Event("visibilitychange"));
};
const show = () => {
  visibility = "visible";
  document.dispatchEvent(new Event("visibilitychange"));
};
// sent returns the fields of the beacons sent so far, checking each went
// to /rum.
const sent = () =>
  beacon.mock.calls.map(([url, body]) => {
    expect(url).toBe("/rum");
    expect(body).toBeInstanceOf(URLSearchParams);
    return Object.fromEntries(body as URLSearchParams);
  });
const element = (html: string, selector: string) => {
  document.body.innerHTML = html;
  return document.querySelector(selector);
};

describe("rum", () => {
  it("sends one beacon, when the page is first hidden", async () => {
    await start();
    expect(beacon).not.toHaveBeenCalled();
    hide();
    expect(beacon).toHaveBeenCalledTimes(1);
    show();
    hide();
    pageTransition("pagehide", false);
    expect(beacon).toHaveBeenCalledTimes(1);
  });

  it("sends on pagehide when the page wasn't hidden first", async () => {
    await start();
    pageTransition("pagehide", false);
    expect(beacon).toHaveBeenCalledTimes(1);
    hide();
    expect(beacon).toHaveBeenCalledTimes(1);
  });

  it("sends the latest metrics, timings and context", async () => {
    navigation = {
      type: "navigate",
      activationStart: 0,
      domContentLoadedEventEnd: 420.26,
      loadEventEnd: 980.04,
      transferSize: 12345,
      nextHopProtocol: "h2",
      serverTiming: [
        { name: "db", duration: 3, description: "" } as PerformanceServerTiming,
        { name: "app", duration: 12.34, description: "" } as PerformanceServerTiming,
      ],
    };
    vi.stubGlobal("innerWidth", 390);
    vi.spyOn(window, "matchMedia").mockImplementation(
      (query) => ({ matches: query === "(display-mode: standalone)" }) as MediaQueryList,
    );
    Object.defineProperty(navigator, "connection", { value: { effectiveType: "4g" }, configurable: true });
    vi.spyOn(document, "referrer", "get").mockReturnValue("https://news.example.com/item?id=123");
    await start();

    reports.TTFB({ value: 120.54, entries: [] });
    reports.FCP({ value: 300, entries: [] });
    reports.LCP({ value: 450, entries: [{ element: element(`<p>Text</p>`, "p") }] });
    reports.LCP({ value: 610.25, entries: [{ element: element(`<img src="/a.jpg" alt="">`, "img") }] });
    const button = element(
      `<button data-share-url="https://example.com/a" data-share-title="A"><svg></svg></button>`,
      "svg",
    );
    reports.INP({ value: 96, entries: [{ target: null }, { target: button }] });
    reports.CLS({ value: 0.012345, entries: [] });
    reports.CLS({ value: 0.05, entries: [] });
    hide();

    expect(sent()).toEqual([
      {
        path: "/feeds/0b8e7d2c-4f1a-4a8e-9a57-2f1c3b7d9e10",
        nav: "navigate",
        width: "390",
        installed: "1",
        connection: "4g",
        protocol: "h2",
        referrer: "news.example.com",
        ttfb: "120.5",
        fcp: "300",
        lcp: "610.3",
        inp: "96",
        cls: "0.05",
        dcl: "420.3",
        load: "980",
        server: "12.3",
        transfer: "12345",
        lcp_element: "img",
        inp_target: "data-share-url",
      },
    ]);
  });

  it("leaves out what the browser didn't report", async () => {
    await start();
    hide();
    expect(sent()).toEqual([
      { path: "/feeds/0b8e7d2c-4f1a-4a8e-9a57-2f1c3b7d9e10", nav: "navigate", width: "1280", installed: "0" },
    ]);
  });

  it("leaves out load timings that hadn't happened", async () => {
    navigation = { type: "reload", domContentLoadedEventEnd: 200, loadEventEnd: 0, nextHopProtocol: "" };
    await start();
    hide();
    const [fields] = sent();
    expect(fields.nav).toBe("reload");
    expect(fields.dcl).toBe("200");
    expect(fields).not.toHaveProperty("load");
    expect(fields).not.toHaveProperty("protocol");
  });

  it("counts a prerendered page's timings from when it was shown", async () => {
    navigation = { type: "navigate", activationStart: 1000, domContentLoadedEventEnd: 400, loadEventEnd: 1500 };
    await start();
    hide();
    const [fields] = sent();
    expect(fields.nav).toBe("prerender");
    expect(fields.dcl).toBe("0");
    expect(fields.load).toBe("500");
  });

  it("names the interaction's target by its tag when its control has no data-*", async () => {
    await start();
    reports.INP({ value: 250, entries: [{ target: element(`<p><a href="/x"><span>Read</span></a></p>`, "span") }] });
    hide();
    expect(sent()[0].inp_target).toBe("a");
  });

  it("names no target when the interaction's was removed", async () => {
    await start();
    reports.INP({ value: 250, entries: [{ target: null }] });
    hide();
    expect(sent()[0]).not.toHaveProperty("inp_target");
  });

  it("doesn't send a referrer from this site", async () => {
    vi.spyOn(document, "referrer", "get").mockReturnValue(`${location.origin}/timelines/all`);
    await start();
    hide();
    expect(sent()[0]).not.toHaveProperty("referrer");
  });

  it("sends a page restored from the back/forward cache as a new view", async () => {
    navigation = {
      type: "navigate",
      domContentLoadedEventEnd: 400,
      loadEventEnd: 900,
      transferSize: 5000,
      nextHopProtocol: "h3",
    };
    vi.spyOn(document, "referrer", "get").mockReturnValue("https://news.example.com/");
    await start();
    reports.INP({ value: 300, entries: [] });
    hide();
    pageTransition("pageshow", true);
    show();
    reports.TTFB({ value: 0, entries: [] });
    reports.CLS({ value: 0, entries: [] });
    hide();
    hide();

    const views = sent();
    expect(views).toHaveLength(2);
    expect(views[0].nav).toBe("navigate");
    expect(views[1]).toEqual({
      path: "/feeds/0b8e7d2c-4f1a-4a8e-9a57-2f1c3b7d9e10",
      nav: "restore",
      width: "1280",
      installed: "0",
      ttfb: "0",
      cls: "0",
    });
  });

  it("doesn't count a pageshow that isn't a restore as a new view", async () => {
    await start();
    hide();
    pageTransition("pageshow", false);
    show();
    hide();
    expect(beacon).toHaveBeenCalledTimes(1);
  });

  it("sends nothing without sendBeacon", async () => {
    Object.defineProperty(navigator, "sendBeacon", { value: undefined, configurable: true });
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    await start();
    hide();
    pageTransition("pagehide", false);
    expect(fetch).not.toHaveBeenCalled();
    expect(reports).toEqual({});
  });
});
