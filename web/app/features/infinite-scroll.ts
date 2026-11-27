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

// What opening an article keeps in its history entry: its row's id, and
// the row's distance from the top of the screen.
type RowState = { row: string; top: number };

// restoreRow scrolls to the row of list that the URL's fragment names:
// back where it was on the screen when its article was opened (the
// entry's RowState), else to the top, clear of the sticky bars (the row's
// scroll-margin, rows.css). A web font arriving later can move the row, so
// it scrolls again once the fonts are ready, unless the reader has
// scrolled since.
function restoreRow(list: Element): void {
  const id = decodeURIComponent(location.hash.slice(1));
  const row = id ? document.getElementById(id) : null;
  if (!row || row.parentElement !== list || !row.matches(".article-row")) return;
  const state = history.state as Partial<RowState> | null;
  const top = state?.row === id && typeof state.top === "number" ? state.top : null;
  const scroll = () => {
    if (top === null) row.scrollIntoView({ block: "start" });
    else scrollTo(0, scrollY + row.getBoundingClientRect().top - top);
    return scrollY;
  };
  const at = scroll();
  document.fonts?.ready.then(() => {
    if (scrollY === at) scroll();
  });
}

// scrollMargin is the top scroll-margin of list's rows, in pixels.
function scrollMargin(list: Element): number {
  const row = list.querySelector(":scope > .article-row");
  return (row && parseFloat(getComputedStyle(row).scrollMarginTop)) || 0;
}

// How long a page may take to load before it counts as failed: well past a
// slow phone connection's few seconds for a page of rows.
const LOAD_TIMEOUT = 20_000;

// The words of each pager link's states, from its own label.
const pagerText = new WeakMap<HTMLAnchorElement, { loading: string; failed: string }>();

// Infinite scroll: when the "Older articles" link (.pager a[rel=next])
// nears the viewport, fetch the page it links to and append its rows to
// .article-list, and, from the last page, All feeds' end line
// (.timeline-end) in the link's place. The list is aria-busy while a page
// loads; a load that fails says so above the link, which becomes Try
// again, and auto-loading stops until a retry works. The link stays the
// source of truth: its href never changes, a press with a modifier always
// follows it, and a retry that gets anything but the page follows it too.
// The URL follows the topmost visible row, so a reload or Back renders
// that row's page anchored on the row; opening an article anchors it on
// that article's row instead, and remembers where on the screen the row
// was, so Back to a page that's loaded again puts it back there.
export function init(): void {
  const list = document.querySelector(".article-list");
  if (!list || !("IntersectionObserver" in window)) return;
  const withoutHash = (href: string) => {
    const u = new URL(href, location.href);
    u.hash = "";
    return u.href;
  };
  const isFirst = (row: HTMLElement) => row === list.querySelector(":scope > .article-row");

  // The scroll a browser would restore was measured on this page with
  // every page infinite scroll added; loaded again, the page has only the
  // anchored row's page, so that scroll lands anywhere (Safari keeps it,
  // cut to the shorter page). We put the anchored row back instead. Set
  // here, it's this history entry's mode when it's loaded again.
  history.scrollRestoration = "manual";
  restoreRow(list);

  const visible = new Set<HTMLElement>();
  let timer = 0;
  let current: HTMLElement | null = null;
  // opened is set while an opened article's row holds the URL: until the
  // page is shown again from the back/forward cache, nothing moves it.
  let opened = false;
  const updateURL = () => {
    if (opened) return;
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
    history.replaceState(null, "", isFirst(top) ? top.dataset.pageUrl : `${top.dataset.pageUrl}#${top.id}`);
  };
  // Opening an article (a plain press of its row's .article-card; a
  // press with a modifier opens it elsewhere and leaves this page as it
  // is) anchors the URL on its row, with the row's distance from the top
  // of the screen in the entry's state.
  list.addEventListener("click", (e) => {
    const me = e as MouseEvent;
    if (me.button !== 0 || me.metaKey || me.ctrlKey || me.shiftKey || me.altKey) return;
    const link = (e.target as Element).closest(".article-card a[href]");
    const row = link?.closest<HTMLElement>(".article-list > .article-row");
    if (!row?.dataset.pageUrl) return;
    clearTimeout(timer);
    opened = true;
    current = row;
    if (isFirst(row)) history.replaceState(null, "", row.dataset.pageUrl);
    else {
      const state: RowState = { row: row.id, top: row.getBoundingClientRect().top };
      history.replaceState(state, "", `${row.dataset.pageUrl}#${row.id}`);
    }
  });
  addEventListener("pageshow", (e) => {
    if (e.persisted) opened = false;
  });
  const rows = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (e.isIntersecting) visible.add(e.target as HTMLElement);
        else visible.delete(e.target as HTMLElement);
      }
      clearTimeout(timer);
      timer = setTimeout(updateURL, 250);
    },
    // A row counts as visible below the band its scroll-margin keeps
    // clear of the sticky bars, so the URL never names a row hidden
    // under them, and a reload lands on the row that was showing. A pixel
    // more, as a row that only touches the edge counts: the one above a
    // row scrolled to its margin ends there.
    { threshold: 0, rootMargin: `-${scrollMargin(list) + 1}px 0px 0px 0px` },
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
    // failed keeps the link for the reader to press, as Try again, and
    // stops auto-loading until Try again works.
    const failed = (say: boolean) => {
      clearTimeout(busyTimer);
      list.removeAttribute("aria-busy");
      loader.disconnect();
      pager.querySelector(".pager-error")?.remove();
      const error = document.createElement("p");
      error.className = "pager-error";
      error.textContent = text.failed;
      pager.prepend(error);
      pager.dataset.state = "failed";
      link.textContent = "Try again";
      if (say) announce(text.failed);
      loading = false;
    };
    const pageUrl = withoutHash(link.href);
    let doc: Document;
    try {
      // No copy in any cache (the server sends a script private, no-cache;
      // see setCacheControl). A request that hangs ends as a failure, as
      // a plain press can't leave a load; a browser without
      // AbortSignal.timeout just waits.
      const res = await fetch(pageUrl, {
        credentials: "same-origin",
        cache: "no-store",
        signal: AbortSignal.timeout?.(LOAD_TIMEOUT),
      });
      doc = new DOMParser().parseFromString(await res.text(), "text/html");
      // A redirect (e.g. to the login page) isn't the page we asked for.
      if (res.status !== 200 || res.redirected || !doc.querySelector(".article-list")) {
        if (!retry) throw new Error(`status ${res.status}`);
        // Try again asked for this page, so go there, as the link would,
        // still showing it's loading. Back to this page from the
        // back/forward cache then finds the failure again, not a load
        // that's no longer running.
        addEventListener(
          "pageshow",
          (e) => {
            if (e.persisted) failed(false);
          },
          { once: true },
        );
        location.assign(link.href);
        return;
      }
    } catch {
      failed(true);
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
