import { announce } from "../../shared/lib/announce";
import { busyDelay } from "../../shared/lib/busy-delay";
import { localizeTimes } from "../../shared/lib/local-times";

// Live search: as the reader types in form[data-live-search]'s q, fetch
// the form's page for the query about 250 ms after the last keystroke
// (aborting any request still in flight) and swap its
// [data-search-results] into this page's, so what it shows updates in place.
// It fetches the action with q alone, without the form's hidden scope,
// which only a submit sends, so on Discover typing gets suggestions and
// Enter the results (#666).
// Nothing is fetched under 2 characters; emptying the box fetches the
// no-query page at once, so Discover's sections come back as they are now
// (without the feeds followed meanwhile). The URL follows the query, so a
// reload or Back shows the same results, and the new container's
// data-announce (what the query found, worded by the server: "2 feeds",
// "No feeds") is announced in the polite live region; focus never moves. A swap also
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
export function init(): void {
  const form = document.querySelector<HTMLFormElement>("form[data-live-search]");
  const input = form && form.elements.namedItem("q");
  const results = document.querySelector<HTMLElement>("[data-search-results]");
  if (!form || !(input instanceof HTMLInputElement) || !results) return;
  let timer = 0;
  let inFlight: AbortController | null = null;
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
    }, busyDelay());
  };
  const stopBusy = () => {
    clearTimeout(busyTimer);
    results.removeAttribute("aria-busy");
    delete results.dataset.busy;
    delete spinner.dataset.shown;
  };
  const pageURL = (q: string) => {
    const u = new URL(form.action);
    if (q) u.searchParams.set("q", q);
    return u.href;
  };
  const show = (q: string, container: Element) => {
    form.querySelector(".error")?.remove();
    input.removeAttribute("aria-invalid");
    input.removeAttribute("aria-describedby");
    results.replaceChildren(...container.childNodes);
    localizeTimes(results);
    history.replaceState(null, "", pageURL(q));
  };
  const search = async (q: string) => {
    const controller = new AbortController();
    inFlight = controller;
    startBusy();
    try {
      // No copy in any cache (the server sends a script private, no-cache; see setCacheControl).
      const res = await fetch(pageURL(q), { credentials: "same-origin", cache: "no-store", signal: controller.signal });
      if (res.status !== 200 || res.redirected) throw new Error(`status ${res.status}`);
      const doc = new DOMParser().parseFromString(await res.text(), "text/html");
      const container = doc.querySelector("[data-search-results]");
      if (!container || controller.signal.aborted) return;
      show(q, container);
      const said = container.getAttribute("data-announce");
      if (q && said) announce(said);
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
      void search("");
      return;
    }
    if ([...q].length < 2) {
      stopBusy();
      return;
    }
    timer = setTimeout(() => search(q), 250);
  });
}
