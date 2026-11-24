import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate } from "../../shared/test/setup";

isolate();

// The article page's markup: the rail's list, then the body's headings.
beforeEach(() => {
  document.body.innerHTML = `
    <nav class="reader-contents" aria-label="Contents" data-contents>
      <ol class="reader-contents-list" role="list">
        <li><a href="#body-one">One</a>
          <ol role="list"><li><a href="#body-two">Two</a></li></ol>
        </li>
        <li><a href="#body-caf%C3%A9">Café</a></li>
        <li><a href="#body-gone">Gone</a></li>
      </ol>
    </nav>
    <div class="article-body">
      <h2 id="body-one">One</h2>
      <h3 id="body-two">Two</h3>
      <h2 id="body-café">Café</h2>
    </div>`;
  Object.defineProperty(window, "innerHeight", { value: 1000, configurable: true });
  Object.defineProperty(window, "scrollY", { value: 0, configurable: true });
  Object.defineProperty(document.documentElement, "scrollHeight", { value: 5000, configurable: true });
});

let wide = true;
beforeEach(() => {
  wide = true;
  vi.spyOn(window, "matchMedia").mockImplementation(
    (query: string) =>
      ({
        get matches() {
          return wide;
        },
        media: query,
        addEventListener: () => {},
      }) as unknown as MediaQueryList,
  );
});

// at places the headings' tops on screen, in px.
function at(one: number, two: number, cafe: number): void {
  const tops: Record<string, number> = { "body-one": one, "body-two": two, "body-café": cafe };
  for (const h of document.querySelectorAll<HTMLElement>("h2, h3")) {
    vi.spyOn(h, "getBoundingClientRect").mockReturnValue({ top: tops[h.id], bottom: tops[h.id] + 30 } as DOMRect);
  }
}

const start = async () => (await import("./contents")).init();
const scroll = async () => {
  window.dispatchEvent(new Event("scroll"));
  await new Promise((resolve) => requestAnimationFrame(resolve));
};
const marked = () => [...document.querySelectorAll("a[aria-current]")].map((a) => a.textContent);
const open = () =>
  [...document.querySelectorAll(".reader-contents-list > li[data-open]")].map(
    (li) => li.querySelector("a")!.textContent,
  );
const nav = () => document.querySelector<HTMLElement>("nav[data-contents]")!;

describe("contents", () => {
  it("marks no section above the first heading, and folds the <h3>s", async () => {
    at(500, 1200, 2000);
    await start();
    expect("tracking" in nav().dataset).toBe(true);
    expect(marked()).toEqual([]);
    expect(open()).toEqual([]);
  });

  it("marks the last section whose heading has passed the reading line", async () => {
    at(100, 900, 1800);
    await start();
    expect(marked()).toEqual(["One"]);
    expect(open()).toEqual(["One"]);

    at(-600, 200, 1100);
    await scroll();
    expect(marked()).toEqual(["Two"]);
    expect(nav().querySelector('a[href="#body-two"]')!.getAttribute("aria-current")).toBe("true");
    // An <h3> opens its <h2>'s list.
    expect(open()).toEqual(["One"]);

    at(-1500, -700, 250);
    await scroll();
    expect(marked()).toEqual(["Café"]);
    expect(open()).toEqual(["Café"]);

    at(500, 1200, 2000);
    await scroll();
    expect(marked()).toEqual([]);
    expect(open()).toEqual([]);
  });

  it("marks the section the URL points at while its heading is near the top", async () => {
    // Just after following "One": its first <h3> is close below and has
    // passed the reading line too.
    at(16, 120, 1800);
    location.hash = "#body-one";
    await start();
    expect(marked()).toEqual(["One"]);

    at(-200, -80, 1600);
    await scroll();
    expect(marked()).toEqual(["Two"]);
    location.hash = "";
  });

  it("marks the last section on screen at the end of the page", async () => {
    at(-900, -400, 700);
    Object.defineProperty(window, "scrollY", { value: 4000, configurable: true });
    await start();
    expect(marked()).toEqual(["Café"]);
  });

  it("doesn't count a page that fits the screen as at its end before it's scrolled", async () => {
    at(500, 600, 700);
    Object.defineProperty(document.documentElement, "scrollHeight", { value: 1000, configurable: true });
    await start();
    expect(marked()).toEqual([]);
  });

  it("does nothing while the list isn't shown", async () => {
    wide = false;
    at(100, 900, 1800);
    await start();
    expect(marked()).toEqual([]);
    wide = true;
    await scroll();
    expect(marked()).toEqual(["One"]);
  });

  it("does nothing without the list", async () => {
    nav().remove();
    await start();
    expect(document.querySelector("[aria-current]")).toBeNull();
  });
});

describe("readingSection", () => {
  it("is the last heading at or above the line", async () => {
    const { readingSection } = await import("./contents");
    expect(readingSection([400, 800], 300)).toBe(-1);
    expect(readingSection([300, 800], 300)).toBe(0);
    expect(readingSection([-500, 100, 800], 300)).toBe(1);
    expect(readingSection([], 300)).toBe(-1);
  });

  it("is the target while its heading is between the top of the screen and the line", async () => {
    const { readingSection } = await import("./contents");
    expect(readingSection([16, 120, 800], 300, undefined, 0)).toBe(0);
    expect(readingSection([-200, 120, 800], 300, undefined, 0)).toBe(1);
    expect(readingSection([-900, -400, 500], 300, undefined, 2)).toBe(1);
    expect(readingSection([-900, 100, 200], 300, 1000, 1)).toBe(1);
  });

  it("is the last heading on screen at the end of the page", async () => {
    const { readingSection } = await import("./contents");
    expect(readingSection([-500, 100, 800], 300, 1000)).toBe(2);
    expect(readingSection([-500, 100, 1200], 300, 1000)).toBe(1);
  });
});
