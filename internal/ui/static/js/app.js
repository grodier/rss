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

  // Back and Forward cut instead of crossfading (main.css opts into view
  // transitions). Browsers animate their own swipe-back gestures (Safari on
  // iOS and macOS), and a page can't tell a swipe from the Back button, so
  // the crossfade could play after the browser's own animation. The page
  // being left skips its outgoing transition when the navigation is a
  // traverse; it has loaded, so this deferred script is already listening.
  // Browsers without the Navigation API have no activation there, so a page
  // restored from the back/forward cache (as a swipe back usually is) skips
  // its incoming one too: this script was listening before it was cached.
  addEventListener("pageswap", (e) => {
    if (e.viewTransition && e.activation && e.activation.navigationType === "traverse") e.viewTransition.skipTransition();
  });
  let restored = false;
  addEventListener("pageshow", (e) => { restored = e.persisted; });
  addEventListener("pagereveal", (e) => {
    if (e.viewTransition && restored) e.viewTransition.skipTransition();
  });

  // Save and Star buttons: form[data-toggle] posts to /articles/{id}/save,
  // /unsave, /star or /unstar. Post it with fetch instead, then flip
  // aria-pressed (main.css swaps the icon) and the action, so the page
  // doesn't reload. Success is a 204; on anything else, including a
  // redirect to the login page, submit the form normally so the round trip
  // shows what happened. Delegated, so rows added by infinite scroll
  // work too.
  // While the request runs the button is aria-disabled (not disabled, so
  // focus stays on it) and further presses are ignored; data-busy dims it
  // only once the request has taken --delay-busy, so a quick one never
  // flickers. A 204 that turns the button on sets data-just-toggled, which
  // main.css animates (the star's pop and burst, the bookmark's drop);
  // nothing depends on it, and it's cleared once the animations end (with
  // reduced motion none run, so it stays until the button is turned off).
  const busyDelay = parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--delay-busy")) || 300;
  document.addEventListener("submit", async (e) => {
    const form = e.target;
    if (!(form instanceof HTMLFormElement) || !form.matches("form[data-toggle]")) return;
    e.preventDefault();
    const button = form.querySelector("button[aria-pressed]");
    if (!button || button.getAttribute("aria-disabled") === "true") return;
    button.setAttribute("aria-disabled", "true");
    const busyTimer = setTimeout(() => { button.dataset.busy = ""; }, busyDelay);
    try {
      const res = await fetch(form.action, {
        method: "POST",
        body: new URLSearchParams(new FormData(form)),
      });
      if (res.status !== 204) throw new Error(`status ${res.status}`);
    } catch {
      form.submit(); // stays busy while the page loads
      return;
    }
    clearTimeout(busyTimer);
    delete button.dataset.busy;
    button.removeAttribute("aria-disabled");
    const pressed = button.getAttribute("aria-pressed") !== "true";
    button.setAttribute("aria-pressed", String(pressed));
    form.setAttribute("action", form.getAttribute("action").replace(/\/(un)?(save|star)$/, (_, un, verb) => (pressed ? `/un${verb}` : `/${verb}`)));
    delete button.dataset.justToggled;
    if (pressed) {
      void button.offsetWidth; // restart the animation
      button.dataset.justToggled = "";
    }
  });
  document.addEventListener("animationend", (e) => {
    const button = e.target instanceof Element && e.target.closest("button[data-just-toggled]");
    if (!button || button.getAnimations({ subtree: true }).some((a) => a.playState === "running")) return;
    delete button.dataset.justToggled;
  });

  // Share buttons: button[data-share-url] (the share-button partial) is
  // rendered hidden, since it needs JavaScript; show it where it can work
  // (a share sheet or the Clipboard API, which needs a secure context). On
  // click, open the share sheet with data-share-title and the URL where there
  // is one, else copy the URL, show the outcome with showCopied and announce
  // it, in the page's polite live region (announce, also used by live
  // search). Takes a root so rows added to the page later can be enhanced
  // too; the click is delegated.
  const canShare = Boolean(navigator.share || navigator.clipboard?.writeText);
  const enhanceShare = (root) => {
    if (!canShare) return;
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
  // showCopied shows a copy's outcome on its button: its .copied-label
  // (aria-hidden, since announce says it) reads "Link copied" or "Couldn't
  // copy", shown by data-copied (main.css also swaps the icon for a check
  // that draws itself) or data-copy-failed. Only the latest copy shows, since
  // the clipboard holds one link: a copy hides any other button's label at
  // once. A success shows for 2 seconds, and longer while a mouse is over
  // the button (until it leaves) or it has keyboard focus (until the next key
  // or focus moves); a failure stays until the reader's next press or key,
  // so it never times out before it's read. A success repeated while it
  // shows replays its entrance without hiding and starts its time over:
  // data-copy-round alternates "a" and "b", which main.css uses to restart
  // the animations, and data-copy-dip, set for an instant, dips the label
  // under reduced motion (reading its style applies the dip before it goes,
  // so the label fades back). The label keeps its text when it hides, so it fades out
  // with it. lastPointer is the type of the latest press, since a touch
  // leaves :hover on what it tapped, even on a device that also has a mouse.
  let lastPointer = "";
  document.addEventListener("pointerdown", (e) => { lastPointer = e.pointerType; }, { capture: true });
  let showing; // the button whose label is showing
  const showCopied = (button, ok) => {
    const label = button.querySelector(".copied-label");
    const again = ok && button === showing && "copied" in button.dataset;
    if (again) {
      button.stopCopied();
      button.dataset.copyRound = button.dataset.copyRound === "a" ? "b" : "a";
      button.dataset.copyDip = "";
      if (label) void getComputedStyle(label).opacity;
      delete button.dataset.copyDip;
    } else {
      showing?.hideCopied();
      if (label) label.textContent = ok ? "Link copied" : "Couldn't copy";
      button.dataset[ok ? "copied" : "copyFailed"] = "";
    }
    showing = button;
    const done = new AbortController();
    const { signal } = done;
    let timer;
    const stop = () => {
      clearTimeout(timer);
      done.abort();
    };
    const hide = () => {
      stop();
      delete button.dataset.copied;
      delete button.dataset.copyFailed;
      delete button.dataset.copyRound;
      if (showing === button) showing = undefined;
    };
    button.stopCopied = stop;
    button.hideCopied = hide;
    if (!ok) {
      for (const type of ["pointerdown", "keydown"]) document.addEventListener(type, hide, { signal, capture: true });
      return;
    }
    const hovered = () => lastPointer === "mouse" && button.matches(":hover");
    const focused = () => button.matches(":focus-visible");
    timer = setTimeout(() => {
      if (hovered()) {
        button.addEventListener("pointerleave", hide, { signal });
      } else if (focused()) {
        button.addEventListener("blur", hide, { signal });
        document.addEventListener("keydown", hide, { signal, capture: true });
      } else {
        hide();
      }
    }, 2000);
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
      showCopied(button, false);
      announce("Couldn't copy the link");
      return;
    }
    showCopied(button, true);
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
  // While a request is in flight the results container is aria-busy; once
  // it has taken --delay-busy, data-busy dims the stale results and the
  // spinner (made here once, in place of the box's search icon) fades in,
  // so a fast
  // search shows nothing extra. Both clear when the results are swapped in,
  // the request fails, or the box is emptied; a keystroke that aborts a
  // request for a new one keeps them on, without restarting the delay.
  {
    const form = document.querySelector("form[data-live-search]");
    const input = form && form.elements.namedItem("q");
    const results = document.querySelector("[data-search-results]");
    if (form && input instanceof HTMLInputElement && results) {
      let timer = 0;
      let inFlight = null;
      let busyTimer = 0;
      const spinner = document.createElement("span");
      spinner.className = "spinner search-spinner";
      spinner.setAttribute("aria-hidden", "true");
      (input.closest(".search-field") || form).append(spinner);
      const startBusy = () => {
        if (results.hasAttribute("aria-busy")) return;
        results.setAttribute("aria-busy", "true");
        busyTimer = setTimeout(() => {
          results.dataset.busy = "";
          spinner.dataset.shown = "";
        }, busyDelay);
      };
      const stopBusy = () => {
        clearTimeout(busyTimer);
        results.removeAttribute("aria-busy");
        delete results.dataset.busy;
        delete spinner.dataset.shown;
      };
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
        startBusy();
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
          if (inFlight === controller) {
            inFlight = null;
            stopBusy();
          }
        }
      };
      input.addEventListener("input", () => {
        clearTimeout(timer);
        if (inFlight) inFlight.abort();
        inFlight = null;
        const q = input.value.trim();
        if (q === "") {
          stopBusy();
          show("", document.createElement("div"));
          return;
        }
        if ([...q].length < 2) {
          stopBusy();
          return;
        }
        timer = setTimeout(() => search(q), 250);
      });
    }
  }

  // Lookup status: while a lookup runs, its page ([data-lookup-poll]) fetches
  // itself 2 s after the previous answer arrived (never overlapping), only
  // while the page is visible, instead of the meta refresh it uses without
  // JavaScript. A redirect to the site page replaces this page, so Back skips
  // the finished lookup; any other redirect (Log in) is followed. A 200
  // without [data-lookup-poll] (failed, or no feeds) swaps in its .empty
  // section and announces its first line in the polite live region; focus
  // never moves. Still in progress, a network error or another status: the
  // page is left alone, so the spinner keeps turning, and polling goes on.
  {
    let section = document.querySelector("[data-lookup-poll]");
    if (section) {
      const interval = 2000;
      let timer = 0;
      let inFlight = false;
      let finished = false;
      const poll = async () => {
        timer = 0;
        // Hidden: stop; visibilitychange polls again when it's shown.
        if (document.hidden) return;
        inFlight = true;
        try {
          const res = await fetch(location.href, { credentials: "same-origin" });
          if (res.redirected) {
            finished = true;
            const to = new URL(res.url);
            if (to.origin === location.origin && to.pathname.startsWith("/sites/")) location.replace(to.href);
            else location.assign(to.href);
            return;
          }
          if (res.status !== 200) return;
          const doc = new DOMParser().parseFromString(await res.text(), "text/html");
          if (doc.querySelector("[data-lookup-poll]")) return;
          const next = doc.querySelector("main .empty");
          if (!next) return;
          finished = true;
          section.replaceWith(next);
          section = next;
          const line = next.querySelector("p");
          if (line) announce(line.textContent.trim());
        } catch {
          // Network error: try again at the same interval.
        } finally {
          inFlight = false;
          if (!finished) timer = setTimeout(poll, interval);
        }
      };
      // Poll at once when shown again if the wait ran out while hidden (no
      // timer and no request); otherwise the pending timer polls.
      document.addEventListener("visibilitychange", () => {
        if (!document.hidden && !finished && !inFlight && !timer) poll();
      });
      timer = setTimeout(poll, interval);
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

  // The timeline tab row (partials/timeline_tabs.html).
  {
    // The row (not the page) keeps where the reader scrolled it when they
    // move to another tab, so its labels don't jump and the underline slides
    // along a row that stays put: the page being left saves its scrollLeft
    // for the page it goes to (pageswap, below). main.css already starts the
    // row with the current tab in view, before the page is first drawn
    // (scroll-initial-target). The saved scroll is restored only before the
    // row is first seen: during a view transition in pagereveal, which this
    // deferred script only hears when it runs before the transition captures
    // the row, and otherwise when nothing has been painted yet. When it runs
    // later (on a slow device, say), the row stays where main.css put it
    // rather than jump under the reader. Then, if the current tab isn't fully
    // in view, clear of the fade at the row's end (the row's scroll padding),
    // the row scrolls just enough to show it, as main.css does where
    // scroll-initial-target isn't supported.
    const SAVED = "tab-row";
    const current = document.querySelector(".timeline-tabs [aria-current=page]");
    const row = current && current.closest("ul");
    const placeRow = (restore) => {
      try {
        const saved = JSON.parse(sessionStorage.getItem(SAVED));
        sessionStorage.removeItem(SAVED);
        if (restore && saved && saved.to === location.href) row.scrollLeft = saved.left;
      } catch {
        // No storage: the row stays where main.css put it.
      }
      const fade = parseFloat(getComputedStyle(row).scrollPaddingInlineEnd) || 0;
      const left = current.offsetLeft, right = left + current.offsetWidth;
      // Within a pixel is in view: main.css's own placement may round.
      if (left < row.scrollLeft - 1) row.scrollLeft = left;
      else if (right > row.scrollLeft + row.clientWidth - fade + 1) row.scrollLeft = right - row.clientWidth + fade;
    };
    if (row && document.activeViewTransition) addEventListener("pagereveal", () => placeRow(true), { once: true });
    else if (row) placeRow(performance.getEntriesByType("paint").length === 0);

    // The page being left saves its row's scroll for the next page (above),
    // when the browser says where it's going (the Navigation API). And when
    // its tab is scrolled out of the row, it unnames the underline
    // (main.css): a named element's snapshot isn't clipped by the row, so it
    // would slide in from outside it. The new page's underline fades in
    // instead.
    addEventListener("pageswap", (e) => {
      if (!row) return;
      const to = e.activation && e.activation.entry && e.activation.entry.url;
      try {
        if (to) sessionStorage.setItem(SAVED, JSON.stringify({ to, left: row.scrollLeft }));
      } catch {
        // No storage: the next page's row starts at Home.
      }
      const line = e.viewTransition && row.querySelector(".tab-indicator");
      if (!line) return;
      const l = line.getBoundingClientRect();
      const r = row.getBoundingClientRect();
      if (l.left < r.left || l.right > r.right) line.dataset.unnamed = "";
    });

    // Tab to tab (#521): main.css moves the underline from the old tab to
    // the new one and stretches it about its middle. When this script is
    // listening before the page is first drawn, it replaces that with an
    // elastic move that reaches toward the new tab: the edge in front leaves
    // first and runs ahead, the edge behind follows on a softer spring, so
    // the line stretches toward the new tab, then catches up and settles.
    // The front edge overshoots a little (at most MAX_OVERSHOOT px, however
    // far it goes) and springs back. The line thins as it stretches and
    // thickens a hair as it settles, keeping its area, with its bottom edge
    // on the row's divider. Springs reach every distance in about the same
    // time, so far moves are faster and stretch further, up to about 1.9x.
    // A deferred script can miss pagereveal (on a slow device, say), and then
    // main.css's move plays; the page never waits for this one. See "Motion
    // and feedback" in docs/decisions.md.
    const GROUP = "::view-transition-group(tab-indicator)";
    const NEW = "::view-transition-new(tab-indicator)";
    const LEAD = { omega: 34, overshoot: 0.05 }; // rad/s; share of the distance
    const TRAIL = { omega: 26, overshoot: 0.003 };
    const MAX_OVERSHOOT = 6; // px
    const STEP = 1000 / 120; // ms between keyframes
    const REST = 0.5; // px: an edge this close to its place has arrived

    // The step response of a damped spring from 0 to 1, starting at rest,
    // with the damping that overshoots by `overshoot` of the distance (at
    // most MAX_OVERSHOOT px of `distance`), and the time after which it stays
    // within REST px of its place.
    const spring = ({ omega, overshoot }, distance) => {
      const p = Math.max(Math.min(overshoot, MAX_OVERSHOOT / Math.max(distance, 1)), 1e-4);
      const zeta = -Math.log(p) / Math.hypot(Math.PI, Math.log(p));
      const k = Math.sqrt(1 - zeta * zeta);
      const decay = zeta * omega, wd = omega * k;
      const at = (ms) => {
        const t = ms / 1000;
        return 1 - Math.exp(-decay * t) * (Math.cos(wd * t) + (decay / wd) * Math.sin(wd * t));
      };
      // The error is at most exp(-decay t) / k of the distance.
      const settle = Math.max((1000 * Math.log(distance / (k * REST))) / decay, 0);
      return { at, settle };
    };

    addEventListener("pagereveal", async (e) => {
      if (!e.viewTransition) return;
      try {
        await e.viewTransition.ready;
      } catch {
        return; // skipped
      }
      const root = document.documentElement;
      const animations = root.getAnimations({ subtree: true });
      const slide = animations.find((a) => a.effect && a.effect.pseudoElement === GROUP);
      if (!slide) return; // no underline on one of the pages: it fades with the page
      const [first] = slide.effect.getKeyframes();
      if (!first || !first.transform || !first.width) return;
      const from = new DOMMatrixReadOnly(first.transform);
      const w1 = parseFloat(first.width);
      const h = parseFloat(first.height);
      // main.css's move ends where the group's style puts it: the new
      // underline's place, which shows once that move is gone.
      slide.cancel();
      const end = getComputedStyle(root, GROUP);
      const to = new DOMMatrixReadOnly(end.transform);
      const w2 = parseFloat(end.width);
      if (![from.e, from.f, w1, h, to.e, to.f, w2].every(Number.isFinite)) {
        slide.play();
        return;
      }

      // Edges, in the group's coordinates: left and right, old and new.
      const L1 = from.e, R1 = from.e + w1;
      const L2 = to.e, R2 = to.e + w2;
      const forward = L2 + R2 >= L1 + R1;
      const near = Math.abs(forward ? L2 - L1 : R2 - R1);
      const lead = spring(LEAD, Math.abs(forward ? R2 - R1 : L2 - L1));
      // On long moves the back edge follows a little more closely, so the
      // stretch tops out rather than growing with the distance.
      const catchUp = 3 * Math.min(Math.max((near - 150) / 550, 0), 1);
      const trail = spring({ ...TRAIL, omega: TRAIL.omega + catchUp }, near);
      // A page scrolled down a little (where the head doesn't stick) starts
      // the new one at the top, so the line may move down too.
      const bottom1 = from.f + h, bottom2 = to.f + h;
      const drop = spring(TRAIL, Math.abs(bottom2 - bottom1));

      // Sample until every edge is surely settled, then cut after the last
      // frame that still moved, so the transition ends as soon as it looks
      // done.
      const longest = Math.max(lead.settle, trail.settle, drop.settle);
      const frames = [];
      let last = -1;
      for (let ms = 0; ms < longest; ms += STEP) {
        const pl = lead.at(ms), pt = trail.at(ms);
        const [pL, pR] = forward ? [pt, pl] : [pl, pt];
        const left = L1 + (L2 - L1) * pL;
        const right = R1 + (R2 - R1) * pR;
        const width = Math.max(right - left, 0);
        const p = (pl + pt) / 2;
        const rest = w1 + (w2 - w1) * p;
        const thickness = h * Math.min(Math.max(Math.sqrt(rest / Math.max(width, 1)), 0.7), 1.15);
        const bottom = bottom1 + (bottom2 - bottom1) * drop.at(ms);
        frames.push({
          transform: `translate(${left}px, ${bottom - thickness}px)`,
          width: `${width}px`,
          height: `${thickness}px`,
        });
        if ([left - L2, right - R2, bottom - bottom2].some((d) => Math.abs(d) >= REST)) last = frames.length - 1;
      }
      if (last < 0) return; // the same tab again: nothing moves
      frames.length = last + 1;
      frames.push({ transform: `translate(${L2}px, ${to.f}px)`, width: `${w2}px`, height: `${h}px` });
      // The line's own stretch (main.css) would add to this one.
      for (const a of animations) if (a.effect && a.effect.pseudoElement === NEW && a.animationName === "tab-indicator-stretch") a.cancel();
      root.animate(frames, { duration: (frames.length - 1) * STEP, easing: "linear", pseudoElement: GROUP });
    });
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
  // Rows glide to their places (#519; not under reduced motion): the order
  // changes at once and the motion is drawn on top, so a press is never
  // held up by the one before it. See "Timelines" and "Motion and
  // feedback" in docs/decisions.md.
  {
    const list = document.querySelector("ul[data-reorder]");
    if (list) {
      const rows = () => [...list.children];
      for (const handle of list.querySelectorAll(".reorder-handle")) handle.hidden = false;
      const reduced = matchMedia("(prefers-reduced-motion: reduce)");

      // Every row moves on a critically damped spring, x(t) = (x0 + (v0 +
      // ωx0)t)e^(−ωt), from x0 px away from where it's going at v0 px/s.
      // From rest it eases in and out; interrupted (another press, a drag
      // changing its mind), it carries on from where it is at the speed it
      // has, so nothing jumps or restarts. It never overshoots: rows don't
      // bounce into each other. Rows move the real elements, not snapshots
      // (a view transition would take the page's clicks while it ran), so
      // the buttons keep their focus and take every press.
      const OMEGA = 28; // rad/s: one row's height in about 260 ms
      const STEP = 1000 / 120; // ms between keyframes
      const at = (x0, v0, t) => {
        const b = v0 + OMEGA * x0, e = Math.exp(-OMEGA * t);
        return { x: (x0 + b * t) * e, v: (b - OMEGA * (x0 + b * t)) * e };
      };
      const held = new Map(); // row → px from its place it rests at (aside, during a drag)
      const springs = new Map(); // row → { x0, v0, anim }
      // Where a row is drawn now, from its place in the list, and its speed.
      const motion = (row) => {
        const to = held.get(row) ?? 0;
        const s = springs.get(row);
        if (!s || s.anim.playState !== "running") return { x: to, v: 0 };
        const { x, v } = at(s.x0, s.v0, (s.anim.currentTime ?? 0) / 1000);
        return { x: to + x, v };
      };
      // Move a row to rest `to` px from its place, from `from` px at v0 px/s.
      const spring = (row, to, from, v0 = 0, zIndex = "auto") => {
        springs.get(row)?.anim.cancel();
        springs.delete(row);
        if (to) held.set(row, to);
        else held.delete(row);
        row.style.transform = to ? `translateY(${to}px)` : "";
        const x0 = from - to;
        // Faster than this towards its place and it would overshoot.
        if (x0 * v0 < 0 && Math.abs(v0) > OMEGA * Math.abs(x0)) v0 = -OMEGA * x0;
        if (reduced.matches || (Math.abs(x0) < 0.5 && Math.abs(v0) < 30)) return;
        const frames = [];
        for (let ms = 0; ; ms += STEP) {
          const { x, v } = at(x0, v0, ms / 1000);
          frames.push({ transform: `translateY(${to + x}px)`, zIndex });
          if ((Math.abs(x) < 0.5 && Math.abs(v) < 30) || ms > 1000) break;
        }
        frames.push({ transform: to ? `translateY(${to}px)` : "none", zIndex });
        const anim = row.animate(frames, { duration: (frames.length - 1) * STEP });
        // Start now, as of this frame, not at the next one: x0 and v0 are
        // where the row is in this frame, and a pending start would hold it
        // there for a frame, a stop in the middle of a move.
        anim.startTime = document.timeline.currentTime;
        springs.set(row, { x0, v0, anim });
        anim.onfinish = () => {
          if (springs.get(row)?.anim === anim) springs.delete(row);
        };
      };
      // Change the order and glide every row from where it's drawn to its
      // new place, keeping its speed. `lead` (the row the reader moved) is
      // drawn above the rows it passes, at `speed` if given.
      const glide = (change, lead, z, speed) => {
        const before = new Map(rows().map((li) => [li, { top: li.getBoundingClientRect().top, v: motion(li).v }]));
        if (speed !== undefined) before.get(lead).v = speed;
        for (const s of springs.values()) s.anim.cancel();
        springs.clear();
        change();
        for (const li of rows()) {
          const { top, v } = before.get(li);
          spring(li, 0, top - li.getBoundingClientRect().top, v, li === lead ? z : "auto");
        }
      };

      // Where the list can be seen: below the sticky bars at the top, above
      // the fixed tab bar at the bottom (only those across the list count,
      // so not the desktop rail beside it).
      const visible = () => {
        const l = list.getBoundingClientRect();
        let top = 0, bottom = innerHeight;
        for (const el of document.querySelectorAll(".rail, .page-head, .rail-links")) {
          if (!/^(sticky|fixed)$/.test(getComputedStyle(el).position)) continue;
          const r = el.getBoundingClientRect();
          if (r.right <= l.left || r.left >= l.right || r.height > innerHeight / 2) continue;
          if (r.top <= 1) top = Math.max(top, r.bottom);
          else if (r.bottom >= innerHeight - 1) bottom = Math.min(bottom, r.top);
        }
        return { top, bottom };
      };
      // Keep a moved row in sight, clear of the bars, scrolling with it.
      // Called while nothing is animating, so the row's box is its place.
      const reveal = (li) => {
        const { top, bottom } = visible();
        const r = li.getBoundingClientRect();
        const MARGIN = 12;
        const by = r.bottom + MARGIN > bottom ? r.bottom + MARGIN - bottom : r.top - MARGIN < top ? r.top - MARGIN - top : 0;
        if (by) scrollBy({ top: by, behavior: reduced.matches ? "instant" : "smooth" });
      };

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
      // button, or its sibling once the pressed one is disabled at an end,
      // and the row in sight. The row and its neighbour trade places, the
      // moved row drawn above.
      let drag = null;
      list.addEventListener("submit", (e) => {
        const form = e.target;
        if (!(form instanceof HTMLFormElement) || !form.matches("form[data-move]")) return;
        e.preventDefault();
        const button = e.submitter;
        const li = form.closest("li");
        const up = button?.value === "up";
        const neighbour = up ? li.previousElementSibling : li.nextElementSibling;
        if (!button || !neighbour || drag) return;
        glide(() => {
          if (up) list.insertBefore(li, neighbour);
          else list.insertBefore(neighbour, li);
          reveal(li);
        }, li, 1);
        moved(li);
        (button.disabled ? li.querySelector(`button[value="${up ? "down" : "up"}"]`) : button).focus({ preventScroll: true });
      });

      // Dragging a handle: the row lifts and follows the pointer, held
      // within the list, and the rows it passes slide out of its way as its
      // edge crosses their midpoints. The order only changes on the drop,
      // when the row settles into the gap, carrying the pointer's speed.
      // Near the top or bottom of the visible page the page scrolls, faster
      // the closer the pointer is to the edge. Escape, a cancelled pointer,
      // or leaving the window glides every row back.
      const clamp = (n, lo, hi) => Math.min(Math.max(n, lo), hi);

      // Place the dragged row under the pointer and move the others aside.
      // Positions are in page coordinates, from the rows' places when the
      // drag began, so scrolling during the drag moves the row with the
      // pointer.
      const follow = () => {
        const { li, all, index, tops, heights, gap } = drag;
        const last = all.length - 1;
        const want = drag.y + scrollY - drag.grab;
        const dy = clamp(want, tops[0] - tops[index], tops[last] + heights[last] - tops[index] - heights[index]);
        drag.clamped = dy !== want;
        li.style.transform = `translateY(${dy}px)`;
        const top = tops[index] + dy, bottom = top + heights[index];
        const shift = heights[index] + gap;
        let target = index;
        all.forEach((row, i) => {
          if (i === index) return;
          const middle = tops[i] + heights[i] / 2;
          let d = 0;
          if (i < index && top < middle) {
            d = shift;
            target--;
          } else if (i > index && bottom > middle) {
            d = -shift;
            target++;
          }
          if (d !== (held.get(row) ?? 0)) {
            const { x, v } = motion(row);
            spring(row, d, x, v);
          }
        });
        drag.target = target;
      };

      const EDGE = 72; // px: how close to an edge the page starts to scroll
      const SPEED = 900; // px/s at the edge itself
      const scroll = (now) => {
        if (!drag) return;
        const { top, bottom } = visible();
        const l = list.getBoundingClientRect();
        // Only towards rows that are out of sight.
        const over = (l.top < top ? Math.max(top + EDGE - drag.y, 0) : 0) - (l.bottom > bottom ? Math.max(drag.y - (bottom - EDGE), 0) : 0);
        const dt = drag.at ? Math.min(now - drag.at, 50) : 0;
        drag.at = now;
        if (over && dt) {
          const v = SPEED * Math.min(Math.abs(over) / EDGE, 1) ** 2;
          scrollBy(0, (-Math.sign(over) * v * dt) / 1000);
        }
        drag.frame = requestAnimationFrame(scroll);
      };

      list.addEventListener("pointerdown", (e) => {
        const handle = e.target instanceof Element && e.target.closest(".reorder-handle");
        if (!handle || e.button !== 0 || !e.isPrimary || drag) return;
        e.preventDefault();
        const li = handle.closest("li");
        const all = rows();
        const index = all.indexOf(li);
        // Rows still gliding keep going; their places are where they're
        // going, and the grabbed row stops where it's drawn.
        const offsets = all.map((row) => motion(row).x);
        const rects = all.map((row) => row.getBoundingClientRect());
        const tops = rects.map((r, i) => r.top - offsets[i] + scrollY);
        const heights = rects.map((r) => r.height);
        spring(li, offsets[index], offsets[index]);
        held.delete(li);
        drag = {
          li,
          all,
          index,
          target: index,
          tops,
          heights,
          gap: all.length > 1 ? tops[1] - tops[0] - heights[0] : 0,
          grab: e.clientY + scrollY - offsets[index],
          y: e.clientY,
          t: e.timeStamp,
          v: 0, // px/s, the pointer's
          at: 0,
        };
        li.classList.add("dragging");
        handle.setPointerCapture(e.pointerId);
        drag.frame = requestAnimationFrame(scroll);
      });
      list.addEventListener("pointermove", (e) => {
        if (!drag || !e.isPrimary) return;
        const dt = e.timeStamp - drag.t;
        if (dt > 0) drag.v = 0.6 * ((e.clientY - drag.y) * 1000 / dt) + 0.4 * drag.v;
        drag.y = e.clientY;
        drag.t = e.timeStamp;
        follow();
      });
      addEventListener("scroll", () => {
        if (drag) follow();
      }, { passive: true });

      const end = (cancel, e) => {
        if (!drag) return;
        const { li, all, index, frame } = drag;
        const target = cancel ? index : drag.target;
        // A pointer that had stopped, or a row held at an end, is at rest.
        const still = cancel || drag.clamped || !e || e.timeStamp - drag.t > 60;
        const speed = still ? 0 : drag.v;
        drag = null;
        cancelAnimationFrame(frame);
        glide(() => {
          li.classList.remove("dragging");
          for (const row of all) row.style.transform = "";
          held.clear();
          if (target === index) return;
          // Only the dragged row moves in the document, and it keeps focus
          // if it had it (a move button pressed before the drag).
          const focused = li.contains(document.activeElement) && document.activeElement;
          list.insertBefore(li, all.filter((row) => row !== li)[target] ?? null);
          if (focused) focused.focus({ preventScroll: true });
        }, li, 2, speed);
        if (target !== index) moved(li);
      };
      list.addEventListener("pointerup", (e) => {
        if (e.isPrimary) end(false, e);
      });
      list.addEventListener("pointercancel", () => end(true));
      list.addEventListener("lostpointercapture", () => end(true));
      document.addEventListener("keydown", (e) => {
        if (e.key === "Escape" && drag) {
          e.preventDefault();
          end(true);
        }
      });
      addEventListener("blur", () => end(true));
    }
  }

  // "You were here": a timeline's first page carries the newest position on
  // it in .article-list[data-seen-up-to], and the timeline in
  // data-timeline, whose own line it moves. Report it when the page is first
  // shown (at load, or its first time visible if it loaded in the
  // background, and again when it's restored from the back-forward cache),
  // and each time it's hidden (another tab, the app backgrounded, the page
  // closed, where unload doesn't fire on phones). The positions of what
  // arrives next are set above the latest report, so reporting on show means
  // an article arriving while the page is open already counts what's on it.
  // The server moves the divider at the reader's next visit.
  const seen = document.querySelector(".article-list[data-seen-up-to]");
  if (seen) {
    const report = () => navigator.sendBeacon("/timeline/seen", new URLSearchParams({
      timeline: seen.dataset.timeline,
      up_to: seen.dataset.seenUpTo,
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
    // While the reader is using the page, report every ten minutes too: a
    // visit ends after 30 minutes without a report (rss.VisitGap), so a long
    // read of one page would otherwise end it, moving the line to the top of
    // the page still on screen. Only use counts (scrolling, a key, a tap or a
    // click), so a page left on screen while the reader is away still lets
    // the visit end.
    let used = false;
    for (const type of ["scroll", "keydown", "pointerdown"]) {
      window.addEventListener(type, () => { used = true; }, { passive: true });
    }
    setInterval(() => {
      if (used && document.visibilityState === "visible") {
        used = false;
        report();
      }
    }, 10 * 60 * 1000);
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
