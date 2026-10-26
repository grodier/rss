// app.js: progressive enhancements. Every page works without it.
(() => {
  "use strict";

  // Local times: <time data-local datetime="…">, rendered in UTC by the
  // server (the local-time and local-date partials), is shown in the
  // reader's time zone; data-local="date" shows the date only. Takes a root
  // so rows added to the page later can be enhanced too.
  const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
  const dateFmt = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });
  const localizeTimes = (root) => {
    for (const el of root.querySelectorAll("time[data-local][datetime]")) {
      const d = new Date(el.dateTime);
      if (!Number.isNaN(d.getTime())) el.textContent = (el.dataset.local === "date" ? dateFmt : fmt).format(d);
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

  // Save and Star buttons: form[data-toggle] posts to /articles/{id}/save,
  // /unsave, /star or /unstar. Post it with fetch instead, then flip
  // aria-pressed (main.css swaps the icon) and the action, so the page
  // doesn't reload. On any failure, submit the form normally so the round
  // trip shows what happened. Delegated, so rows added by infinite scroll
  // work too.
  document.addEventListener("submit", async (e) => {
    const form = e.target;
    if (!(form instanceof HTMLFormElement) || !form.matches("form[data-toggle]")) return;
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
      const pressed = button.getAttribute("aria-pressed") !== "true";
      button.setAttribute("aria-pressed", String(pressed));
      form.setAttribute("action", form.getAttribute("action").replace(/\/(un)?(save|star)$/, (_, un, verb) => (pressed ? `/un${verb}` : `/${verb}`)));
      button.disabled = false;
    } catch {
      form.submit();
    }
  });

  // Share buttons: button[data-share-url] (the share-button partial) is
  // rendered hidden, since it needs JavaScript; show it. On click, open the
  // share sheet with data-share-title and the URL where there is one, else
  // copy the URL, set data-copied for 2 seconds (main.css swaps the icon for
  // a check) and announce it. Takes a root so rows added to the page later
  // can be enhanced too; the click is delegated.
  const enhanceShare = (root) => {
    for (const b of root.querySelectorAll("button[data-share-url]")) b.hidden = false;
  };
  enhanceShare(document);
  let shareStatus;
  const announce = (msg) => {
    if (!shareStatus) {
      shareStatus = document.createElement("p");
      shareStatus.className = "visually-hidden";
      shareStatus.setAttribute("aria-live", "polite");
      document.body.append(shareStatus);
    }
    // Clear it first so the same message is announced again.
    shareStatus.textContent = "";
    setTimeout(() => { shareStatus.textContent = msg; }, 50);
  };
  document.addEventListener("click", async (e) => {
    const button = e.target instanceof Element && e.target.closest("button[data-share-url]");
    if (!button) return;
    const url = button.dataset.shareUrl;
    if (navigator.share) {
      try {
        await navigator.share({ title: button.dataset.shareTitle, url });
        return;
      } catch (err) {
        if (err.name === "AbortError") return; // the reader closed the sheet
      }
    }
    try {
      await navigator.clipboard.writeText(url);
    } catch {
      announce("Couldn't copy the link");
      return;
    }
    button.dataset.copied = "";
    clearTimeout(button.copiedTimer);
    button.copiedTimer = setTimeout(() => { delete button.dataset.copied; }, 2000);
    announce("Link copied");
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

  // Timeline tabs: when the current tab (.timeline-tabs [aria-current])
  // is scrolled out of its row, scroll the row (not the page) so it shows.
  // Without this the row starts at Home and the reader scrolls it.
  {
    const current = document.querySelector(".timeline-tabs [aria-current=page]");
    const row = current && current.closest("ul");
    if (row && current.offsetLeft + current.offsetWidth > row.clientWidth) {
      row.scrollLeft = current.offsetLeft - (row.clientWidth - current.offsetWidth) / 2;
    }
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
          enhanceShare(row);
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
