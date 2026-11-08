export interface PollSelfOptions {
  // onPage gets each 200's parsed page and returns true to stop polling.
  onPage: (doc: Document) => boolean;
  // onRedirect gets the URL a redirect led to (default: location.assign).
  onRedirect?: (url: string) => void;
}

// pollSelf fetches location.href 2 s after the previous answer arrived
// (never overlapping), only while the page is visible. A redirect calls
// onRedirect(url) (default: location.assign) and stops; a 200 calls
// onPage(doc) with the parsed page, which returns true to stop; anything
// else, or a network error, polls again. It's what the lookup and import
// pages use instead of the meta refresh they use without JavaScript.
export function pollSelf({ onPage, onRedirect = (url) => location.assign(url) }: PollSelfOptions): void {
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
        onRedirect(res.url);
        return;
      }
      if (res.status !== 200) return;
      const doc = new DOMParser().parseFromString(await res.text(), "text/html");
      if (onPage(doc)) finished = true;
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
