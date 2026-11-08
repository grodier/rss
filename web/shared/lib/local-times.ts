// Local times: <time data-local datetime="…">, rendered in UTC by the
// server (the local-time and local-date partials), is shown in the
// reader's time zone; data-local="date" shows the date only. Takes a root
// so rows added to the page later can be enhanced too.
let fmt: Intl.DateTimeFormat | undefined;
let dateFmt: Intl.DateTimeFormat | undefined;

export function localizeTimes(root: ParentNode): void {
  fmt ??= new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
  dateFmt ??= new Intl.DateTimeFormat(undefined, { dateStyle: "medium" });
  for (const el of root.querySelectorAll<HTMLTimeElement>("time[data-local][datetime]")) {
    const d = new Date(el.dateTime);
    if (!Number.isNaN(d.getTime())) el.textContent = (el.dataset.local === "date" ? dateFmt : fmt).format(d);
  }
}
