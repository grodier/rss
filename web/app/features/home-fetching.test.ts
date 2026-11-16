import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate } from "../../shared/test/setup";

isolate();

const waitingPage = `
  <main class="column">
    <div class="empty home-empty" data-home-fetching>
      <div class="spinner"></div>
      <p class="home-empty-lead">Getting the latest from A and B…</p>
    </div>
  </main>`;

const rowsPage = `
  <main class="column">
    <div class="article-list">
      <article class="article-row" id="article-a1"><button data-share-url="/a1" hidden>Share</button></article>
    </div>
    <p class="timeline-end">That's everything from your 2 sites. <a href="/discover">Find more in Discover</a></p>
  </main>`;

let fetchMock: ReturnType<typeof vi.fn>;
let seenInit: ReturnType<typeof vi.fn>;
let scrollInit: ReturnType<typeof vi.fn>;
let page: string;

beforeEach(() => {
  vi.useFakeTimers();
  document.body.innerHTML = waitingPage;
  page = waitingPage;
  fetchMock = vi.fn(async () => new Response(page, { status: 200 }));
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("navigator", { ...navigator, share: () => Promise.resolve() });
  seenInit = vi.fn();
  scrollInit = vi.fn();
  vi.doMock("./seen-beacon", () => ({ init: seenInit }));
  vi.doMock("./infinite-scroll", () => ({ init: scrollInit }));
});

const setup = async () => {
  const mod = await import("./home-fetching");
  mod.init();
  return mod;
};

describe("home-fetching", () => {
  it("does nothing on a page that isn't waiting", async () => {
    document.body.innerHTML = rowsPage;
    await setup();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("refetches the page every INTERVAL while it's visible, leaving the message", async () => {
    const { INTERVAL } = await setup();
    await vi.advanceTimersByTimeAsync(INTERVAL - 1);
    expect(fetchMock).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0][0]).toBe(location.href);
    await vi.advanceTimersByTimeAsync(INTERVAL);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(document.querySelector("[data-home-fetching]")).not.toBeNull();
  });

  it("waits while the page is hidden", async () => {
    const { INTERVAL } = await setup();
    Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
    try {
      await vi.advanceTimersByTimeAsync(INTERVAL * 3);
      expect(fetchMock).not.toHaveBeenCalled();
    } finally {
      delete (document as { hidden?: boolean }).hidden;
    }
  });

  it("stops after MAX_POLLS", async () => {
    const { INTERVAL, MAX_POLLS } = await setup();
    await vi.advanceTimersByTimeAsync(INTERVAL * (MAX_POLLS + 5));
    expect(fetchMock).toHaveBeenCalledTimes(MAX_POLLS);
  });

  it("swaps in the rows and the end line, sets them up, and stops", async () => {
    const { INTERVAL } = await setup();
    page = rowsPage;
    await vi.advanceTimersByTimeAsync(INTERVAL);
    expect(document.querySelector("[data-home-fetching]")).toBeNull();
    const main = document.querySelector("main")!;
    expect([...main.children].map((el) => el.className)).toEqual(["article-list", "timeline-end"]);
    expect(main.querySelector<HTMLButtonElement>("button[data-share-url]")!.hidden).toBe(false);
    expect(seenInit).toHaveBeenCalledTimes(1);
    expect(scrollInit).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(INTERVAL * 3);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});
