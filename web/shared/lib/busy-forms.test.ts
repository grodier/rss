import { beforeEach, describe, expect, it, vi } from "vitest";
import { isolate, liveRegion, pageTransition, submit } from "../test/setup";

isolate();

beforeEach(() => {
  vi.useFakeTimers();
  document.body.innerHTML = `
    <form action="/login" method="post" data-busy>
      <input name="email" value="a@example.com">
      <button class="btn-primary" type="submit" data-busy-label="Logging in…"><svg class="icon"></svg><span data-busy-text>Log in</span></button>
    </form>`;
});

const start = async () => (await import("./busy-forms")).init();
const form = () => document.querySelector<HTMLFormElement>("form")!;
const button = () => document.querySelector<HTMLButtonElement>("button")!;
const text = () => button().querySelector("[data-busy-text]")!.textContent;

describe("busy-forms", () => {
  it("lets the post go ahead, with the button aria-disabled at once and its busy label", async () => {
    await start();
    expect(submit(form(), button())).toBe(false);
    expect(button().getAttribute("aria-disabled")).toBe("true");
    expect(button().disabled).toBe(false);
    expect("busy" in button().dataset).toBe(true);
    expect(text()).toBe("Logging in…");
    expect(button().querySelector("svg.icon")).not.toBeNull();
  });

  it("finds the submit button without a submitter", async () => {
    await start();
    submit(form());
    expect(button().getAttribute("aria-disabled")).toBe("true");
  });

  it("ignores a second submit", async () => {
    await start();
    expect(submit(form(), button())).toBe(false);
    expect(submit(form(), button())).toBe(true);
  });

  it("shows the spinner and announces the label after --delay-busy", async () => {
    await start();
    submit(form(), button());
    await vi.advanceTimersByTimeAsync(299);
    expect(button().querySelector(".btn-spinner")).toBeNull();
    expect(liveRegion()).toBe("");
    await vi.advanceTimersByTimeAsync(1);
    const spinner = button().querySelector(".btn-spinner")!;
    expect(spinner.getAttribute("aria-hidden")).toBe("true");
    expect(spinner.nextElementSibling?.hasAttribute("data-busy-text")).toBe(true);
    await vi.advanceTimersByTimeAsync(50);
    expect(liveRegion()).toBe("Logging in…");
  });

  it("restores the button on pageshow from the back/forward cache", async () => {
    await start();
    submit(form(), button());
    pageTransition("pageshow", false);
    expect(button().getAttribute("aria-disabled")).toBe("true");

    await vi.advanceTimersByTimeAsync(300);
    pageTransition("pageshow", true);
    expect(button().hasAttribute("aria-disabled")).toBe(false);
    expect("busy" in button().dataset).toBe(false);
    expect(text()).toBe("Log in");
    expect(button().querySelector(".btn-spinner")).toBeNull();
    expect(button().style.minInlineSize).toBe("");
    expect(submit(form(), button())).toBe(false); // it can be submitted again
  });

  it("restores before the spinner, which then never shows", async () => {
    await start();
    submit(form(), button());
    pageTransition("pageshow", true);
    await vi.advanceTimersByTimeAsync(1000);
    expect(button().querySelector(".btn-spinner")).toBeNull();
  });

  it("ignores forms that aren't busy forms, and submits another listener prevented", async () => {
    document.addEventListener("submit", (e) => {
      if ((e.target as HTMLFormElement).id === "prevented") e.preventDefault();
    });
    await start();
    form().id = "prevented";
    submit(form(), button());
    expect(button().hasAttribute("aria-disabled")).toBe(false);

    document.body.innerHTML = `<form action="/x" method="post"><button>Go</button></form>`;
    submit(form(), button());
    expect(button().hasAttribute("aria-disabled")).toBe(false);
  });
});
