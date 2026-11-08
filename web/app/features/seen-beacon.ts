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

// The list's data-*, which the server always renders together.
type SeenList = HTMLElement & { dataset: { timeline: string; seenUpTo: string } };

export function init(): void {
  const seen = document.querySelector<SeenList>(".article-list[data-seen-up-to]");
  if (!seen) return;
  const report = () =>
    navigator.sendBeacon(
      "/timeline/seen",
      new URLSearchParams({
        timeline: seen.dataset.timeline,
        up_to: seen.dataset.seenUpTo,
      }),
    );
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
    window.addEventListener(
      type,
      () => {
        used = true;
      },
      { passive: true },
    );
  }
  setInterval(
    () => {
      if (used && document.visibilityState === "visible") {
        used = false;
        report();
      }
    },
    10 * 60 * 1000,
  );
}
