import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate } from "../../shared/test/setup";

isolate();

// A stand-in IntersectionObserver whose reports the test sends with see().
let observers: FakeObserver[];
class FakeObserver {
  observed = new Set<Element>();
  constructor(
    public callback: IntersectionObserverCallback,
    public options?: IntersectionObserverInit,
  ) {
    observers.push(this);
  }
  observe(el: Element) {
    this.observed.add(el);
  }
  unobserve(el: Element) {
    this.observed.delete(el);
  }
  disconnect() {
    this.observed.clear();
  }
}

// see reports el at ratio (of it in view) to the observer watching it.
const see = (el: Element, ratio: number) => {
  for (const o of observers) {
    if (!o.observed.has(el)) continue;
    const entry = { target: el, isIntersecting: ratio > 0, intersectionRatio: ratio } as IntersectionObserverEntry;
    o.callback([entry], o as unknown as IntersectionObserver);
  }
};

let reduced: boolean;

beforeEach(() => {
  vi.useFakeTimers();
  observers = [];
  reduced = false;
  vi.stubGlobal("IntersectionObserver", FakeObserver);
  vi.spyOn(window, "matchMedia").mockImplementation(
    (query: string) => ({ matches: query === "(prefers-reduced-motion: reduce)" && reduced }) as MediaQueryList,
  );
  document.body.innerHTML = `<div class="article-list"><div class="timeline-divider" role="separator"><span>You were here</span></div></div>`;
});

const divider = () => document.querySelector<HTMLElement>(".timeline-divider")!;
const load = () => import("./divider-draw");

describe("divider-draw", () => {
  it("marks the divider pending and watches for half of it in view", async () => {
    (await load()).init();
    expect(divider().dataset.draw).toBe("pending");
    expect(observers).toHaveLength(1);
    expect(observers[0].options?.threshold).toBe(0.5);
    expect(observers[0].observed.has(divider())).toBe(true);
  });

  it("draws once half of it is in view, after the page has settled", async () => {
    const { init, SETTLE } = await load();
    expect(SETTLE).toBe(200);
    init();
    see(divider(), 0.2);
    expect(divider().dataset.draw).toBe("pending");
    vi.advanceTimersByTime(50);
    see(divider(), 1);
    vi.advanceTimersByTime(SETTLE - 51);
    expect(divider().dataset.draw).toBe("pending");
    vi.advanceTimersByTime(1);
    expect(divider().dataset.draw).toBe("play");
  });

  it("draws at once when it comes into view later, and only once", async () => {
    (await load()).init();
    see(divider(), 0);
    vi.advanceTimersByTime(5000);
    see(divider(), 0.6);
    expect(divider().dataset.draw).toBe("play");
    expect(observers[0].observed.size).toBe(0);
  });

  it("draws a divider appended later the first time it's seen", async () => {
    const { init, enhanceDivider } = await load();
    document.body.innerHTML = `<div class="article-list"></div>`;
    init();
    vi.advanceTimersByTime(5000);
    const el = document.createElement("div");
    el.className = "timeline-divider";
    document.querySelector(".article-list")!.append(el);
    enhanceDivider(el);
    expect(el.dataset.draw).toBe("pending");
    see(el, 1);
    expect(el.dataset.draw).toBe("play");
    enhanceDivider(el);
    expect(el.dataset.draw).toBe("play");
  });

  it("leaves the line drawn under reduced motion", async () => {
    reduced = true;
    const { init, enhanceDivider } = await load();
    init();
    enhanceDivider(divider());
    expect(divider().dataset.draw).toBeUndefined();
    expect(observers).toHaveLength(0);
  });

  it("leaves the line drawn without IntersectionObserver", async () => {
    vi.stubGlobal("IntersectionObserver", undefined);
    // "in window" must be false, not just undefined.
    delete (window as { IntersectionObserver?: unknown }).IntersectionObserver;
    (await load()).init();
    expect(divider().dataset.draw).toBeUndefined();
  });
});
