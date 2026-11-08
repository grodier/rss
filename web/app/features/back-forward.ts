// Back and Forward cut instead of crossfading (shell.css opts into view
// transitions). Browsers animate their own swipe-back gestures (Safari on
// iOS and macOS), and a page can't tell a swipe from the Back button, so
// the crossfade could play after the browser's own animation. The page
// being left skips its outgoing transition when the navigation is a
// traverse; it has loaded, so this deferred script is already listening.
// Browsers without the Navigation API have no activation there, so a page
// restored from the back/forward cache (as a swipe back usually is) skips
// its incoming one too: this script was listening before it was cached.
export function init(): void {
  addEventListener("pageswap", (e) => {
    if (e.viewTransition && e.activation && e.activation.navigationType === "traverse")
      e.viewTransition.skipTransition();
  });
  let restored = false;
  addEventListener("pageshow", (e) => {
    restored = e.persisted;
  });
  addEventListener("pagereveal", (e) => {
    if (e.viewTransition && restored) e.viewTransition.skipTransition();
  });
}
