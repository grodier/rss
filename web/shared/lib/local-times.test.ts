import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { isolate } from "../test/setup";

isolate();

// West of UTC, so a date-only day moved to the reader's zone would land on
// the day before.
beforeEach(() => {
  vi.stubEnv("TZ", "America/New_York");
});
afterEach(() => {
  vi.unstubAllEnvs();
});

const medium = (options: Intl.DateTimeFormatOptions = {}) =>
  new Intl.DateTimeFormat(undefined, { dateStyle: "medium", ...options });

async function localize(html: string): Promise<string | null> {
  document.body.innerHTML = html;
  const { localizeTimes } = await import("./local-times");
  localizeTimes(document);
  return document.querySelector("time")!.textContent;
}

describe("localizeTimes", () => {
  it("shows a time in the reader's format and zone", async () => {
    const text = await localize(`<time data-local datetime="2026-10-09T02:30:00Z">9 Oct 2026 02:30 UTC</time>`);
    expect(text).toBe(medium({ timeStyle: "short" }).format(new Date("2026-10-09T02:30:00Z")));
  });

  it("shows a date in the reader's format and zone", async () => {
    const text = await localize(`<time data-local="date" datetime="2026-10-09T02:30:00Z">9 Oct 2026</time>`);
    expect(text).toBe(medium().format(new Date("2026-10-09T02:30:00Z")));
  });

  it("shows a date-only day in the reader's format without moving it to their zone", async () => {
    const text = await localize(`<time data-local="day" datetime="2026-10-09">9 Oct 2026</time>`);
    const day = new Date("2026-10-09");
    expect(text).toBe(medium({ timeZone: "UTC" }).format(day));
    expect(text).not.toBe(medium().format(day)); // 8 Oct in New York
  });

  it("keeps a relative date's text and shows its date in the reader's format and zone in its title", async () => {
    document.body.innerHTML = `<time data-local="since" datetime="2026-10-09T02:30:00Z" title="9 Oct 2026">4 days ago</time>`;
    const { localizeTimes } = await import("./local-times");
    localizeTimes(document);
    const el = document.querySelector("time")!;
    expect(el.textContent).toBe("4 days ago");
    expect(el.title).toBe(medium().format(new Date("2026-10-09T02:30:00Z")));
    expect(el.title).not.toBe("9 Oct 2026"); // 8 Oct in New York
  });

  it("leaves a time it can't read and one without data-local as the server wrote them", async () => {
    document.body.innerHTML =
      `<time data-local datetime="not a time">as written</time>` + `<time datetime="2026-10-09">9 Oct 2026</time>`;
    const { localizeTimes } = await import("./local-times");
    localizeTimes(document);
    expect([...document.querySelectorAll("time")].map((t) => t.textContent)).toEqual(["as written", "9 Oct 2026"]);
  });

  it("localizes only inside the root it's given", async () => {
    document.body.innerHTML =
      `<div id="new"><time data-local="day" datetime="2026-10-09">9 Oct 2026</time></div>` +
      `<time data-local="day" datetime="2026-10-10">10 Oct 2026</time>`;
    const { localizeTimes } = await import("./local-times");
    localizeTimes(document.getElementById("new")!);
    const [inside, outside] = document.querySelectorAll("time");
    expect(inside.textContent).toBe(medium({ timeZone: "UTC" }).format(new Date("2026-10-09")));
    expect(outside.textContent).toBe("10 Oct 2026");
  });
});
