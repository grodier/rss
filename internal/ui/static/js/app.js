// app.js: progressive enhancements. Every page works without it.
(() => {
  "use strict";

  // Local times: <time data-local datetime="…">, rendered in UTC by the
  // server, is shown in the reader's time zone. Takes a root so rows added
  // to the page later can be enhanced too.
  const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
  const localizeTimes = (root) => {
    for (const el of root.querySelectorAll("time[data-local][datetime]")) {
      const d = new Date(el.dateTime);
      if (!Number.isNaN(d.getTime())) el.textContent = fmt.format(d);
    }
  };
  localizeTimes(document);

  // Read pings: <a ping> records a click on the original article. Firefox
  // disables ping by default, so send a beacon instead in every browser.
  // Takes a root so rows added to the page later can be enhanced too.
  const enhancePings = (root) => {
    for (const a of root.querySelectorAll("a[ping]")) {
      const url = a.getAttribute("ping");
      a.removeAttribute("ping");
      const send = () => navigator.sendBeacon(url);
      a.addEventListener("click", send);
      a.addEventListener("auxclick", (e) => { if (e.button === 1) send(); });
    }
  };
  enhancePings(document);

  // Infinite scroll: when the "Older articles" link (.pager a[rel=next])
  // nears the viewport, fetch the page it links to and append its rows to
  // .article-list. The link stays the source of truth: on any failure it is
  // left in place to be clicked. The URL follows the topmost visible row, so
  // a reload or Back renders that row's page anchored on the row.
  const list = document.querySelector(".article-list");
  if (list && "IntersectionObserver" in window) {
    const withoutHash = (href) => {
      const u = new URL(href, location.href);
      u.hash = "";
      return u.href;
    };

    const visible = new Set();
    let timer = 0;
    let current = null;
    const updateURL = () => {
      let top = null;
      let topY = Infinity;
      for (const row of visible) {
        const y = row.getBoundingClientRect().top;
        if (y < topY) { top = row; topY = y; }
      }
      if (!top || top === current) return;
      current = top;
      // At the very top of the page, keep the plain URL rather than
      // anchoring on the first row.
      const url = top === list.firstElementChild
        ? top.dataset.pageUrl
        : top.dataset.pageUrl + "#" + top.id;
      history.replaceState(null, "", url);
    };
    const rows = new IntersectionObserver((entries) => {
      for (const e of entries) {
        if (e.isIntersecting) visible.add(e.target);
        else visible.delete(e.target);
      }
      clearTimeout(timer);
      timer = setTimeout(updateURL, 250);
    }, { threshold: 0 });

    const firstPage = withoutHash(location.href);
    for (const row of list.querySelectorAll(":scope > .article-row")) {
      row.dataset.pageUrl = firstPage;
      rows.observe(row);
    }

    let loading = false;
    const load = async (link) => {
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
        for (const row of doc.querySelectorAll(".article-list > .article-row")) {
          row.dataset.pageUrl = pageUrl;
          list.append(row);
          localizeTimes(row);
          enhancePings(row);
          rows.observe(row);
        }
        const pager = link.closest(".pager");
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
    const loader = new IntersectionObserver((entries) => {
      for (const e of entries) {
        if (e.isIntersecting) { load(e.target); return; }
      }
    }, { rootMargin: "800px 0px" });

    const link = document.querySelector(".pager a[rel=next]");
    if (link) loader.observe(link);
  }
})();
