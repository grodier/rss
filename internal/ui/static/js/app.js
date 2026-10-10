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
})();
