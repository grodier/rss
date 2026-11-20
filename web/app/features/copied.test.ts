import { describe, expect, it, vi } from "vitest";
import { isolate } from "../../shared/test/setup";

isolate();

// setup makes buttons with a copied label and returns copied.ts, set up.
const setup = async (n = 1) => {
  document.body.innerHTML = Array.from(
    { length: n },
    () => `<button type="button"><span class="copied-label" aria-hidden="true"></span></button>`,
  ).join("");
  const copied = await import("./copied");
  copied.init();
  return copied;
};
const buttons = () => [...document.querySelectorAll<HTMLButtonElement>("button")];
const label = (b: HTMLElement) => b.querySelector(".copied-label")!.textContent;

describe("showCopied", () => {
  it("shows the given text on success, for 2 seconds", async () => {
    vi.useFakeTimers();
    const { showCopied } = await setup();
    const [b] = buttons();
    showCopied(b, true, "Code copied");
    expect("copied" in b.dataset).toBe(true);
    expect(label(b)).toBe("Code copied");
    await vi.advanceTimersByTimeAsync(1999);
    expect("copied" in b.dataset).toBe(true);
    await vi.advanceTimersByTimeAsync(1);
    expect("copied" in b.dataset).toBe(false);
    // The label keeps its text, so it fades out with it.
    expect(label(b)).toBe("Code copied");
  });

  it("replays a repeated copy without hiding it", async () => {
    vi.useFakeTimers();
    const { showCopied } = await setup();
    const [b] = buttons();
    showCopied(b, true, "Link copied");
    await vi.advanceTimersByTimeAsync(1500);
    showCopied(b, true, "Link copied");
    expect(b.dataset.copyRound).toBe("a");
    expect("copied" in b.dataset).toBe(true);
    showCopied(b, true, "Link copied");
    expect(b.dataset.copyRound).toBe("b");
    // Its time starts over.
    await vi.advanceTimersByTimeAsync(1500);
    expect("copied" in b.dataset).toBe(true);
    await vi.advanceTimersByTimeAsync(500);
    expect("copied" in b.dataset).toBe(false);
    expect("copyRound" in b.dataset).toBe(false);
  });

  it("says Couldn't copy on failure, until the next press or key", async () => {
    vi.useFakeTimers();
    const { showCopied } = await setup();
    const [b] = buttons();
    showCopied(b, false, "Code copied");
    expect("copyFailed" in b.dataset).toBe(true);
    expect(label(b)).toBe("Couldn't copy");
    await vi.advanceTimersByTimeAsync(10_000);
    expect("copyFailed" in b.dataset).toBe(true);
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "a" }));
    expect("copyFailed" in b.dataset).toBe(false);

    showCopied(b, false, "Code copied");
    document.dispatchEvent(new PointerEvent("pointerdown"));
    expect("copyFailed" in b.dataset).toBe(false);
  });

  it("shows only the latest copy", async () => {
    const { showCopied } = await setup(2);
    const [a, b] = buttons();
    showCopied(a, true, "Link copied");
    showCopied(b, true, "Code copied");
    expect("copied" in a.dataset).toBe(false);
    expect("copied" in b.dataset).toBe(true);
  });

  it("stays while a mouse is over the button, until it leaves", async () => {
    vi.useFakeTimers();
    const { showCopied } = await setup();
    const [b] = buttons();
    b.dispatchEvent(new PointerEvent("pointerdown", { pointerType: "mouse", bubbles: true }));
    vi.spyOn(b, "matches").mockImplementation((s) => s === ":hover");
    showCopied(b, true, "Code copied");
    await vi.advanceTimersByTimeAsync(5000);
    expect("copied" in b.dataset).toBe(true);
    b.dispatchEvent(new PointerEvent("pointerleave"));
    expect("copied" in b.dataset).toBe(false);
  });

  it("stays while the button has keyboard focus, until the next key", async () => {
    vi.useFakeTimers();
    const { showCopied } = await setup();
    const [b] = buttons();
    vi.spyOn(b, "matches").mockImplementation((s) => s === ":focus-visible");
    showCopied(b, true, "Code copied");
    await vi.advanceTimersByTimeAsync(5000);
    expect("copied" in b.dataset).toBe(true);
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab" }));
    expect("copied" in b.dataset).toBe(false);
  });
});
