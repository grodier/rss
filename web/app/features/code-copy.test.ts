import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { isolate, liveRegion } from "../../shared/test/setup";

isolate();

beforeEach(() => {
  document.body.innerHTML = `
    <div class="article-body">
      <p>Run <code>make</code> first.</p>
      <pre><code>go build ./...
go test ./...
</code></pre>
      <pre>npm ci</pre>
    </div>
    <pre>not in an article</pre>`;
});

afterEach(() => {
  Reflect.deleteProperty(navigator, "clipboard");
});

// clipboard gives navigator a Clipboard API with writeText, or none.
const clipboard = (writeText?: (s: string) => Promise<void>) =>
  Object.defineProperty(navigator, "clipboard", { value: writeText && { writeText }, configurable: true });
const start = async () => (await import("./code-copy")).init();
const buttons = () => [...document.querySelectorAll<HTMLButtonElement>("button")];

describe("code-copy", () => {
  it("adds one Copy button to each code block in the article", async () => {
    clipboard(async () => {});
    await start();
    await start();
    const pres = document.querySelectorAll(".article-body pre");
    expect(buttons()).toHaveLength(2);
    for (const pre of pres) {
      const b = pre.querySelectorAll("button");
      expect(b).toHaveLength(1);
      expect(b[0].getAttribute("aria-label")).toBe("Copy code");
      expect(b[0].type).toBe("button");
    }
    expect(document.querySelector("p button")).toBeNull();
    expect(document.querySelector("body > pre button")).toBeNull();
  });

  it("adds none without the Clipboard API", async () => {
    clipboard();
    await start();
    expect(buttons()).toHaveLength(0);
  });

  it("copies the block's text, less one trailing line break, and says Code copied", async () => {
    vi.useFakeTimers();
    const writeText = vi.fn(async (_: string) => {});
    clipboard(writeText);
    await start();
    const [first, second] = buttons();
    first.click();
    await vi.advanceTimersByTimeAsync(50);
    expect(writeText).toHaveBeenLastCalledWith("go build ./...\ngo test ./...");
    expect("copied" in first.dataset).toBe(true);
    expect(first.querySelector(".copied-label")!.textContent).toBe("Code copied");
    expect(liveRegion()).toBe("Code copied");

    second.click();
    await vi.advanceTimersByTimeAsync(50);
    expect(writeText).toHaveBeenLastCalledWith("npm ci");
  });

  it("says Couldn't copy when the copy fails", async () => {
    vi.useFakeTimers();
    clipboard(async () => Promise.reject(new DOMException("denied", "NotAllowedError")));
    await start();
    const [b] = buttons();
    b.click();
    await vi.advanceTimersByTimeAsync(50);
    expect("copyFailed" in b.dataset).toBe(true);
    expect(b.querySelector(".copied-label")!.textContent).toBe("Couldn't copy");
    expect(liveRegion()).toBe("Couldn't copy");
  });
});
