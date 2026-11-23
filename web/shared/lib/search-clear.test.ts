import { describe, expect, it, vi } from "vitest";
import { isolate } from "../test/setup";

isolate();

// page renders a search box as the search-box partial does, with q as its
// query and the clear button in place or not.
const page = async (q: string, inPlace: boolean) => {
  document.body.innerHTML = `
    <form role="search" action="/saved">
      <div class="search-field">
        <button class="search-submit" type="submit" aria-label="Search"></button>
        <input type="search" name="q" value="${q}">
        <a class="search-clear" href="/saved" aria-label="Clear search" data-search-clear="${inPlace ? "in-place" : ""}"${q ? "" : " hidden"}></a>
      </div>
    </form>`;
  const { init } = await import("./search-clear");
  init();
  const input = document.querySelector<HTMLInputElement>('input[type="search"]')!;
  const clear = document.querySelector<HTMLAnchorElement>("a.search-clear")!;
  const inputs = vi.fn();
  input.addEventListener("input", inputs);
  return { input, clear, inputs };
};

const type = (input: HTMLInputElement, value: string) => {
  input.value = value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
};

const click = (el: HTMLElement, init: MouseEventInit = {}) => {
  const e = new MouseEvent("click", { bubbles: true, cancelable: true, button: 0, ...init });
  el.dispatchEvent(e);
  return e;
};

const escape = (input: HTMLInputElement) => {
  const e = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
  input.dispatchEvent(e);
  return e;
};

describe("search clear", () => {
  it("shows while the box has text and hides when it's emptied", async () => {
    const { input, clear } = await page("", true);
    expect(clear.hidden).toBe(true);
    type(input, "n");
    expect(clear.hidden).toBe(false);
    type(input, "");
    expect(clear.hidden).toBe(true);
  });

  it("matches the box when the page is shown again (Back restores its value)", async () => {
    const { input, clear } = await page("", true);
    input.value = "restored";
    window.dispatchEvent(new Event("pageshow"));
    expect(clear.hidden).toBe(false);
  });

  it("clears in place on a live page: empties the box, tells the page, keeps focus", async () => {
    const { input, clear, inputs } = await page("news", true);
    const e = click(clear);
    expect(e.defaultPrevented).toBe(true);
    expect(input.value).toBe("");
    expect(clear.hidden).toBe(true);
    expect(inputs).toHaveBeenCalledTimes(1);
    expect(document.activeElement).toBe(input);
  });

  it("clears in place on a page showing no query yet", async () => {
    const { input, clear, inputs } = await page("", false);
    type(input, "bread");
    inputs.mockClear();
    const e = click(clear);
    expect(e.defaultPrevented).toBe(true);
    expect(input.value).toBe("");
    expect(inputs).toHaveBeenCalledTimes(1);
  });

  it("follows the link on a page showing a query's results that doesn't update as you type", async () => {
    const { input, clear, inputs } = await page("bread", false);
    const e = click(clear);
    expect(e.defaultPrevented).toBe(false);
    expect(input.value).toBe("bread");
    expect(inputs).not.toHaveBeenCalled();
  });

  it("leaves a modified click to the browser", async () => {
    const { input, clear } = await page("news", true);
    for (const mod of [{ metaKey: true }, { ctrlKey: true }, { shiftKey: true }, { altKey: true }, { button: 1 }]) {
      const e = click(clear, mod);
      expect(e.defaultPrevented).toBe(false);
      expect(input.value).toBe("news");
    }
  });

  it("Escape in a box with text does what the button does", async () => {
    const live = await page("news", true);
    expect(escape(live.input).defaultPrevented).toBe(true);
    expect(live.input.value).toBe("");
    expect(live.inputs).toHaveBeenCalledTimes(1);
  });

  it("Escape on a page showing a query's results follows the link", async () => {
    const { input, clear } = await page("bread", false);
    const clicks = vi.fn((e: Event) => e.preventDefault()); // stop happy-dom navigating
    clear.addEventListener("click", clicks);
    escape(input);
    expect(clicks).toHaveBeenCalledTimes(1);
    expect(input.value).toBe("bread");
  });

  it("leaves Escape in an empty box alone", async () => {
    const { input, inputs } = await page("", true);
    expect(escape(input).defaultPrevented).toBe(false);
    expect(inputs).not.toHaveBeenCalled();
  });

  it("does nothing without a search box", async () => {
    document.body.innerHTML = `<a data-search-clear href="/x"></a>`;
    const { init } = await import("./search-clear");
    expect(() => init()).not.toThrow();
  });
});
