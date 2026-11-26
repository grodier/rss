import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { isolate, liveRegion } from "../../shared/test/setup";

isolate();

const feedURL = "https://paperround.app/shared/abc/feed.atom";
const pageURL = "https://paperround.app/shared/abc";

beforeEach(() => {
  document.body.innerHTML = `
    <div class="field">
      <label for="share-feed-url">Feed link</label>
      <div class="copy-field">
        <input type="text" id="share-feed-url" value="${feedURL}" readonly />
        <button class="btn-sm copy-button" type="button" data-copy="share-feed-url" hidden><svg class="icon icon-copy"></svg><svg class="icon icon-check"></svg>Copy<span class="visually-hidden"> feed link</span><span class="copied-label" aria-hidden="true"></span></button>
      </div>
    </div>
    <div class="field">
      <label for="share-page-url">Page link</label>
      <div class="copy-field">
        <input type="text" id="share-page-url" value="${pageURL}" readonly />
        <button class="btn-sm copy-button" type="button" data-copy="share-page-url" hidden><svg class="icon icon-copy"></svg><svg class="icon icon-check"></svg>Copy<span class="visually-hidden"> page link</span><span class="copied-label" aria-hidden="true"></span></button>
      </div>
    </div>
    <input type="text" id="elsewhere" value="not a copy field" readonly />`;
});

afterEach(() => {
  Reflect.deleteProperty(navigator, "clipboard");
});

// clipboard gives navigator a Clipboard API with writeText, or none.
const clipboard = (writeText?: (s: string) => Promise<void>) =>
  Object.defineProperty(navigator, "clipboard", { value: writeText && { writeText }, configurable: true });
const start = async () => (await import("./copy-field")).init();
const button = (id: string) => document.querySelector<HTMLButtonElement>(`button[data-copy="${id}"]`)!;
const input = (id: string) => document.getElementById(id) as HTMLInputElement;
const selected = (el: HTMLInputElement) => [el.selectionStart, el.selectionEnd];

describe("copy-field", () => {
  it("shows the Copy buttons", async () => {
    clipboard(async () => {});
    await start();
    expect(button("share-feed-url").hidden).toBe(false);
    expect(button("share-page-url").hidden).toBe(false);
  });

  it("keeps them hidden without the Clipboard API", async () => {
    clipboard();
    await start();
    expect(button("share-feed-url").hidden).toBe(true);
    expect(button("share-page-url").hidden).toBe(true);
  });

  it("copies the named input's value, shows it was copied and announces it", async () => {
    vi.useFakeTimers();
    const writeText = vi.fn(async (_: string) => {});
    clipboard(writeText);
    await start();
    const feed = button("share-feed-url");
    feed.click();
    await vi.advanceTimersByTimeAsync(50);
    expect(writeText).toHaveBeenLastCalledWith(feedURL);
    expect("copied" in feed.dataset).toBe(true);
    expect(feed.querySelector(".copied-label")!.textContent).toBe("Feed link copied");
    expect(liveRegion()).toBe("Feed link copied");

    const page = button("share-page-url");
    page.click();
    await vi.advanceTimersByTimeAsync(50);
    expect(writeText).toHaveBeenLastCalledWith(pageURL);
    expect("copied" in page.dataset).toBe(true);
    expect("copied" in feed.dataset).toBe(false); // only the latest copy shows
    expect(liveRegion()).toBe("Page link copied");
  });

  it("selects the link when the copy fails, so the reader can copy it", async () => {
    vi.useFakeTimers();
    clipboard(async () => Promise.reject(new DOMException("denied", "NotAllowedError")));
    await start();
    const page = button("share-page-url");
    page.click();
    await vi.advanceTimersByTimeAsync(50);
    expect("copyFailed" in page.dataset).toBe(true);
    expect(page.querySelector(".copied-label")!.textContent).toBe("Couldn't copy");
    expect(document.activeElement).toBe(input("share-page-url"));
    expect(selected(input("share-page-url"))).toEqual([0, pageURL.length]);
    expect(liveRegion()).toBe("Couldn't copy the link. It's selected, so you can copy it.");
  });

  it("names what it couldn't copy by data-copy-what", async () => {
    vi.useFakeTimers();
    clipboard(async () => Promise.reject(new DOMException("denied", "NotAllowedError")));
    await start();
    const page = button("share-page-url");
    page.dataset.copyWhat = "your address";
    page.click();
    await vi.advanceTimersByTimeAsync(50);
    expect(liveRegion()).toBe("Couldn't copy your address. It's selected, so you can copy it.");
  });

  it("does nothing for a button naming a missing input", async () => {
    vi.useFakeTimers();
    const writeText = vi.fn(async (_: string) => {});
    clipboard(writeText);
    button("share-feed-url").dataset.copy = "nowhere";
    await start();
    const b = button("nowhere");
    expect(b.hidden).toBe(true);
    b.click();
    await vi.advanceTimersByTimeAsync(50);
    expect(writeText).not.toHaveBeenCalled();
    expect("copied" in b.dataset).toBe(false);
    expect(liveRegion()).toBe("");
  });

  it("selects a copy field's link when it takes focus, with or without the Clipboard API", async () => {
    clipboard();
    await start();
    const feed = input("share-feed-url");
    feed.focus();
    expect(selected(feed)).toEqual([0, feedURL.length]);

    const other = input("elsewhere");
    other.setSelectionRange(0, 0);
    other.focus();
    expect(selected(other)).toEqual([0, 0]);
  });

  it("selects the link on the press that focuses it, but not on later presses", async () => {
    clipboard();
    await start();
    const feed = input("share-feed-url");
    // A press: pointerdown, then focus, then the browser places the caret,
    // then click.
    const press = (focus: boolean) => {
      feed.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
      if (focus) feed.focus();
      feed.setSelectionRange(5, 5);
      feed.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    };
    press(true);
    expect(selected(feed)).toEqual([0, feedURL.length]);
    press(false);
    expect(selected(feed)).toEqual([5, 5]);
  });

  it("keeps a part of the link the press that focused it dragged out", async () => {
    clipboard();
    await start();
    const feed = input("share-feed-url");
    feed.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
    feed.focus();
    feed.setSelectionRange(8, 20);
    feed.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(selected(feed)).toEqual([8, 20]);
  });
});
