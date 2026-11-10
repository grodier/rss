// "You were here" draws itself in: the first time at least half of a
// .timeline-divider is in view on this page load, its two rules grow outward
// from the label (rows.css). The label never moves or hides. It plays once,
// not with the scroll position, so scrolling past it again does nothing.
// Without this script, or under reduced motion, the line is simply drawn:
// data-draw="pending" (the rules at zero length) is only ever set here.

// SETTLE is how long after setup a divider already in view at load waits
// before it draws, so it plays once the page has settled (after the page
// crossfade, --duration-quick).
export const SETTLE = 200; // ms

let observer: IntersectionObserver | null = null;
let start = 0;

// enhanceDivider marks a divider pending and draws it the first time it's
// half in view. Infinite scroll calls it on a divider it appends.
export function enhanceDivider(el: HTMLElement): void {
  if (!observer || el.dataset.draw) return;
  el.dataset.draw = "pending";
  observer.observe(el);
}

export function init(): void {
  if (!("IntersectionObserver" in window)) return;
  if (matchMedia("(prefers-reduced-motion: reduce)").matches) return;
  start = Date.now();
  observer = new IntersectionObserver(
    (entries, obs) => {
      for (const e of entries) {
        // The first report can say it intersects with less than half in view.
        if (!e.isIntersecting || e.intersectionRatio < 0.5) continue;
        const el = e.target as HTMLElement;
        obs.unobserve(el);
        const play = () => {
          el.dataset.draw = "play";
        };
        const wait = start + SETTLE - Date.now();
        if (wait > 0) setTimeout(play, wait);
        else play();
      }
    },
    { threshold: 0.5 },
  );
  for (const el of document.querySelectorAll<HTMLElement>(".timeline-divider")) enhanceDivider(el);
}
