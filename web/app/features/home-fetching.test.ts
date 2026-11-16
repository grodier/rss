import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate, liveRegion } from "../../shared/test/setup";

isolate();

const waitingPage = `
  <main class="column">
    <div class="empty home-empty" data-home-fetching="/timelines/all">
      <div class="spinner"></div>
      <p class="home-empty-lead">Getting the latest from A and B…</p>
    </div>
  </main>`;

const rowsPage = (after: string) => `
  <main class="column">
    <div class="article-list">
      <article class="article-row" id="article-a1"></article>
    </div>
    ${after}
  </main>`;
const endLine = `<p class="timeline-end">That's everything from your 2 sites. <a href="/discover">Find more in Discover</a></p>`;
const pager = `<nav class="pager"><a class="btn" rel="next" href="/timelines/all?before=c1">Older articles</a></nav>`;

let fetchMock: ReturnType<typeof vi.fn>;
let seenInit: ReturnType<typeof vi.fn>;
let scrollInit: ReturnType<typeof vi.fn>;
let enhanceRows: ReturnType<typeof vi.fn>;
let page: string;

beforeEach(() => {
  vi.useFakeTimers();
  document.body.innerHTML = waitingPage;
  page = waitingPage;
  fetchMock = vi.fn(async () => new Response(page, { status: 200 }));
  vi.stubGlobal("fetch", fetchMock);
  seenInit = vi.fn();
  scrollInit = vi.fn();
  enhanceRows = vi.fn();
  vi.doMock("./seen-beacon", () => ({ init: seenInit }));
  vi.doMock("./infinite-scroll", () => ({ init: scrollInit, enhanceRows }));
});

const setup = async () => {
  const mod = await import("./home-fetching");
  mod.init();
  return mod;
};

describe("home-fetching", () => {
  it("does nothing on a page that isn't waiting", async () => {
    document.body.innerHTML = rowsPage(endLine);
    await setup();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("checks Home's own address every INTERVAL, leaving the message", async () => {
    const { INTERVAL } = await setup();
    await vi.advanceTimersByTimeAsync(INTERVAL - 1);
    expect(fetchMock).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0][0]).toBe("/timelines/all");
    await vi.advanceTimersByTimeAsync(INTERVAL);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(document.querySelector("[data-home-fetching] .spinner")).not.toBeNull();
  });

  it("stops after MAX_POLLS and takes the spinner away", async () => {
    const { INTERVAL, MAX_POLLS } = await setup();
    await vi.advanceTimersByTimeAsync(INTERVAL * (MAX_POLLS - 1));
    expect(document.querySelector(".spinner")).not.toBeNull();
    await vi.advanceTimersByTimeAsync(INTERVAL * 6);
    expect(fetchMock).toHaveBeenCalledTimes(MAX_POLLS);
    expect(document.querySelector(".spinner")).toBeNull();
    expect(document.querySelector("[data-home-fetching]")!.textContent).toContain("Getting the latest from A and B…");
  });

  for (const [name, after, cls] of [
    ["the end line", endLine, "timeline-end"],
    ["the pager", pager, "pager"],
  ]) {
    it(`swaps in the rows and ${name}, sets them up, announces them and stops`, async () => {
      const { INTERVAL, ARRIVED } = await setup();
      page = rowsPage(after);
      await vi.advanceTimersByTimeAsync(INTERVAL);
      expect(document.querySelector("[data-home-fetching]")).toBeNull();
      const main = document.querySelector("main")!;
      expect([...main.children].map((el) => el.className)).toEqual(["article-list", cls]);
      expect(enhanceRows).toHaveBeenCalledWith(main.querySelector(".article-list"));
      expect(seenInit).toHaveBeenCalledTimes(1);
      expect(scrollInit).toHaveBeenCalledTimes(1);
      await vi.advanceTimersByTimeAsync(100);
      expect(liveRegion()).toBe(ARRIVED);
      await vi.advanceTimersByTimeAsync(INTERVAL * 3);
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
  }
});
