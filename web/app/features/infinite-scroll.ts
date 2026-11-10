import { localizeTimes } from "../../shared/lib/local-times";
import { enhanceDivider } from "./divider-draw";
import { enhancePings } from "./read-pings";
import { enhanceShare } from "./share";

// Infinite scroll: when the "Older articles" link (.pager a[rel=next])
// nears the viewport, fetch the page it links to and append its rows to
// .article-list. The link stays the source of truth: on any failure it is
// left in place to be clicked. The URL follows the topmost visible row, so
// a reload or Back renders that row's page anchored on the row.
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

  let loading = false;
  const load = async (link: HTMLAnchorElement) => {
    if (loading) return;
    loading = true;
    loader.unobserve(link);
    const pageUrl = withoutHash(link.href);
    try {
      const res = await fetch(pageUrl, { credentials: "same-origin" });
      // A redirect (e.g. to the login page) isn't the page we asked for.
      if (res.status !== 200 || res.redirected) throw new Error(`status ${res.status}`);
      const doc = new DOMParser().parseFromString(await res.text(), "text/html");
      if (!doc.querySelector(".article-list")) throw new Error("no article list");
      // Rows and the "You were here" divider, in order; only rows anchor
      // the URL.
      for (const el of doc.querySelectorAll<HTMLElement>(
        ".article-list > .article-row, .article-list > .timeline-divider",
      )) {
        list.append(el);
        if (!el.matches(".article-row")) {
          enhanceDivider(el);
          continue;
        }
        el.dataset.pageUrl = pageUrl;
        localizeTimes(el);
        enhancePings(el);
        enhanceShare(el);
        rows.observe(el);
      }
      const pager = link.closest(".pager")!;
      const next = doc.querySelector(".pager");
      if (next) pager.replaceWith(next);
      else pager.remove();
      loading = false;
      const nextLink = next && next.querySelector("a[rel=next]");
      if (nextLink) loader.observe(nextLink);
    } catch {
      // Keep the link for the reader to click; stop auto-loading.
      loader.disconnect();
    }
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

  const link = document.querySelector(".pager a[rel=next]");
  if (link) loader.observe(link);
}
