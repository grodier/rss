import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate } from "../../shared/test/setup";

isolate();

// The observers infinite-scroll makes, so a test can say what's in view.
let observers: { callback: IntersectionObserverCallback; targets: Element[] }[];

class FakeObserver {
  targets: Element[] = [];
  constructor(callback: IntersectionObserverCallback) {
    observers.push({ callback, targets: this.targets });
  }
  observe(el: Element) {
    this.targets.push(el);
  }
  unobserve(el: Element) {
    this.targets.splice(this.targets.indexOf(el), 1);
  }
  disconnect() {
    this.targets.length = 0;
  }
}

const row = (id: string) => `<article class="article-row" id="article-${id}"></article>`;

let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(async () => {
  observers = [];
  vi.stubGlobal("IntersectionObserver", FakeObserver);
  document.body.innerHTML = `
    <main>
      <div class="article-list">${row("a1")}</div>
      <nav class="pager"><a class="btn" rel="next" href="/timelines/all?before=c1">Older articles</a></nav>
    </main>`;
  fetchMock = vi.fn();
  vi.stubGlobal("fetch", fetchMock);
  vi.doMock("./divider-draw", () => ({ enhanceDivider: () => {} }));
  (await import("./infinite-scroll")).init();
});

// loadNext tells the loader (the last observer made) its link is in view,
// and waits for the page to be appended.
const loadNext = async (page: string) => {
  fetchMock.mockResolvedValueOnce(new Response(page, { status: 200 }));
  const loader = observers[observers.length - 1];
  const link = loader.targets[0];
  loader.callback([{ isIntersecting: true, target: link } as unknown as IntersectionObserverEntry], {} as never);
  await vi.waitFor(() => expect(document.contains(link)).toBe(false));
};

describe("infinite-scroll", () => {
  it("appends the next page's rows and its pager", async () => {
    await loadNext(`<main>
      <div class="article-list">${row("a2")}</div>
      <nav class="pager"><a class="btn" rel="next" href="/timelines/all?before=c2">Older articles</a></nav>
    </main>`);
    expect([...document.querySelectorAll(".article-row")].map((el) => el.id)).toEqual(["article-a1", "article-a2"]);
    expect(document.querySelector<HTMLAnchorElement>(".pager a")!.getAttribute("href")).toBe(
      "/timelines/all?before=c2",
    );
  });

  it("puts the last page's end line where the pager was", async () => {
    await loadNext(`<main>
      <div class="article-list">${row("a2")}</div>
      <p class="timeline-end">That's everything from your 2 sites. <a href="/discover">Find more in Discover</a></p>
    </main>`);
    expect(document.querySelector(".pager")).toBeNull();
    const end = document.querySelector("main > .timeline-end");
    expect(end?.previousElementSibling?.className).toBe("article-list");
  });

  it("removes the pager after a last page without an end line", async () => {
    await loadNext(`<main><div class="article-list">${row("a2")}</div></main>`);
    expect(document.querySelector(".pager, .timeline-end")).toBeNull();
    expect(document.querySelectorAll(".article-row")).toHaveLength(2);
  });
});
