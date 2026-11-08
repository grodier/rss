// The timeline tab row (partials/timeline_tabs.html).

// Tab to tab (#521): timeline-tabs.css moves the underline from the old tab to
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
// timeline-tabs.css's move plays; the page never waits for this one. See
// "Motion and feedback" in docs/decisions.md.
const GROUP = "::view-transition-group(tab-indicator)";
const NEW = "::view-transition-new(tab-indicator)";
export const LEAD = { omega: 34, overshoot: 0.05 }; // rad/s; share of the distance
export const TRAIL = { omega: 26, overshoot: 0.003 };
export const MAX_OVERSHOOT = 6; // px
const STEP = 1000 / 120; // ms between keyframes
export const REST = 0.5; // px: an edge this close to its place has arrived

export interface SpringParams {
  omega: number; // rad/s
  overshoot: number; // share of the distance
}

export interface Spring {
  at: (ms: number) => number; // from 0 to 1
  settle: number; // ms
}

// The step response of a damped spring from 0 to 1, starting at rest,
// with the damping that overshoots by `overshoot` of the distance (at
// most MAX_OVERSHOOT px of `distance`), and the time after which it stays
// within REST px of its place.
export function spring({ omega, overshoot }: SpringParams, distance: number): Spring {
  const p = Math.max(Math.min(overshoot, MAX_OVERSHOOT / Math.max(distance, 1)), 1e-4);
  const zeta = -Math.log(p) / Math.hypot(Math.PI, Math.log(p));
  const k = Math.sqrt(1 - zeta * zeta);
  const decay = zeta * omega,
    wd = omega * k;
  const at = (ms: number) => {
    const t = ms / 1000;
    return 1 - Math.exp(-decay * t) * (Math.cos(wd * t) + (decay / wd) * Math.sin(wd * t));
  };
  // The error is at most exp(-decay t) / k of the distance.
  const settle = Math.max((1000 * Math.log(distance / (k * REST))) / decay, 0);
  return { at, settle };
}

// keyframeEffect is a's effect as the KeyframeEffect it is for CSS
// animations and transitions.
const keyframeEffect = (a: Animation) => a.effect as KeyframeEffect | null;

export function init(): void {
  // The row (not the page) keeps where the reader scrolled it when they
  // move to another tab, so its labels don't jump and the underline slides
  // along a row that stays put: the page being left saves its scrollLeft
  // for the page it goes to (pageswap, below). timeline-tabs.css already starts
  // the row with the current tab in view, before the page is first drawn
  // (scroll-initial-target). The saved scroll is restored only before the
  // row is first seen: during a view transition in pagereveal, which this
  // deferred script only hears when it runs before the transition captures
  // the row, and otherwise when nothing has been painted yet. When it runs
  // later (on a slow device, say), the row stays where timeline-tabs.css put it
  // rather than jump under the reader. Then, if the current tab isn't fully
  // in view, clear of the fade at the row's end (the row's scroll padding),
  // the row scrolls just enough to show it, as timeline-tabs.css does where
  // scroll-initial-target isn't supported.
  const SAVED = "tab-row";
  const current = document.querySelector<HTMLElement>(".timeline-tabs [aria-current=page]");
  const row = current && current.closest("ul");
  if (row && current) {
    const placeRow = (restore: boolean) => {
      try {
        const saved = JSON.parse(sessionStorage.getItem(SAVED) ?? "null") as { to: string; left: number } | null;
        sessionStorage.removeItem(SAVED);
        if (restore && saved && saved.to === location.href) row.scrollLeft = saved.left;
      } catch {
        // No storage: the row stays where timeline-tabs.css put it.
      }
      const fade = parseFloat(getComputedStyle(row).scrollPaddingInlineEnd) || 0;
      const left = current.offsetLeft,
        right = left + current.offsetWidth;
      // Within a pixel is in view: timeline-tabs.css's own placement may round.
      if (left < row.scrollLeft - 1) row.scrollLeft = left;
      else if (right > row.scrollLeft + row.clientWidth - fade + 1) row.scrollLeft = right - row.clientWidth + fade;
    };
    if (document.activeViewTransition) addEventListener("pagereveal", () => placeRow(true), { once: true });
    else placeRow(performance.getEntriesByType("paint").length === 0);
  }

  // The page being left saves its row's scroll for the next page (above),
  // when the browser says where it's going (the Navigation API). And when
  // its tab is scrolled out of the row, it unnames the underline
  // (timeline-tabs.css): a named element's snapshot isn't clipped by the row,
  // so it would slide in from outside it. The new page's underline fades
  // in instead.
  addEventListener("pageswap", (e) => {
    if (!row) return;
    const to = e.activation && e.activation.entry && e.activation.entry.url;
    try {
      if (to) sessionStorage.setItem(SAVED, JSON.stringify({ to, left: row.scrollLeft }));
    } catch {
      // No storage: the next page's row starts at Home.
    }
    const line = e.viewTransition && row.querySelector<HTMLElement>(".tab-indicator");
    if (!line) return;
    const l = line.getBoundingClientRect();
    const r = row.getBoundingClientRect();
    if (l.left < r.left || l.right > r.right) line.dataset.unnamed = "";
  });

  addEventListener("pagereveal", async (e) => {
    if (!e.viewTransition) return;
    try {
      await e.viewTransition.ready;
    } catch {
      return; // skipped
    }
    const root = document.documentElement;
    const animations = root.getAnimations({ subtree: true });
    const slide = animations.find((a) => keyframeEffect(a)?.pseudoElement === GROUP);
    if (!slide) return; // no underline on one of the pages: it fades with the page
    const [first] = keyframeEffect(slide)!.getKeyframes();
    if (!first || !first.transform || !first.width) return;
    const from = new DOMMatrixReadOnly(String(first.transform));
    const w1 = parseFloat(String(first.width));
    const h = parseFloat(String(first.height));
    // timeline-tabs.css's move ends where the group's style puts it: the new
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
    const L1 = from.e,
      R1 = from.e + w1;
    const L2 = to.e,
      R2 = to.e + w2;
    const forward = L2 + R2 >= L1 + R1;
    const near = Math.abs(forward ? L2 - L1 : R2 - R1);
    const lead = spring(LEAD, Math.abs(forward ? R2 - R1 : L2 - L1));
    // On long moves the back edge follows a little more closely, so the
    // stretch tops out rather than growing with the distance.
    const catchUp = 3 * Math.min(Math.max((near - 150) / 550, 0), 1);
    const trail = spring({ ...TRAIL, omega: TRAIL.omega + catchUp }, near);
    // A page scrolled down a little (where the head doesn't stick) starts
    // the new one at the top, so the line may move down too.
    const bottom1 = from.f + h,
      bottom2 = to.f + h;
    const drop = spring(TRAIL, Math.abs(bottom2 - bottom1));

    // Sample until every edge is surely settled, then cut after the last
    // frame that still moved, so the transition ends as soon as it looks
    // done.
    const longest = Math.max(lead.settle, trail.settle, drop.settle);
    const frames: Keyframe[] = [];
    let last = -1;
    for (let ms = 0; ms < longest; ms += STEP) {
      const pl = lead.at(ms),
        pt = trail.at(ms);
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
    // The line's own stretch (timeline-tabs.css) would add to this one.
    for (const a of animations)
      if (keyframeEffect(a)?.pseudoElement === NEW && (a as CSSAnimation).animationName === "tab-indicator-stretch")
        a.cancel();
    root.animate(frames, { duration: (frames.length - 1) * STEP, easing: "linear", pseudoElement: GROUP });
  });
}
