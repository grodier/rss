import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate, liveRegion } from "../../shared/test/setup";

isolate();

// The markup feedsHandler renders: a group of two feeds, marking the
// section after it, then two feeds of their own, every row's data-filter
// lowercased.
beforeEach(async () => {
  vi.useFakeTimers();
  history.replaceState(null, "", "/feeds?sort=updated");
  document.body.innerHTML = `
    <form method="get" action="/feeds" data-feeds-filter>
      <input type="search" name="q" value="">
      <input type="hidden" name="sort" value="updated">
      <p><a href="/feeds" data-sort-link>Name</a> <a href="/feeds?sort=updated" data-sort-link>Recently updated</a></p>
    </form>
    <section id="alpha" data-grouped>
      <ul>
        <li id="notes" data-filter="alpha notes\nalpha notes\nalpha\nalpha.example.com">Alpha Notes</li>
        <li id="mine" data-filter="my alpha\nalpha posts\nalpha\nalpha.example.com">My Alpha</li>
      </ul>
    </section>
    <section id="beta" data-after-group>
      <ul><li id="beta-feed" data-filter="beta\nbeta\nbeta\nbeta.example.com">Beta</li></ul>
    </section>
    <section id="gamma">
      <ul><li id="weekly" data-filter="gamma weekly\ngamma weekly\n\ngamma.example.com">Gamma Weekly</li></ul>
    </section>
    <div data-feeds-none hidden><p>None of your feeds match “<span data-feeds-q></span>”. <a href="/discover?q=" data-feeds-discover>Search Discover</a></p></div>`;
  (await import("./feeds-filter")).init();
});

const input = () => document.querySelector<HTMLInputElement>("input[name=q]")!;
const type = (value: string) => {
  input().value = value;
  input().dispatchEvent(new Event("input", { bubbles: true }));
};
const hidden = (id: string) => document.getElementById(id)!.hidden;
const none = () => document.querySelector<HTMLElement>("[data-feeds-none]")!;
const sortLinks = () =>
  [...document.querySelectorAll<HTMLAnchorElement>("a[data-sort-link]")].map((a) => a.getAttribute("href"));

describe("feeds-filter", () => {
  it("hides the rows that don't match, ignoring case and spaces around", () => {
    type("  POSTS ");
    expect([hidden("notes"), hidden("mine"), hidden("weekly")]).toEqual([true, false, true]);
  });

  it("matches the site's host", () => {
    type("gamma.example");
    expect([hidden("notes"), hidden("mine"), hidden("weekly")]).toEqual([true, true, false]);
  });

  it("hides a section left with no rows, and shows it again", () => {
    type("weekly");
    expect(hidden("alpha")).toBe(true);
    expect(hidden("gamma")).toBe(false);
    type("alpha");
    expect(hidden("alpha")).toBe(false);
    expect(hidden("gamma")).toBe(true);
  });

  it("shows every row when the box is emptied", () => {
    type("weekly");
    type("");
    expect([hidden("notes"), hidden("mine"), hidden("weekly"), hidden("alpha"), hidden("gamma")]).toEqual([
      false,
      false,
      false,
      false,
      false,
    ]);
    expect(none().hidden).toBe(true);
  });

  it("says when nothing matches, with the query and a Discover link for it", () => {
    type("zzz top");
    expect(none().hidden).toBe(false);
    expect(none().querySelector("[data-feeds-q]")!.textContent).toBe("zzz top");
    expect(none().querySelector("a")!.getAttribute("href")).toBe("/discover?q=zzz+top");
    type("alpha");
    expect(none().hidden).toBe(true);
  });

  it("keeps the query in the URL and the sort links, and drops it when emptied", () => {
    type("a b");
    expect(location.pathname + location.search).toBe("/feeds?sort=updated&q=a+b");
    expect(sortLinks()).toEqual(["/feeds?q=a+b", "/feeds?sort=updated&q=a+b"]);
    type("");
    expect(location.pathname + location.search).toBe("/feeds?sort=updated");
    expect(sortLinks()).toEqual(["/feeds", "/feeds?sort=updated"]);
  });

  it("keeps the kind in the URL, the sort links and the kind links as it types", () => {
    history.replaceState(null, "", "/feeds?kind=videos&sort=updated");
    const kinds = document.createElement("nav");
    kinds.innerHTML = `
      <a href="/feeds?sort=updated" data-kind-link>All</a>
      <a href="/feeds?kind=videos&sort=updated" aria-current="page" data-kind-link>Videos</a>`;
    document.body.prepend(kinds);
    for (const [i, a] of document.querySelectorAll<HTMLAnchorElement>("a[data-sort-link]").entries()) {
      a.href = ["/feeds?kind=videos", "/feeds?kind=videos&sort=updated"][i];
    }
    const kindLinks = () =>
      [...document.querySelectorAll<HTMLAnchorElement>("a[data-kind-link]")].map((a) => a.getAttribute("href"));

    type("a b");
    expect(location.pathname + location.search).toBe("/feeds?kind=videos&sort=updated&q=a+b");
    expect(sortLinks()).toEqual(["/feeds?kind=videos&q=a+b", "/feeds?kind=videos&sort=updated&q=a+b"]);
    expect(kindLinks()).toEqual(["/feeds?sort=updated&q=a+b", "/feeds?kind=videos&sort=updated&q=a+b"]);
    type("");
    expect(location.pathname + location.search).toBe("/feeds?kind=videos&sort=updated");
    expect(kindLinks()).toEqual(["/feeds?sort=updated", "/feeds?kind=videos&sort=updated"]);
  });

  it("announces how many feeds match once typing pauses", async () => {
    type("a");
    type("alpha");
    await vi.advanceTimersByTimeAsync(499);
    expect(liveRegion()).toBe("");
    await vi.advanceTimersByTimeAsync(100);
    expect(liveRegion()).toBe("2 feeds");
    type("weekly");
    await vi.advanceTimersByTimeAsync(600);
    expect(liveRegion()).toBe("1 feed");
    type("zzz");
    await vi.advanceTimersByTimeAsync(600);
    expect(liveRegion()).toBe("No feeds");
  });

  it("puts the rule after a group on the next shown section", () => {
    const ruled = () => ["beta", "gamma"].filter((id) => document.getElementById(id)!.hasAttribute("data-after-group"));
    type("weekly");
    expect(ruled()).toEqual([]); // the group is hidden
    type("y"); // My Alpha and Gamma Weekly, not Beta
    expect(ruled()).toEqual(["gamma"]);
    type("");
    expect(ruled()).toEqual(["beta"]);
  });

  it("filters on Enter without submitting", () => {
    input().value = "weekly";
    const submitted = new Event("submit", { cancelable: true });
    document.querySelector("form")!.dispatchEvent(submitted);
    expect(submitted.defaultPrevented).toBe(true);
    expect(hidden("weekly")).toBe(false);
    expect(hidden("notes")).toBe(true);
  });
});
