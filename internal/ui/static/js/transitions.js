// Back and Forward cut instead of crossfading (see "Motion and feedback" in
// docs/decisions.md). Browsers animate their own swipe-back gestures (Safari
// on iOS and macOS), and a page can't tell a swipe from the Back button, so
// a crossfade there could play after the browser's own animation.
//
// pagereveal fires before the new page's first render, which a deferred
// script can miss, so this file is a classic script in <head> (app-head),
// kept apart from app.js.
(() => {
  // Set by pageshow, which fires before pagereveal when a page comes back
  // from the back/forward cache.
  let restored = false;
  addEventListener("pageshow", (event) => {
    restored = event.persisted;
  });

  addEventListener("pagereveal", (event) => {
    if (!event.viewTransition) {
      return;
    }
    const activation = window.navigation && window.navigation.activation;
    const traverse = activation
      ? activation.navigationType === "traverse"
      : restored || performance.getEntriesByType("navigation")[0]?.type === "back_forward";
    if (traverse) {
      event.viewTransition.skipTransition();
    }
  });
})();
