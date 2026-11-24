import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate } from "../../shared/test/setup";

isolate();

let beacon: ReturnType<typeof vi.fn>;

// row is an unread article row as partials/article_row.html renders it.
const row = (id: string) => `
  <article class="article-row" id="article-${id}">
    <div class="article-main">
      <p class="article-meta"><a class="article-feed" href="/feeds/f1">Feed One</a></p>
      <div class="article-card">
        <div class="article-text">
          <h3 class="article-title"><a href="/articles/${id}">Title</a></h3>
        </div>
      </div>
      <p class="article-also-in">Also in: <a href="/feeds/f2">Feed Two</a></p>
      <div class="article-foot">
        <a class="article-source" href="https://example.com/${id}" ping="/articles/${id}/read" target="_blank" rel="noopener noreferrer">example.com ↗</a>
        <form class="save-form" method="post" action="/articles/${id}/save" data-toggle>
          <button class="save-button" type="submit" aria-pressed="false">Save</button>
        </form>
      </div>
    </div>
  </article>`;

beforeEach(async () => {
  beacon = vi.fn(() => true);
  Object.defineProperty(navigator, "sendBeacon", { value: beacon, configurable: true });
  document.body.innerHTML = `<div class="article-list">${row("a1")}</div>`;
  const pings = await import("./read-pings");
  pings.enhancePings(document);
  pings.initReadMarks();
});

const article = (id = "a1") => document.getElementById(`article-${id}`)!;
const title = (id = "a1") => article(id).querySelector<HTMLAnchorElement>(".article-title a")!;
const source = (id = "a1") => article(id).querySelector<HTMLAnchorElement>("a.article-source")!;
// click dispatches a click, or an auxclick with button, and stops the
// navigation happy-dom would otherwise try.
const click = (el: Element, button?: number) => {
  el.addEventListener("click", (e) => e.preventDefault(), { once: true });
  el.dispatchEvent(
    button === undefined
      ? new MouseEvent("click", { bubbles: true, cancelable: true, button: 0 })
      : new MouseEvent("auxclick", { bubbles: true, cancelable: true, button }),
  );
};
const hiddenRead = (id = "a1") => article(id).querySelectorAll(".article-title .visually-hidden");
// isRead checks the row is marked as the template marks a read row: .read,
// and the hidden " (read)" once, after the title's link.
const isRead = (id = "a1") => {
  expect(article(id).classList.contains("read")).toBe(true);
  expect(hiddenRead(id)).toHaveLength(1);
  expect(article(id).querySelector(".article-title")!.innerHTML).toBe(
    `<a href="/articles/${id}">Title</a><span class="visually-hidden"> (read)</span>`,
  );
};
const isUnread = (id = "a1") => {
  expect(article(id).classList.contains("read")).toBe(false);
  expect(hiddenRead(id)).toHaveLength(0);
};

describe("read marks", () => {
  it("marks the row read when its title link is clicked", () => {
    click(title());
    isRead();
  });

  it("marks the row read when its source link is clicked, and sends the beacon", () => {
    click(source());
    isRead();
    expect(beacon).toHaveBeenCalledExactlyOnceWith("/articles/a1/read");
  });

  it.each([
    ["title", title],
    ["source", source],
  ])("marks the row read on a middle click on its %s link", (_, link) => {
    click(link(), 1);
    isRead();
  });

  it.each([
    ["title", title],
    ["source", source],
  ])("doesn't mark the row on a right click on its %s link", (_, link) => {
    click(link(), 2);
    isUnread();
  });

  it("leaves a row already read as it is", () => {
    click(title());
    click(source());
    click(title(), 1);
    isRead();
  });

  it.each([
    ["the feed's link", ".article-feed"],
    ["an Also in link", ".article-also-in a"],
    ["an action button", ".save-button"],
    ["the row itself", ".article-main"],
  ])("doesn't mark the row on a click on %s", (_, selector) => {
    click(article().querySelector(selector)!);
    click(article().querySelector(selector)!, 1);
    isUnread();
  });

  it("marks a row added after setup", () => {
    document.querySelector(".article-list")!.insertAdjacentHTML("beforeend", row("a2"));
    click(title("a2"));
    isRead("a2");
    isUnread("a1");
  });

  it("does nothing for a link outside an article row", () => {
    document.body.innerHTML = `
      <article class="reader">
        <h1 class="article-title"><a href="https://example.com/a1">Title</a></h1>
        <a class="article-source" href="https://example.com/a1">example.com ↗</a>
      </article>`;
    click(document.querySelector(".article-title a")!);
    click(document.querySelector("a.article-source")!);
    expect(document.querySelector(".read")).toBeNull();
    expect(document.querySelector(".visually-hidden")).toBeNull();
  });
});
