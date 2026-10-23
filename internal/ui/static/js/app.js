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

  // Save buttons: form[data-save] posts to /articles/{id}/save or /unsave.
  // Post it with fetch instead, then flip aria-pressed (main.css swaps the
  // icon) and the action, so the page doesn't reload. On any failure, submit
  // the form normally so the round trip shows what happened. Delegated, so
  // rows added by infinite scroll work too.
  document.addEventListener("submit", async (e) => {
    const form = e.target;
    if (!(form instanceof HTMLFormElement) || !form.matches("form[data-save]")) return;
    e.preventDefault();
    const button = form.querySelector("button[aria-pressed]");
    if (!button || button.disabled) return;
    button.disabled = true;
    try {
      const res = await fetch(form.action, {
        method: "POST",
        body: new URLSearchParams(new FormData(form)),
        redirect: "manual",
      });
      if (res.type !== "opaqueredirect" && !res.ok) throw new Error(`status ${res.status}`);
      const saved = button.getAttribute("aria-pressed") !== "true";
      button.setAttribute("aria-pressed", String(saved));
      form.setAttribute("action", form.getAttribute("action").replace(/\/(un)?save$/, saved ? "/unsave" : "/save"));
      button.disabled = false;
    } catch {
      form.submit();
    }
  });

  // Video embeds: figure.embed[data-embed] (sanitize's videoEmbed) holds a
  // link to the video. On a plain click, swap the link for the player, built
  // from the validated data-embed-id, never from a URL in the feed.
  // Delegated, so it covers any article on the page.
  const players = {
    youtube: { title: "YouTube video", src: (id) => `https://www.youtube-nocookie.com/embed/${id}?autoplay=1` },
    vimeo: { title: "Vimeo video", src: (id) => `https://player.vimeo.com/video/${id}?autoplay=1&dnt=1` },
  };
  document.addEventListener("click", (e) => {
    if (e.button !== 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
    const a = e.target instanceof Element && e.target.closest("figure.embed[data-embed] a");
    if (!a) return;
    const figure = a.closest("figure");
    const player = players[figure.dataset.embed];
    const id = figure.dataset.embedId;
    if (!player || !id || !/^[A-Za-z0-9_-]{1,20}$/.test(id)) return;
    e.preventDefault();
    const iframe = document.createElement("iframe");
    iframe.src = player.src(encodeURIComponent(id));
    iframe.title = player.title;
    iframe.allow = "autoplay; encrypted-media; fullscreen; picture-in-picture";
    iframe.allowFullscreen = true;
    iframe.setAttribute("sandbox", "allow-scripts allow-same-origin allow-presentation allow-popups");
    iframe.referrerPolicy = "strict-origin-when-cross-origin";
    a.replaceWith(iframe);
    iframe.focus();
  });

  // Password toggle: a Show/Hide button inside every password input. Before
  // its form submits, revealed inputs go back to type=password so browsers
  // still offer to save the password and don't keep it in autofill history.
  for (const input of document.querySelectorAll("input[type=password]")) {
    const wrap = document.createElement("div");
    wrap.className = "password-input";
    input.replaceWith(wrap);
    wrap.append(input);

    const button = document.createElement("button");
    button.type = "button";
    button.className = "password-toggle";
    button.textContent = "Show";
    button.setAttribute("aria-label", "Show password");
    button.setAttribute("aria-pressed", "false");
    button.setAttribute("aria-controls", input.id);
    wrap.append(button);

    const show = (on) => {
      input.type = on ? "text" : "password";
      button.textContent = on ? "Hide" : "Show";
      button.setAttribute("aria-pressed", String(on));
    };
    button.addEventListener("click", () => show(input.type === "password"));
    if (input.form) input.form.addEventListener("submit", () => show(false));
  }

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
