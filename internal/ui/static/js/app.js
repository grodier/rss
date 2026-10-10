// app.js: progressive enhancements. Every page works without it.
(() => {
  "use strict";

  // Local times: <time data-local datetime="…">, rendered in UTC by the
  // server, is shown in the reader's time zone.
  const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
  for (const el of document.querySelectorAll("time[data-local][datetime]")) {
    const d = new Date(el.dateTime);
    if (!Number.isNaN(d.getTime())) el.textContent = fmt.format(d);
  }

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
})();
