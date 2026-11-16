import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate } from "../../shared/test/setup";

isolate();

const key = "paperround.recentSearches";

// page renders Discover's form, the template and the results container,
// with data-recent-query when query is given.
const page = (query?: string, q = "") => {
  document.body.innerHTML = `
    <form action="/discover" method="get" data-live-search>
      <div class="search-field"><input type="search" name="q" value="${q}"></div>
    </form>
    <template data-recent-template>
      <section class="recent-searches">
        <div class="list-head"><h2>Recent searches</h2><button class="link-button" type="button" data-recent-clear>Clear</button></div>
        <ul role="list"><li><a class="recent-search" href="/discover"><svg class="icon"></svg><span data-recent-text></span></a></li></ul>
        <p class="recent-searches-note">Kept on this device only.</p>
      </section>
    </template>
    <div data-search-results${query === undefined ? "" : ` data-recent-query="${query}"`}><p class="sections">Sections</p></div>
    <a class="elsewhere" href="/">Home</a>`;
};

const init = async () => (await import("./recent-searches")).init();
const stored = () => JSON.parse(localStorage.getItem(key) ?? "null") as string[] | null;
const input = () => document.querySelector<HTMLInputElement>("input[name=q]")!;
const results = () => document.querySelector<HTMLElement>("[data-search-results]")!;
const list = () => results().querySelector(".recent-searches");
const rows = () => [...results().querySelectorAll<HTMLAnchorElement>("a.recent-search")];
const focusBox = () => input().dispatchEvent(new FocusEvent("focus"));
const press = (el: Element) => el.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));

beforeEach(() => {
  localStorage.clear();
});

describe("recent-searches", () => {
  it("records a submitted query", async () => {
    page("  Bread  ");
    await init();
    expect(stored()).toEqual(["Bread"]);
  });

  it("puts the newest first, dedupes ignoring case, and keeps 5", async () => {
    localStorage.setItem(key, JSON.stringify(["one", "BREAD", "two", "three", "four"]));
    page("bread");
    await init();
    expect(stored()).toEqual(["bread", "one", "two", "three", "four"]);

    page("five");
    await init();
    expect(stored()).toEqual(["five", "bread", "one", "two", "three"]);
  });

  it("doesn't record without data-recent-query", async () => {
    page();
    await init();
    expect(stored()).toBeNull();
  });

  it("shows the list on focus with an empty box", async () => {
    localStorage.setItem(key, JSON.stringify(["bread", "npr.org"]));
    page();
    await init();
    focusBox();
    expect(results().querySelector(".sections")).toBeNull();
    expect(rows().map((a) => a.textContent)).toEqual(["bread", "npr.org"]);
    expect(list()!.querySelector(".recent-searches-note")!.textContent).toBe("Kept on this device only.");
  });

  it("doesn't show the list with text in the box, or with none kept", async () => {
    localStorage.setItem(key, JSON.stringify(["bread"]));
    page(undefined, "pasta");
    await init();
    focusBox();
    expect(list()).toBeNull();

    localStorage.clear();
    page();
    await init();
    focusBox();
    expect(list()).toBeNull();
    expect(results().querySelector(".sections")).not.toBeNull();
  });

  it("links each to its results", async () => {
    localStorage.setItem(key, JSON.stringify(["bread & butter", "npr.org/news"]));
    page();
    await init();
    focusBox();
    expect(rows().map((a) => a.getAttribute("href"))).toEqual([
      "/discover?q=bread+%26+butter&scope=top",
      "/discover?q=npr.org%2Fnews&scope=top",
    ]);
  });

  it("restores the contents on a press outside", async () => {
    localStorage.setItem(key, JSON.stringify(["bread"]));
    page();
    await init();
    focusBox();
    press(document.querySelector(".elsewhere")!);
    expect(list()).toBeNull();
    expect(results().querySelector(".sections")).not.toBeNull();
  });

  it("restores the contents on Escape", async () => {
    localStorage.setItem(key, JSON.stringify(["bread"]));
    page();
    await init();
    focusBox();
    input().dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    expect(list()).toBeNull();
    expect(results().querySelector(".sections")).not.toBeNull();
  });

  it("restores the contents when focus moves outside", async () => {
    localStorage.setItem(key, JSON.stringify(["bread"]));
    page();
    await init();
    focusBox();
    document.querySelector(".elsewhere")!.dispatchEvent(new FocusEvent("focusin", { bubbles: true }));
    expect(list()).toBeNull();
  });

  it("restores the contents when the reader types", async () => {
    localStorage.setItem(key, JSON.stringify(["bread"]));
    page();
    await init();
    focusBox();
    input().value = "p";
    input().dispatchEvent(new Event("input", { bubbles: true }));
    expect(list()).toBeNull();
    expect(results().querySelector(".sections")).not.toBeNull();
  });

  it("keeps the list on a press on a recent search or in the box", async () => {
    localStorage.setItem(key, JSON.stringify(["bread"]));
    page();
    await init();
    focusBox();
    press(rows()[0]);
    press(input());
    expect(list()).not.toBeNull();
  });

  it("leaves what live search swapped in", async () => {
    localStorage.setItem(key, JSON.stringify(["bread"]));
    page();
    await init();
    focusBox();
    results().innerHTML = `<p class="fresh">Fresh</p>`;
    press(document.querySelector(".elsewhere")!);
    expect(results().querySelector(".fresh")).not.toBeNull();
    expect(results().querySelector(".sections")).toBeNull();
  });

  it("Clear empties storage and hides the list", async () => {
    localStorage.setItem(key, JSON.stringify(["bread"]));
    page();
    await init();
    focusBox();
    list()!.querySelector<HTMLButtonElement>("[data-recent-clear]")!.click();
    expect(stored()).toBeNull();
    expect(list()).toBeNull();
    expect(results().querySelector(".sections")).not.toBeNull();
    focusBox();
    expect(list()).toBeNull();
  });

  it("clearRecentSearches forgets them", async () => {
    localStorage.setItem(key, JSON.stringify(["bread"]));
    (await import("./recent-searches")).clearRecentSearches();
    expect(stored()).toBeNull();
  });

  it("ignores stored values that aren't a list of queries", async () => {
    for (const bad of ["not json", `{"q":"bread"}`, `[1, "", "bread"]`]) {
      localStorage.setItem(key, bad);
      page();
      await init();
      focusBox();
      expect(rows().map((a) => a.textContent)).toEqual(bad.startsWith("[") ? ["bread"] : []);
    }
  });

  it("ignores storage that throws", async () => {
    const blocked = () => {
      throw new Error("blocked");
    };
    vi.stubGlobal("localStorage", { getItem: blocked, setItem: blocked, removeItem: blocked });
    page("bread");
    await init();
    focusBox();
    expect(list()).toBeNull();
    (await import("./recent-searches")).clearRecentSearches();
  });
});
