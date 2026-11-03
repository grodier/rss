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
  // doesn't reload. Success is a 204; on anything else, including a
  // redirect to the login page, submit the form normally so the round trip
  // shows what happened. Delegated, so rows added by infinite scroll
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
      });
      if (res.status !== 204) throw new Error(`status ${res.status}`);
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
  // a check) and announce it, in the page's polite live region (announce,
  // also used by live search). Takes a root so rows added to the page later
  // can be enhanced too; the click is delegated.
  const enhanceShare = (root) => {
    for (const b of root.querySelectorAll("button[data-share-url]")) b.hidden = false;
  };
  enhanceShare(document);
  let liveStatus;
  const announce = (msg) => {
    if (!liveStatus) {
      liveStatus = document.createElement("p");
      liveStatus.className = "visually-hidden";
      liveStatus.setAttribute("aria-live", "polite");
      document.body.append(liveStatus);
    }
    // Clear it first so the same message is announced again.
    liveStatus.textContent = "";
    setTimeout(() => { liveStatus.textContent = msg; }, 50);
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

  // Live search: as the reader types in form[data-live-search]'s q, fetch
  // the form's page for the query about 250 ms after the last keystroke
  // (aborting any request still in flight) and swap its
  // [data-search-results] into this page's, so results update in place.
  // Nothing is fetched under 2 characters; an empty box shows the no-query
  // state (an empty container) without a request. The URL follows the query,
  // so a reload or Back shows the same results, and the number of sites is
  // announced in the polite live region; focus never moves. A swap also
  // removes the form's error, left from a page rendered as a 422, since the
  // new query is valid. On any failure (a non-200, a redirect to the login
  // page, a network error) the page is left as it is: submitting the form
  // still works.
  {
    const form = document.querySelector("form[data-live-search]");
    const input = form && form.elements.namedItem("q");
    const results = document.querySelector("[data-search-results]");
    if (form && input instanceof HTMLInputElement && results) {
      let timer = 0;
      let inFlight = null;
      const pageURL = (q) => {
        const u = new URL(form.action);
        if (q) u.searchParams.set("q", q);
        return u.href;
      };
      const show = (q, container) => {
        form.querySelector(".error")?.remove();
        results.replaceChildren(...container.childNodes);
        localizeTimes(results);
        enhancePings(results);
        enhanceShare(results);
        history.replaceState(null, "", pageURL(q));
      };
      const search = async (q) => {
        const controller = new AbortController();
        inFlight = controller;
        try {
          const res = await fetch(pageURL(q), { credentials: "same-origin", signal: controller.signal });
          if (res.status !== 200 || res.redirected) throw new Error(`status ${res.status}`);
          const doc = new DOMParser().parseFromString(await res.text(), "text/html");
          const container = doc.querySelector("[data-search-results]");
          if (!container || controller.signal.aborted) return;
          show(q, container);
          const n = results.querySelectorAll(".result").length;
          announce(n === 0 ? "No sites" : n === 1 ? "1 site" : `${n} sites`);
        } catch {
          // Aborted, failed or not the page: leave the page as it is.
        } finally {
          if (inFlight === controller) inFlight = null;
        }
      };
      input.addEventListener("input", () => {
        clearTimeout(timer);
        if (inFlight) inFlight.abort();
        inFlight = null;
        const q = input.value.trim();
        if (q === "") {
          show("", document.createElement("div"));
          return;
        }
        if ([...q].length < 2) return;
        timer = setTimeout(() => search(q), 250);
      });
    }
  }

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

  // The timeline form (form[data-timeline-form]): rebuild the summary above
  // it ([data-summary-sentence], [data-summary-count], [data-summary-feeds])
  // and the counts in it as fields change, with the same rules and words as
  // timelineSentence, timelineFeedCount and timelineFormSummary in
  // timelines.go (change them together). Feeds carry their name, tag IDs and
  // posting rate in data-*; tags their name. Also add a filter box above
  // each feed list ([data-feed-list]).
  {
    const form = document.querySelector("form[data-timeline-form]");
    if (form) {
      const checked = (name) => [...form.querySelectorAll(`input[name="${name}"]:checked`)];
      const box = (name) => form.querySelector(`input[name="${name}"]`)?.checked ?? false;
      const joinWith = (word) => (items) =>
        items.length < 2 ? items.join("") : `${items.slice(0, -1).join(", ")} ${word} ${items[items.length - 1]}`;
      const joinOr = joinWith("or");
      const joinAnd = joinWith("and");
      // splitTerms: trimmed, inner spaces collapsed, no empties or repeats.
      const terms = (s) => {
        const seen = new Set();
        return s.split(",").map((t) => t.trim().split(/\s+/).join(" ")).filter((t) => {
          const k = t.toLowerCase();
          if (!t || seen.has(k)) return false;
          seen.add(k);
          return true;
        });
      };
      const quoted = (ws) => ws.map((w) => `“${w}”`);
      // maxPostsPerWeek: a whole number from 1 to 100, else 0.
      const rate = () => {
        const v = form.elements.max_posts_per_week.value.trim();
        const n = Number(v);
        return /^\d+$/.test(v) && n >= 1 && n <= 100 ? n : 0;
      };
      // Every feed, in the user's order, from the Show list (or Leave out's).
      const feeds = [...form.querySelectorAll('input[name="include_feeds"], input[name="exclude_feeds"]')]
        .filter((el, i, all) => all.findIndex((o) => o.value === el.value) === i)
        .map((el) => ({ id: el.value, name: el.dataset.name, tags: (el.dataset.tags || "").split(" ").filter(Boolean), rate: Number(el.dataset.rate) }));

      const update = () => {
        const some = form.elements.show.value === "some";
        const inTags = checked("include_tags"), exTags = checked("exclude_tags");
        const inFeedIDs = checked("include_feeds").map((el) => el.value), exFeedIDs = checked("exclude_feeds").map((el) => el.value);
        const inTagIDs = inTags.map((el) => el.value), exTagIDs = exTags.map((el) => el.value);
        const feedNames = (ids) => feeds.filter((f) => ids.includes(f.id)).map((f) => f.name);
        const n = rate();
        const quiet = box("quiet") ? n : 0;

        let sentence, count, matched = [];
        if (some && !inTags.length && !inFeedIDs.length) {
          sentence = "Choose at least one tag or feed, or show all your feeds.";
          count = "No feeds yet";
        } else {
          let s = "Articles";
          if (box("starred_only")) s += " you’ve starred";
          if (box("media_only")) s += " with audio or video";
          const words = box("mentions") ? quoted(terms(form.elements.include_words.value)) : [];
          const notWords = box("not_mentions") ? quoted(terms(form.elements.exclude_words.value)) : [];
          if (words.length) s += ` mentioning ${joinOr(words)}`;
          if (notWords.length) s += `${words.length ? " but not " : " not mentioning "}${joinOr(notWords)}`;
          const rateText = quiet ? `post at most ${quiet === 1 ? "once a week" : `${quiet} times a week`}` : "";
          if (!some) {
            s += rateText ? ` from feeds that ${rateText}` : " from all your feeds";
          } else {
            const src = [];
            if (inTags.length) src.push(`feeds tagged ${joinOr(inTags.map((el) => el.dataset.name))}`);
            src.push(...feedNames(inFeedIDs));
            s += ` from ${joinAnd(src)}`;
            if (rateText) s += inFeedIDs.length ? `, if they ${rateText}` : ` that ${rateText}`;
          }
          const except = [];
          if (exTags.length) except.push(`feeds tagged ${joinOr(exTags.map((el) => el.dataset.name))}`);
          except.push(...feedNames(exFeedIDs));
          if (except.length) s += `, except ${joinAnd(except)}`;
          s += ".";
          if (box("starred_only")) s += " Newest star first.";
          sentence = s;

          matched = feeds.filter((f) => {
            const shown = !some || f.tags.some((t) => inTagIDs.includes(t)) || inFeedIDs.includes(f.id);
            const out = f.tags.some((t) => exTagIDs.includes(t)) || exFeedIDs.includes(f.id);
            return shown && !out && (!quiet || f.rate <= quiet);
          });
          if (!matched.length) count = "None of your feeds match yet";
          else if (feeds.length === 1) count = "From your only feed";
          else count = `From ${matched.length} of your ${feeds.length} feeds`;
        }

        form.parentElement.querySelector("[data-summary-sentence]").textContent = sentence;
        form.parentElement.querySelector("[data-summary-count]").textContent = count;
        const list = form.parentElement.querySelector("[data-summary-feeds]");
        list.replaceChildren(...matched.map((f) => Object.assign(document.createElement("li"), { textContent: f.name })));

        const hint = form.querySelector("[data-rate-hint]");
        if (hint) {
          const k = feeds.filter((f) => n && f.rate <= n).length;
          hint.textContent = !n ? "" : k === 1 ? "1 of your feeds posts this little right now." : `${k} of your feeds post this little right now.`;
        }
        const label = (sel, k) => {
          const el = form.querySelector(sel);
          if (el) el.textContent = k ? `(${k})` : "";
        };
        label("[data-feeds-count]", inFeedIDs.length);
        label("[data-leave-out-count]", exTags.length + exFeedIDs.length);
      };
      form.addEventListener("input", update);
      form.addEventListener("change", update);

      for (const list of form.querySelectorAll("ul[data-feed-list]")) {
        const filter = Object.assign(document.createElement("input"), { type: "search", className: "feed-filter", placeholder: "Filter feeds" });
        filter.setAttribute("aria-label", "Filter feeds");
        filter.addEventListener("input", () => {
          const q = filter.value.trim().toLowerCase();
          for (const li of list.children) li.hidden = q !== "" && !li.dataset.name.toLowerCase().includes(q);
        });
        // The filter isn't a field: it doesn't rebuild the summary, and
        // Enter in it doesn't submit the form.
        filter.addEventListener("input", (e) => e.stopPropagation());
        filter.addEventListener("keydown", (e) => {
          if (e.key === "Enter") e.preventDefault();
        });
        list.before(filter);
      }
    }
  }

  // Reordering on /timelines (ul[data-reorder]): show each row's drag
  // handle (.reorder-handle, rendered hidden), and let the move buttons
  // (form[data-move]) move rows in place instead of posting. After a drop
  // or a press, update the buttons' end states, announce the new position,
  // and post the whole order (each row's data-id, "all" for Home) to
  // /timelines/order. Anything but a 204, including the redirect for an
  // order that changed in another window, goes to /timelines, where the
  // flash says why; redirect: "manual" keeps that flash for it. The move
  // buttons stay: they're the single-pointer and keyboard way to reorder.
  {
    const list = document.querySelector("ul[data-reorder]");
    if (list) {
      const rows = () => [...list.children];
      for (const handle of list.querySelectorAll(".reorder-handle")) handle.hidden = false;

      const refresh = () => {
        const all = rows();
        all.forEach((li, i) => {
          const up = li.querySelector('button[value="up"]');
          const down = li.querySelector('button[value="down"]');
          if (up) up.disabled = i === 0;
          if (down) down.disabled = i === all.length - 1;
        });
      };
      const save = async () => {
        try {
          const res = await fetch("/timelines/order", {
            method: "POST",
            body: new URLSearchParams(rows().map((li) => ["order", li.dataset.id])),
            redirect: "manual",
          });
          if (res.status !== 204) throw new Error(`status ${res.status}`);
        } catch {
          location.assign("/timelines");
        }
      };
      const moved = (li) => {
        refresh();
        const all = rows();
        announce(`${li.dataset.name} moved to position ${all.indexOf(li) + 1} of ${all.length}.`);
        li.classList.remove("just-moved");
        void li.offsetWidth; // restart the tint
        li.classList.add("just-moved");
        save();
      };

      // Move buttons: one place up or down, keeping focus on the pressed
      // button, or its sibling once the pressed one is disabled at an end.
      list.addEventListener("submit", (e) => {
        const form = e.target;
        if (!(form instanceof HTMLFormElement) || !form.matches("form[data-move]")) return;
        e.preventDefault();
        const button = e.submitter;
        const li = form.closest("li");
        const up = button?.value === "up";
        const neighbour = up ? li.previousElementSibling : li.nextElementSibling;
        if (!button || !neighbour) return;
        if (up) list.insertBefore(li, neighbour);
        else list.insertBefore(neighbour, li);
        moved(li);
        (button.disabled ? li.querySelector(`button[value="${up ? "down" : "up"}"]`) : button).focus();
      });

      // Dragging a handle: the row swaps with a neighbour whose midpoint
      // the pointer crosses. Escape or a cancelled pointer puts every row
      // back.
      let drag = null; // { li, before }
      const middle = (el) => {
        const r = el.getBoundingClientRect();
        return r.top + r.height / 2;
      };
      list.addEventListener("pointerdown", (e) => {
        const handle = e.target instanceof Element && e.target.closest(".reorder-handle");
        if (!handle || e.button !== 0) return;
        e.preventDefault();
        drag = { li: handle.closest("li"), before: rows() };
        drag.li.classList.add("dragging");
        handle.setPointerCapture(e.pointerId);
      });
      list.addEventListener("pointermove", (e) => {
        if (!drag) return;
        for (;;) {
          const prev = drag.li.previousElementSibling;
          const next = drag.li.nextElementSibling;
          if (prev && e.clientY < middle(prev)) list.insertBefore(drag.li, prev);
          else if (next && e.clientY > middle(next)) list.insertBefore(next, drag.li);
          else break;
        }
      });
      const end = (cancel) => {
        if (!drag) return;
        const { li, before } = drag;
        drag = null;
        li.classList.remove("dragging");
        if (cancel) {
          list.append(...before);
          return;
        }
        if (rows().some((row, i) => row !== before[i])) moved(li);
      };
      list.addEventListener("pointerup", () => end(false));
      list.addEventListener("pointercancel", () => end(true));
      document.addEventListener("keydown", (e) => {
        if (e.key === "Escape") end(true);
      });
    }
  }

  // "You were here": a timeline's first page carries the newest position on
  // it in .article-list[data-seen-up-to], and when it was rendered in
  // data-seen-as-of. Report both when the page is first shown (at load, or
  // its first time visible if it loaded in the background, and again when
  // it's restored from the back-forward cache), and each time it's hidden
  // (another tab, the app backgrounded, the page closed, where unload doesn't
  // fire on phones). The positions of what arrives next are set against the
  // latest report, so reporting on show means an article arriving while the
  // page is open already counts what's on it. The server moves the divider
  // at the reader's next visit, and a reload puts what arrived since the
  // render first.
  const seen = document.querySelector(".article-list[data-seen-up-to]");
  if (seen) {
    const report = () => navigator.sendBeacon("/timeline/seen", new URLSearchParams({
      up_to: seen.dataset.seenUpTo,
      as_of: seen.dataset.seenAsOf,
    }));
    let shown = document.visibilityState === "visible";
    if (shown) report();
    document.addEventListener("visibilitychange", () => {
      if (document.visibilityState === "hidden") {
        report();
      } else if (!shown && document.visibilityState === "visible") {
        shown = true;
        report();
      }
    });
    window.addEventListener("pageshow", (e) => {
      if (e.persisted) report();
    });
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
      const url = top === list.querySelector(":scope > .article-row")
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
        // Rows and the "You were here" divider, in order; only rows anchor
        // the URL.
        for (const el of doc.querySelectorAll(".article-list > .article-row, .article-list > .timeline-divider")) {
          list.append(el);
          if (!el.matches(".article-row")) continue;
          el.dataset.pageUrl = pageUrl;
          localizeTimes(el);
          enhancePings(el);
          enhanceShare(el);
          rows.observe(el);
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
