// tabs.js: the timeline tab row (partials/timeline_tabs.html). Loaded
// without defer in the head of the pages that have the row, because its
// pagereveal listener has to be registered before the page is first drawn:
// a deferred script may run after that. Every page works without it.
(() => {
  "use strict";

  // The row (not the page) keeps where the reader scrolled it when they
  // move to another tab, so its labels don't jump and the underline slides
  // along a row that stays put; the page being left saves its scrollLeft
  // for the page it goes to (pageswap, below). Then, if the current tab
  // isn't fully in view (always the case on a fresh visit to a tab past the
  // row's end), the row centers it. This runs before the page is first drawn
  // (pagereveal, after which a view transition captures the new page), so the
  // underline slides to where the tab will stay, and once the page is parsed
  // (DOMContentLoaded), whichever comes first, since pagereveal can come
  // later or not at all. The second run changes nothing.
  const SAVED = "tab-row";
  const scrollRow = () => {
    const current = document.querySelector(".timeline-tabs [aria-current=page]");
    const row = current && current.closest("ul");
    if (!row) return;
    try {
      const saved = JSON.parse(sessionStorage.getItem(SAVED));
      sessionStorage.removeItem(SAVED);
      if (saved && saved.to === location.href) row.scrollLeft = saved.left;
    } catch {
      // No storage: the row starts at Home.
    }
    const left = current.offsetLeft, right = left + current.offsetWidth;
    if (left < row.scrollLeft || right > row.scrollLeft + row.clientWidth) {
      row.scrollLeft = left - (row.clientWidth - current.offsetWidth) / 2;
    }
  };
  document.addEventListener("DOMContentLoaded", scrollRow);

  // The page being left saves its row's scroll for the next page (above),
  // when the browser says where it's going (the Navigation API). And when its
  // tab is scrolled out of the row, it unnames the underline (main.css): a
  // named element's snapshot isn't clipped by the row, so it would slide in
  // from outside it. The new page's underline fades in instead.
  addEventListener("pageswap", (e) => {
    const row = document.querySelector(".timeline-tabs ul");
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

  // Tab to tab (#521): the browser moves the underline from the old tab to
  // the new one in a straight slide (main.css). Replace that with an elastic
  // move: the edge in front leaves first and runs ahead, the edge behind
  // follows on a softer spring, so the line stretches toward the new tab,
  // then catches up and settles. The front edge overshoots a little (at most
  // MAX_OVERSHOOT px, however far it goes) and springs back. The line thins
  // as it stretches and thickens a hair as it settles, keeping its area,
  // with its bottom edge on the row's divider. Springs reach every distance
  // in the same time, so far moves are faster and stretch further. See
  // "Motion and feedback" in docs/decisions.md.
  const GROUP = "::view-transition-group(tab-indicator)";
  const LEAD = { omega: 34, overshoot: 0.05 }; // rad/s; share of the distance
  const TRAIL = { omega: 26, overshoot: 0.003 };
  const MAX_OVERSHOOT = 6; // px
  const STEP = 1000 / 120; // ms between keyframes
  const REST = 0.5; // px: an edge this close to its place has arrived

  // The step response of a damped spring from 0 to 1, starting at rest, with
  // the damping that overshoots by `overshoot` of the distance (at most
  // MAX_OVERSHOOT px of `distance`), and the time after which it stays
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
    scrollRow();
    if (!e.viewTransition) return;
    try {
      await e.viewTransition.ready;
    } catch {
      return; // skipped
    }
    const root = document.documentElement;
    const slide = root.getAnimations({ subtree: true }).find((a) => a.effect && a.effect.pseudoElement === GROUP);
    if (!slide) return; // no underline on one of the pages: it fades with the page
    const [first] = slide.effect.getKeyframes();
    if (!first || !first.transform || !first.width) return;
    const from = new DOMMatrixReadOnly(first.transform);
    const w1 = parseFloat(first.width);
    const h = parseFloat(first.height);
    // The browser's own slide ends where the group's style puts it: the new
    // underline's place, which shows once that slide is gone.
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
    const lead = spring(LEAD, Math.abs(forward ? R2 - R1 : L2 - L1));
    const trail = spring(TRAIL, Math.abs(forward ? L2 - L1 : R2 - R1));
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
    root.animate(frames, { duration: (frames.length - 1) * STEP, easing: "linear", pseudoElement: GROUP });
  });
})();
