import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { isolate, liveRegion } from "../../shared/test/setup";

isolate();

const URL_ = "https://example.com/post";

beforeEach(() => {
  document.body.innerHTML = `
    <button class="share-button" type="button" aria-label="Share" data-share-url="${URL_}" data-share-title="A post" hidden>
      <span class="copied-label" aria-hidden="true"></span>
    </button>`;
});

afterEach(() => {
  for (const name of ["share", "clipboard"]) Reflect.deleteProperty(navigator, name);
});

// browser gives navigator a share sheet and a Clipboard API, or neither.
const browser = ({
  share,
  writeText,
}: {
  share?: (data: ShareData) => Promise<void>;
  writeText?: (s: string) => Promise<void>;
}) => {
  Object.defineProperty(navigator, "share", { value: share, configurable: true });
  Object.defineProperty(navigator, "clipboard", { value: writeText && { writeText }, configurable: true });
};
const button = () => document.querySelector<HTMLButtonElement>("button")!;
const start = async () => (await import("./share")).init();

describe("share", () => {
  it("stays hidden with neither a share sheet nor the Clipboard API", async () => {
    browser({});
    await start();
    expect(button().hidden).toBe(true);
  });

  it.each([
    ["a share sheet", { share: async () => {} }],
    ["the Clipboard API", { writeText: async () => {} }],
  ])("shows the button with %s", async (_, api) => {
    browser(api);
    await start();
    expect(button().hidden).toBe(false);
  });

  it("shows buttons added later with enhanceShare", async () => {
    browser({ writeText: async () => {} });
    const { enhanceShare } = await import("./share");
    const row = document.createElement("div");
    row.innerHTML = `<button data-share-url="${URL_}" hidden></button>`;
    enhanceShare(row);
    expect(row.querySelector("button")!.hidden).toBe(false);
  });

  it("copies the URL, shows Link copied and announces it", async () => {
    vi.useFakeTimers();
    const writeText = vi.fn(async () => {});
    browser({ writeText });
    await start();
    button().click();
    await vi.advanceTimersByTimeAsync(50);
    expect(writeText).toHaveBeenCalledWith(URL_);
    expect("copied" in button().dataset).toBe(true);
    expect(button().querySelector(".copied-label")!.textContent).toBe("Link copied");
    expect(liveRegion()).toBe("Link copied");
    // It shows for 2 seconds.
    await vi.advanceTimersByTimeAsync(1950);
    expect("copied" in button().dataset).toBe(false);
  });

  it("replays a repeated copy without hiding it", async () => {
    vi.useFakeTimers();
    browser({ writeText: async () => {} });
    await start();
    button().click();
    await vi.advanceTimersByTimeAsync(1500);
    button().click();
    await vi.advanceTimersByTimeAsync(0);
    expect(button().dataset.copyRound).toBe("a");
    expect("copied" in button().dataset).toBe(true);
    // Its time starts over.
    await vi.advanceTimersByTimeAsync(1500);
    expect("copied" in button().dataset).toBe(true);
    await vi.advanceTimersByTimeAsync(500);
    expect("copied" in button().dataset).toBe(false);
  });

  it("says Couldn't copy when the copy fails, until the next press", async () => {
    vi.useFakeTimers();
    browser({ writeText: async () => Promise.reject(new DOMException("denied", "NotAllowedError")) });
    await start();
    button().click();
    await vi.advanceTimersByTimeAsync(50);
    expect("copyFailed" in button().dataset).toBe(true);
    expect(button().querySelector(".copied-label")!.textContent).toBe("Couldn't copy");
    expect(liveRegion()).toBe("Couldn't copy the link");
    await vi.advanceTimersByTimeAsync(10_000);
    expect("copyFailed" in button().dataset).toBe(true);
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "a" }));
    expect("copyFailed" in button().dataset).toBe(false);
  });

  it("opens the share sheet where there is one, instead of copying", async () => {
    const share = vi.fn(async () => {});
    const writeText = vi.fn(async () => {});
    browser({ share, writeText });
    await start();
    button().click();
    await vi.waitFor(() => expect(share).toHaveBeenCalledWith({ title: "A post", url: URL_ }));
    expect(writeText).not.toHaveBeenCalled();
  });

  it("copies when the share sheet fails, but not when the reader closes it", async () => {
    const writeText = vi.fn(async () => {});
    browser({ share: async () => Promise.reject(new DOMException("closed", "AbortError")), writeText });
    await start();
    button().click();
    await new Promise((r) => setTimeout(r, 0));
    expect(writeText).not.toHaveBeenCalled();

    browser({ share: async () => Promise.reject(new TypeError("can't share")), writeText });
    button().click();
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(URL_));
  });
});
