import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate, liveRegion, pageTransition } from "../../shared/test/setup";

isolate();

// The observers infinite-scroll makes, so a test can say what's in view.
let observers: { callback: IntersectionObserverCallback; targets: Element[]; options?: IntersectionObserverInit }[];

class FakeObserver {
  targets: Element[] = [];
  constructor(callback: IntersectionObserverCallback, options?: IntersectionObserverInit) {
    observers.push({ callback, targets: this.targets, options });
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

  it("fetches the next page without keeping a copy", async () => {
    await loadNext(`<main><div class="article-list">${row("a2")}</div></main>`);
    expect(fetchMock).toHaveBeenCalledWith(new URL("/timelines/all?before=c1", location.href).href, {
      credentials: "same-origin",
      cache: "no-store",
      signal: expect.any(AbortSignal),
    });
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

const NEXT_PAGE = `<main>
  <div class="article-list">${row("a2")}</div>
  <nav class="pager"><a class="btn" rel="next" href="/timelines/all?before=c2">Older articles</a></nav>
</main>`;
const HREF = "/timelines/all?before=c1";

const list = () => document.querySelector(".article-list")!;
const pager = () => document.querySelector<HTMLElement>(".pager")!;
const link = () => document.querySelector<HTMLAnchorElement>(".pager a[rel=next]")!;
const loader = () => observers[observers.length - 1];
// reach tells the loader its link is in view.
const reach = () =>
  loader().callback(
    [{ isIntersecting: true, target: loader().targets[0] } as unknown as IntersectionObserverEntry],
    {} as never,
  );
// press clicks the link and returns whether the click was prevented.
const press = (init: MouseEventInit = {}) => {
  const e = new MouseEvent("click", { bubbles: true, cancelable: true, button: 0, ...init });
  link().dispatchEvent(e);
  return e.defaultPrevented;
};
const redirected = (body: string) => {
  const res = new Response(body, { status: 200 });
  Object.defineProperty(res, "redirected", { value: true });
  return res;
};
// fail reaches the link with fetch answering as given, and waits for the
// failure to show.
const fail = async (answer: () => void) => {
  answer();
  reach();
  await vi.waitFor(() => expect(pager().dataset.state).toBe("failed"));
};

describe("infinite-scroll while loading", () => {
  it("marks the list busy, and shows it's loading on the link only after --delay-busy", async () => {
    vi.useFakeTimers();
    let finish!: (r: Response) => void;
    fetchMock.mockReturnValueOnce(new Promise<Response>((r) => (finish = r)));
    reach();
    expect(list().getAttribute("aria-busy")).toBe("true");
    await vi.advanceTimersByTimeAsync(299);
    expect(link().textContent).toBe("Older articles");
    await vi.advanceTimersByTimeAsync(1);
    expect(pager().dataset.state).toBe("busy");
    expect(link().textContent).toBe("Loading older articles…");
    expect(link().firstElementChild?.matches(".spinner.btn-spinner[aria-hidden=true]")).toBe(true);
    expect(link().getAttribute("href")).toBe(HREF);
    expect(press()).toBe(true);
    expect(press({ ctrlKey: true })).toBe(false);
    finish(new Response(NEXT_PAGE, { status: 200 }));
    await vi.waitFor(() => expect(document.querySelectorAll(".article-row")).toHaveLength(2));
    expect(list().hasAttribute("aria-busy")).toBe(false);
  });

  it("never shows the busy label when the page arrives first", async () => {
    vi.useFakeTimers();
    fetchMock.mockResolvedValueOnce(new Response(NEXT_PAGE, { status: 200 }));
    reach();
    await vi.waitFor(() => expect(document.querySelectorAll(".article-row")).toHaveLength(2));
    await vi.advanceTimersByTimeAsync(1000);
    expect(link().textContent).toBe("Older articles");
    expect(pager().dataset.state).toBeUndefined();
  });

  it("ignores a plain press while loading, but not one with a modifier", async () => {
    fetchMock.mockReturnValueOnce(new Promise(() => {}));
    reach();
    expect(press()).toBe(true);
    expect(press({ ctrlKey: true })).toBe(false);
    expect(press({ metaKey: true })).toBe(false);
    expect(press({ shiftKey: true })).toBe(false);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("follows the link on a plain press before anything loads", () => {
    expect(press()).toBe(false);
  });

  it("names the states after the link's own words", async () => {
    vi.useFakeTimers();
    link().textContent = "Older posts";
    // watch read the words at init, so start again with the new label.
    observers = [];
    (await import("./infinite-scroll")).init();
    fetchMock.mockReturnValueOnce(new Promise(() => {}));
    reach();
    await vi.advanceTimersByTimeAsync(300);
    expect(link().textContent).toBe("Loading older posts…");
  });
});

describe("infinite-scroll when a load fails", () => {
  for (const [name, answer] of [
    ["a network error", () => fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch"))],
    ["a 500", () => fetchMock.mockResolvedValueOnce(new Response("oops", { status: 500 }))],
    ["a redirect", () => fetchMock.mockResolvedValueOnce(redirected("<main><form></form></main>"))],
  ] as const) {
    it(`says so after ${name}, offers Try again and stops loading by itself`, async () => {
      vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
      await fail(answer);
      expect(pager().querySelector(".pager-error")?.textContent).toBe("Couldn't load older articles.");
      expect(pager().firstElementChild?.matches(".pager-error")).toBe(true);
      expect(link().textContent).toBe("Try again");
      expect(link().getAttribute("href")).toBe(HREF);
      expect(list().hasAttribute("aria-busy")).toBe(false);
      expect(loader().targets).toHaveLength(0);
      await vi.advanceTimersByTimeAsync(1000);
      expect(liveRegion()).toBe("Couldn't load older articles.");
      expect(document.querySelectorAll("p[aria-live]")).toHaveLength(1);
      expect(link().textContent).toBe("Try again"); // the busy timer was stopped
    });
  }

  it("gives up on a load that takes 20 s", async () => {
    // happy-dom's AbortSignal.timeout doesn't use the fake timers.
    const ctrl = new AbortController();
    const timeout = vi.spyOn(AbortSignal, "timeout").mockReturnValue(ctrl.signal);
    fetchMock.mockImplementationOnce(
      (_url: string, init: RequestInit) =>
        new Promise((_resolve, reject) => init.signal!.addEventListener("abort", () => reject(init.signal!.reason))),
    );
    reach();
    expect(timeout).toHaveBeenCalledWith(20_000);
    ctrl.abort(new DOMException("The operation timed out.", "TimeoutError"));
    await vi.waitFor(() => expect(pager().dataset.state).toBe("failed"));
    expect(link().textContent).toBe("Try again");
  });

  it("appends the rows when Try again works, and loads by itself again", async () => {
    await fail(() => fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch")));
    fetchMock.mockResolvedValueOnce(new Response(NEXT_PAGE, { status: 200 }));
    expect(press()).toBe(true);
    expect(link().textContent).toBe("Loading older articles…");
    expect(pager().querySelector(".pager-error")).toBeNull();
    await vi.waitFor(() => expect(document.querySelectorAll(".article-row")).toHaveLength(2));
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(document.querySelectorAll(".pager")).toHaveLength(1);
    expect(link().getAttribute("href")).toBe("/timelines/all?before=c2");
    expect(loader().targets).toEqual([link()]);
  });

  it("moves focus to the first new row when Try again had it", async () => {
    document.body.querySelector(".article-list")!.innerHTML = row("a1");
    await fail(() => fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch")));
    fetchMock.mockResolvedValueOnce(
      new Response(
        `<main><div class="article-list"><article class="article-row" id="article-a2"><h3 class="article-title"><a href="/articles/a2">A2</a></h3></article></div></main>`,
        { status: 200 },
      ),
    );
    link().focus();
    press();
    await vi.waitFor(() => expect(document.activeElement?.getAttribute("href")).toBe("/articles/a2"));
  });

  it("fails again in place when Try again can't reach the server", async () => {
    await fail(() => fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch")));
    fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    press();
    await vi.waitFor(() => expect(link().textContent).toBe("Try again"));
    expect(pager().querySelectorAll(".pager-error")).toHaveLength(1);
  });

  for (const [name, res] of [
    ["a redirect", () => redirected("<main><form></form></main>")],
    ["a 500", () => new Response("oops", { status: 500 })],
  ] as const) {
    it(`follows the link when Try again gets ${name}`, async () => {
      const assign = vi.fn();
      vi.stubGlobal("location", { ...location, href: location.href, assign });
      await fail(() => fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch")));
      fetchMock.mockResolvedValueOnce(res());
      press();
      await vi.waitFor(() => expect(assign).toHaveBeenCalledWith(link().href));
      expect(link().getAttribute("href")).toBe(HREF);
    });
  }

  it("shows the failure again on Back to a page restored after Try again followed the link", async () => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    vi.stubGlobal("location", { ...location, href: location.href, assign: vi.fn() });
    await fail(() => fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch")));
    fetchMock.mockResolvedValueOnce(redirected("<main><form></form></main>"));
    press();
    await vi.waitFor(() => expect(location.assign).toHaveBeenCalled());
    await vi.advanceTimersByTimeAsync(300);
    expect(link().textContent).toBe("Loading older articles…"); // while the next page loads
    pageTransition("pageshow", true);
    expect(pager().dataset.state).toBe("failed");
    expect(link().textContent).toBe("Try again");
    expect(pager().querySelectorAll(".pager-error")).toHaveLength(1);
    expect(list().hasAttribute("aria-busy")).toBe(false);
    fetchMock.mockResolvedValueOnce(new Response(NEXT_PAGE, { status: 200 }));
    press();
    await vi.waitFor(() => expect(document.querySelectorAll(".article-row")).toHaveLength(2));
  });

  it("follows the link on a press with a modifier", async () => {
    await fail(() => fetchMock.mockRejectedValueOnce(new TypeError("Failed to fetch")));
    expect(press({ metaKey: true })).toBe(false);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

// A row as article_row.html renders it, with the link that opens it.
const articleRow = (id: string, style = "") =>
  `<article class="article-row" id="article-${id}"${style}><div class="article-card"><h3 class="article-title"><a href="/articles/${id}">${id}</a></h3></div></article>`;

// start renders a page of rows at url, with the history entry's state,
// and sets infinite scroll up on it.
const start = async (url: string, rowsHTML: string, state: unknown = null) => {
  history.replaceState(state, "", url);
  document.body.innerHTML = `<main><div class="article-list">${rowsHTML}</div></main>`;
  observers = [];
  (await import("./infinite-scroll")).init();
};

// rowAt makes a row report its distance from the top of the screen.
const rowAt = (id: string, top: number) =>
  vi.spyOn(document.getElementById(`article-${id}`)!, "getBoundingClientRect").mockReturnValue({ top } as DOMRect);

// showRows tells the rows' observer which rows came into view, and lets
// the URL catch up.
const showRows = async (...ids: string[]) => {
  observers[0].callback(
    ids.map(
      (id) =>
        ({
          isIntersecting: true,
          target: document.getElementById(`article-${id}`),
        }) as unknown as IntersectionObserverEntry,
    ),
    {} as never,
  );
  await vi.advanceTimersByTimeAsync(300);
};

// open presses a row's link, with the event's other fields as given, and
// returns the event; the browser's own navigation never runs.
const open = (id: string, init: MouseEventInit = {}) => {
  const e = new MouseEvent("click", { bubbles: true, cancelable: true, button: 0, ...init });
  document.addEventListener("click", (ev) => ev.preventDefault(), { once: true });
  document.querySelector(`#article-${id} .article-card a`)!.dispatchEvent(e);
  return e;
};

describe("infinite-scroll's URL", () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
  });

  it("takes over restoring the scroll", async () => {
    await start("/timelines/all", articleRow("a1"));
    expect(history.scrollRestoration).toBe("manual");
  });

  it("follows the topmost visible row, but stays plain on the first", async () => {
    await start("/timelines/all", articleRow("a1") + articleRow("a2") + articleRow("a3"));
    rowAt("a1", 0);
    rowAt("a2", 200);
    rowAt("a3", 400);
    await showRows("a2", "a3");
    expect(location.pathname + location.hash).toBe("/timelines/all#article-a2");
    expect(history.state).toBeNull();
    await showRows("a1");
    expect(location.pathname + location.hash).toBe("/timelines/all");
  });

  it("doesn't count a row under the sticky bars as visible", async () => {
    await start("/timelines/all", articleRow("a1", ` style="scroll-margin-top: 72px"`));
    expect(observers[0].options?.rootMargin).toBe("-73px 0px 0px 0px");
  });

  it("anchors on an opened article's row, with where it was on the screen", async () => {
    await start("/timelines/all", articleRow("a1") + articleRow("a2") + articleRow("a3"));
    rowAt("a2", 120);
    rowAt("a3", 340);
    await showRows("a2", "a3");
    open("a3");
    expect(location.pathname + location.hash).toBe("/timelines/all#article-a3");
    expect(history.state).toEqual({ row: "article-a3", top: 340 });
  });

  it("keeps the plain URL for the first row's article", async () => {
    await start("/timelines/all", articleRow("a1") + articleRow("a2"));
    rowAt("a2", 120);
    await showRows("a2");
    open("a1");
    expect(location.pathname + location.hash).toBe("/timelines/all");
    expect(history.state).toBeNull();
  });

  it("leaves the URL alone for a press with a modifier or another button", async () => {
    await start("/timelines/all", articleRow("a1") + articleRow("a2") + articleRow("a3"));
    rowAt("a2", 120);
    await showRows("a2");
    open("a3", { metaKey: true });
    open("a3", { button: 1 });
    expect(location.hash).toBe("#article-a2");
    expect(history.state).toBeNull();
  });

  it("holds the opened row until the page is restored, then follows the rows again", async () => {
    await start("/timelines/all", articleRow("a1") + articleRow("a2") + articleRow("a3"));
    rowAt("a2", 120);
    rowAt("a3", 340);
    open("a3");
    await showRows("a2");
    expect(location.hash).toBe("#article-a3");
    pageTransition("pageshow", true);
    await showRows("a2");
    expect(location.hash).toBe("#article-a2");
  });

  it("puts an opened article's row back where it was on the screen", async () => {
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue({ top: 900 } as DOMRect);
    const scroll = vi.spyOn(window, "scrollTo").mockImplementation(() => {});
    await start("/timelines/all?before=c1#article-a2", articleRow("a1") + articleRow("a2"), {
      row: "article-a2",
      top: 340,
    });
    expect(scroll).toHaveBeenCalledWith(0, scrollY + 900 - 340);
  });

  it("scrolls to the top of a row the URL names without saying where it was", async () => {
    const into = vi.spyOn(Element.prototype, "scrollIntoView").mockImplementation(() => {});
    const scroll = vi.spyOn(window, "scrollTo").mockImplementation(() => {});
    await start("/timelines/all?before=c1#article-a2", articleRow("a1") + articleRow("a2"), {
      row: "article-a1",
      top: 340,
    });
    expect(into.mock.contexts).toEqual([document.getElementById("article-a2")]);
    expect(into).toHaveBeenCalledWith({ block: "start" });
    expect(scroll).not.toHaveBeenCalled();
  });

  it("leaves the scroll alone when the URL names no row", async () => {
    const into = vi.spyOn(Element.prototype, "scrollIntoView").mockImplementation(() => {});
    const scroll = vi.spyOn(window, "scrollTo").mockImplementation(() => {});
    await start("/timelines/all#elsewhere", `${articleRow("a1")}<p id="elsewhere"></p>`);
    expect(into).not.toHaveBeenCalled();
    expect(scroll).not.toHaveBeenCalled();
  });
});
