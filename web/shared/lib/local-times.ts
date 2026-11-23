// Local times: <time data-local datetime="…">, rendered in UTC by the
// server (the local-time, local-date and local-day partials), is shown in
// the reader's date format and time zone; data-local="date" shows the date
// only, and data-local="day" a date-only feed's calendar day, in the
// reader's format but never moved to their time zone. data-local="since"
// (the since-date partial) keeps its relative text ("4 days ago") and shows
// the date in its title instead. Takes a root so rows added to the page
// later can be enhanced too.
let fmt: Intl.DateTimeFormat | undefined;
let dateFmt: Intl.DateTimeFormat | undefined;
let dayFmt: Intl.DateTimeFormat | undefined;

function formatter(kind: string | undefined): Intl.DateTimeFormat {
  switch (kind) {
    case "date":
    case "since":
      return (dateFmt ??= new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }));
    case "day":
      // datetime is a bare date, which Date reads as midnight UTC.
      return (dayFmt ??= new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeZone: "UTC" }));
    default:
      return (fmt ??= new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }));
  }
}

export function localizeTimes(root: ParentNode): void {
  for (const el of root.querySelectorAll<HTMLTimeElement>("time[data-local][datetime]")) {
    const d = new Date(el.dateTime);
    if (Number.isNaN(d.getTime())) continue;
    const text = formatter(el.dataset.local).format(d);
    if (el.dataset.local === "since") el.title = text;
    else el.textContent = text;
  }
}
