import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate, liveRegion } from "../../shared/test/setup";

isolate();

let fetchMock: ReturnType<typeof vi.fn>;

const page = (results: number) =>
  `<main><div data-search-results>${'<div class="result">Site</div>'.repeat(results)}</div></main>`;

beforeEach(async () => {
  vi.useFakeTimers();
  history.replaceState(null, "", "/search");
  document.body.innerHTML = `
    <form action="/search" method="get" data-live-search>
      <span class="error">Type at least 2 characters.</span>
      <div class="search-field"><svg class="icon"></svg><input type="search" name="q"></div>
    </form>
    <div data-search-results><p>Earlier results</p></div>`;
  fetchMock = vi.fn(async () => new Response(page(2), { status: 200 }));
  vi.stubGlobal("fetch", fetchMock);
  (await import("./live-search")).init();
});

const input = () => document.querySelector<HTMLInputElement>("input[name=q]")!;
const results = () => document.querySelector<HTMLElement>("[data-search-results]")!;
const spinner = () => document.querySelector<HTMLElement>(".search-spinner")!;
const type = (value: string) => {
  input().value = value;
  input().dispatchEvent(new Event("input", { bubbles: true }));
};
// deferred makes fetch wait until the test responds.
const deferred = () => {
  const calls: ((r: Response) => void)[] = [];
  fetchMock.mockImplementation(() => new Promise<Response>((resolve) => calls.push(resolve)));
  return calls;
};

describe("live-search", () => {
  it("puts a spinner in the search field", () => {
    expect(spinner().parentElement!.classList.contains("search-field")).toBe(true);
    expect(spinner().getAttribute("aria-hidden")).toBe("true");
  });

  it("fetches nothing under 2 characters", async () => {
    type("a");
    type(" é ");
    await vi.advanceTimersByTimeAsync(1000);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("fetches once, about 250 ms after the last keystroke", async () => {
    type("lo");
    await vi.advanceTimersByTimeAsync(200);
    type("loa");
    await vi.advanceTimersByTimeAsync(249);
    expect(fetchMock).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe(new URL("/search?q=loa", location.href).href);
    expect(init.credentials).toBe("same-origin");
  });

  it("aborts a request still in flight", async () => {
    deferred();
    type("loa");
    await vi.advanceTimersByTimeAsync(250);
    const first: AbortSignal = fetchMock.mock.calls[0][1].signal;
    type("load");
    expect(first.aborted).toBe(true);
    await vi.advanceTimersByTimeAsync(250);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("swaps in the results, follows the query in the URL and announces the count", async () => {
    type("load");
    await vi.advanceTimersByTimeAsync(250);
    expect(results().querySelectorAll(".result")).toHaveLength(2);
    expect(results().textContent).not.toContain("Earlier results");
    expect(location.pathname + location.search).toBe("/search?q=load");
    expect(document.querySelector("form .error")).toBeNull();
    await vi.advanceTimersByTimeAsync(50);
    expect(liveRegion()).toBe("2 sites");
  });

  it.each([
    [0, "No sites"],
    [1, "1 site"],
  ])("announces %d results as %s", async (n, said) => {
    fetchMock.mockImplementation(async () => new Response(page(n), { status: 200 }));
    type("load");
    await vi.advanceTimersByTimeAsync(300);
    expect(liveRegion()).toBe(said);
  });

  it("empties the results without a request when the box is emptied", async () => {
    history.replaceState(null, "", "/search?q=load");
    type("");
    expect(results().children).toHaveLength(0);
    expect(location.pathname + location.search).toBe("/search");
    await vi.advanceTimersByTimeAsync(1000);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it.each([
    ["a 500", async () => new Response(page(2), { status: 500 })],
    [
      "a redirect",
      async () => {
        const res = new Response("<main>Log in</main>", { status: 200 });
        Object.defineProperty(res, "redirected", { value: true });
        return res;
      },
    ],
    ["a page without results", async () => new Response("<main></main>", { status: 200 })],
    [
      "a network error",
      async () => {
        throw new TypeError("network");
      },
    ],
  ])("leaves the page as it is on %s", async (_, response) => {
    fetchMock.mockImplementation(response);
    type("load");
    await vi.advanceTimersByTimeAsync(300);
    expect(results().textContent).toContain("Earlier results");
    expect(location.pathname + location.search).toBe("/search");
    expect(document.querySelector("form .error")).not.toBeNull();
    expect(results().hasAttribute("aria-busy")).toBe(false);
  });

  it("is aria-busy while in flight, and data-busy only after --delay-busy", async () => {
    const respond = deferred();
    type("load");
    await vi.advanceTimersByTimeAsync(250);
    expect(results().getAttribute("aria-busy")).toBe("true");
    expect("busy" in results().dataset).toBe(false);
    await vi.advanceTimersByTimeAsync(299);
    expect("busy" in results().dataset).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect("busy" in results().dataset).toBe(true);
    expect("shown" in spinner().dataset).toBe(true);

    // A keystroke that replaces the request keeps it busy.
    type("loadseed");
    expect(results().getAttribute("aria-busy")).toBe("true");
    await vi.advanceTimersByTimeAsync(250);
    respond[1](new Response(page(1), { status: 200 }));
    await vi.advanceTimersByTimeAsync(0);
    expect(results().hasAttribute("aria-busy")).toBe(false);
    expect("busy" in results().dataset).toBe(false);
    expect("shown" in spinner().dataset).toBe(false);
  });
});
