import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate, pageTransition } from "../../shared/test/setup";

isolate();

let visibility: DocumentVisibilityState;
let beacon: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.useFakeTimers();
  visibility = "visible";
  vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
  beacon = vi.fn(() => true);
  Object.defineProperty(navigator, "sendBeacon", { value: beacon, configurable: true });
  document.body.innerHTML = `<div class="article-list" data-seen-up-to="2026-11-08T10:00:00.000001Z" data-timeline="all"></div>`;
});

const start = async () => (await import("./seen-beacon")).init();
const hide = () => {
  visibility = "hidden";
  document.dispatchEvent(new Event("visibilitychange"));
};
const show = () => {
  visibility = "visible";
  document.dispatchEvent(new Event("visibilitychange"));
};

describe("seen-beacon", () => {
  it("sends only timeline and up_to to /timeline/seen", async () => {
    await start();
    expect(beacon).toHaveBeenCalledTimes(1);
    const [url, body] = beacon.mock.calls[0];
    expect(url).toBe("/timeline/seen");
    expect(body).toBeInstanceOf(URLSearchParams);
    expect([...(body as URLSearchParams)]).toEqual([
      ["timeline", "all"],
      ["up_to", "2026-11-08T10:00:00.000001Z"],
    ]);
  });

  it("does nothing without the list", async () => {
    document.body.innerHTML = `<div class="article-list"></div>`;
    await start();
    hide();
    expect(beacon).not.toHaveBeenCalled();
  });

  it("reports when the page is hidden", async () => {
    await start();
    hide();
    hide();
    expect(beacon).toHaveBeenCalledTimes(3);
  });

  it("reports when a page loaded in the background is first shown, and not again", async () => {
    visibility = "hidden";
    await start();
    expect(beacon).not.toHaveBeenCalled();
    show();
    expect(beacon).toHaveBeenCalledTimes(1);
    hide();
    show();
    expect(beacon).toHaveBeenCalledTimes(2); // the hide's
  });

  it("reports on pageshow from the back/forward cache only", async () => {
    await start();
    pageTransition("pageshow", false);
    expect(beacon).toHaveBeenCalledTimes(1);
    pageTransition("pageshow", true);
    expect(beacon).toHaveBeenCalledTimes(2);
  });

  it("reports every ten minutes while the page is used and visible", async () => {
    const { HEARTBEAT } = await import("./seen-beacon");
    expect(HEARTBEAT).toBe(10 * 60 * 1000);
    await start();
    beacon.mockClear();
    window.dispatchEvent(new Event("scroll"));
    vi.advanceTimersByTime(HEARTBEAT - 1);
    expect(beacon).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(beacon).toHaveBeenCalledTimes(1);
    // Unused for the next ten minutes: no report.
    vi.advanceTimersByTime(HEARTBEAT);
    expect(beacon).toHaveBeenCalledTimes(1);
  });

  it("doesn't report on the heartbeat while the page is hidden", async () => {
    await start();
    window.dispatchEvent(new Event("keydown"));
    visibility = "hidden";
    beacon.mockClear();
    vi.advanceTimersByTime(10 * 60 * 1000);
    expect(beacon).not.toHaveBeenCalled();
  });

  it.each(["scroll", "keydown", "pointerdown"])("counts %s as use", async (type) => {
    await start();
    beacon.mockClear();
    window.dispatchEvent(new Event(type));
    vi.advanceTimersByTime(10 * 60 * 1000);
    expect(beacon).toHaveBeenCalledTimes(1);
  });

  it("doesn't count other events as use", async () => {
    await start();
    beacon.mockClear();
    window.dispatchEvent(new Event("mousemove"));
    vi.advanceTimersByTime(10 * 60 * 1000);
    expect(beacon).not.toHaveBeenCalled();
  });
});
