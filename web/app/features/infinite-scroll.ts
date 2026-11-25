import { announce } from "../../shared/lib/announce";
import { busyDelay } from "../../shared/lib/busy-delay";
import { localizeTimes } from "../../shared/lib/local-times";
import { enhanceDivider } from "./divider-draw";
import { enhancePings } from "./read-pings";
import { enhanceShare } from "./share";

// enhanceRows sets up the article rows in root that arrived after the page
// loaded, as the reading entry's setups do at load: local times, read pings
// and Share. Infinite scroll calls it on each row it appends, and
// home-fetching.ts on the list it swaps in.
export function enhanceRows(root: ParentNode): void {
  localizeTimes(root);
  enhancePings(root);
  enhanceShare(root);
}

// The words of each pager link's states, from its own label.
const pagerText = new WeakMap<HTMLAnchorElement, { loading: string; failed: string }>();

// Infinite scroll: when the "Older articles" link (.pager a[rel=next])
// nears the viewport, fetch the page it links to and append its rows to
// .article-list, and, from the last page, All feeds' end line
// (.timeline-end) in the link's place. The list is aria-busy while a page
// loads; a load that fails says so above the link, which becomes Try
// again, and auto-loading stops until a retry works. The link stays the
// source of truth: its href never changes, a press with a modifier always
// follows it, and a retry that gets anything but the page follows it
// too. The URL follows the
// topmost visible row, so a reload or Back renders that row's page
// anchored on the row.
export function init(): void {
  const list = document.querySelector(".article-list");
  if (!list || !("IntersectionObserver" in window)) return;
  const withoutHash = (href: string) => {
    const u = new URL(href, location.href);
    u.hash = "";
    return u.href;
  };

  const visible = new Set<HTMLElement>();
  let timer = 0;
  let current: HTMLElement | null = null;
  const updateURL = () => {
    let top: HTMLElement | null = null;
    let topY = Infinity;
    for (const row of visible) {
      const y = row.getBoundingClientRect().top;
      if (y < topY) {
        top = row;
        topY = y;
      }
    }
    if (!top || top === current) return;
    current = top;
    // At the very top of the page, keep the plain URL rather than
    // anchoring on the first row.
    const url =
      top === list.querySelector(":scope > .article-row") ? top.dataset.pageUrl : `${top.dataset.pageUrl}#${top.id}`;
    history.replaceState(null, "", url);
  };
  const rows = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (e.isIntersecting) visible.add(e.target as HTMLElement);
        else visible.delete(e.target as HTMLElement);
      }
      clearTimeout(timer);
      timer = setTimeout(updateURL, 250);
    },
    { threshold: 0 },
  );

  const firstPage = withoutHash(location.href);
  for (const row of list.querySelectorAll<HTMLElement>(":scope > .article-row")) {
    row.dataset.pageUrl = firstPage;
    rows.observe(row);
  }

  // A page's load is in one of three states, kept on its pager as
  // data-state: "loading" (fetching, nothing shown yet), "busy" (still
  // loading after --delay-busy: the link says so, with a spinner) and
  // "failed" (the link says Try again, under a line saying what went
  // wrong). While a page loads a plain press of the link does nothing, as
  // its rows are on their way; while it has failed a plain press retries
  // in place. A press with a modifier always follows the link.
  let loading = false;
  const setBusy = (link: HTMLAnchorElement, label: string) => {
    const spinner = document.createElement("span");
    spinner.className = "spinner btn-spinner";
    spinner.setAttribute("aria-hidden", "true");
    link.replaceChildren(spinner, label);
    link.closest<HTMLElement>(".pager")!.dataset.state = "busy";
  };
  // retry is true for a press of Try again: its label changes at once, as
  // the reader asked for it, and a page that isn't the one asked for
  // (a redirect, as to Log in when the session has ended, or any other
  // status) is followed rather than shown as a failure.
  const load = async (link: HTMLAnchorElement, retry = false) => {
    if (loading) return;
    loading = true;
    loader.unobserve(link);
    const pager = link.closest<HTMLElement>(".pager")!;
    const text = pagerText.get(link)!;
    pager.querySelector(".pager-error")?.remove();
    pager.dataset.state = "loading";
    list.setAttribute("aria-busy", "true");
    if (retry) link.textContent = text.loading;
    const busyTimer = setTimeout(() => setBusy(link, text.loading), busyDelay());
    const pageUrl = withoutHash(link.href);
    let doc: Document;
    try {
      // No copy in any cache (the server sends a script private, no-cache; see setCacheControl).
      const res = await fetch(pageUrl, { credentials: "same-origin", cache: "no-store" });
      doc = new DOMParser().parseFromString(await res.text(), "text/html");
      // A redirect (e.g. to the login page) isn't the page we asked for.
      if (res.status !== 200 || res.redirected || !doc.querySelector(".article-list")) {
        if (retry) {
          location.assign(link.href);
          return;
        }
        throw new Error(`status ${res.status}`);
      }
    } catch {
      clearTimeout(busyTimer);
      list.removeAttribute("aria-busy");
      // Keep the link for the reader to press; stop auto-loading until
      // Try again works.
      loader.disconnect();
      const error = document.createElement("p");
      error.className = "pager-error";
      error.textContent = text.failed;
      pager.prepend(error);
      pager.dataset.state = "failed";
      link.textContent = "Try again";
      announce(text.failed);
      loading = false;
      return;
    }
    clearTimeout(busyTimer);
    // Rows and the "You were here" divider, in order; only rows anchor
    // the URL.
    let first: HTMLElement | null = null;
    for (const el of doc.querySelectorAll<HTMLElement>(
      ".article-list > .article-row, .article-list > .timeline-divider",
    )) {
      list.append(el);
      if (!el.matches(".article-row")) {
        enhanceDivider(el);
        continue;
      }
      first ??= el;
      el.dataset.pageUrl = pageUrl;
      enhanceRows(el);
      rows.observe(el);
    }
    list.removeAttribute("aria-busy");
    // Try again pressed from the keyboard would lose focus with its pager,
    // so it goes to the first new row's title, where reading goes on.
    const hadFocus = document.activeElement === link;
    // The last page has no pager, and, on All feeds, the end line.
    const next = doc.querySelector(".pager") ?? doc.querySelector(".timeline-end");
    if (next) pager.replaceWith(next);
    else pager.remove();
    if (hadFocus) first?.querySelector<HTMLElement>(".article-title a")?.focus();
    loading = false;
    const nextLink = next && next.querySelector<HTMLAnchorElement>("a[rel=next]");
    if (nextLink) watch(nextLink);
  };
  const loader = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (e.isIntersecting) {
          load(e.target as HTMLAnchorElement);
          return;
        }
      }
    },
    { rootMargin: "800px 0px" },
  );

  // watch sets up a pager's link: its labels, from its own words ("Older
  // posts" gives "Loading older posts…"), its presses, and auto-loading.
  const watch = (link: HTMLAnchorElement) => {
    const words = link.textContent.trim();
    const lower = words.charAt(0).toLowerCase() + words.slice(1);
    pagerText.set(link, { loading: `Loading ${lower}…`, failed: `Couldn't load ${lower}.` });
    link.addEventListener("click", (e) => {
      if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      const state = link.closest<HTMLElement>(".pager")?.dataset.state;
      if (state === "loading" || state === "busy") e.preventDefault();
      else if (state === "failed") {
        e.preventDefault();
        load(link, true);
      }
    });
    loader.observe(link);
  };
  const link = document.querySelector<HTMLAnchorElement>(".pager a[rel=next]");
  if (link) watch(link);
}
